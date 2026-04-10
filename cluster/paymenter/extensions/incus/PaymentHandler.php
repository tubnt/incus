<?php

namespace Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Cache;

/**
 * 支付处理器 — 支付回调、自动续费、退款逻辑
 *
 * 职责：
 * - 支付成功回调 → 触发 createServer
 * - 支付失败/超时 30 分钟自动取消
 * - 退款（未使用天数按比例退到余额）
 * - 自动续费（余额扣除 + 失败重试 3 次 + 通知）
 */
class PaymentHandler
{
    /** @var int 支付超时时间（秒） */
    private const PAYMENT_TIMEOUT = 1800; // 30 分钟

    /** @var int 自动续费最大重试次数 */
    private const RENEWAL_MAX_RETRIES = 3;

    /** @var int 续费重试间隔（秒），逐次递增 */
    private const RENEWAL_RETRY_INTERVALS = [3600, 7200, 14400]; // 1h, 2h, 4h

    private IncusExtension $extension;

    public function __construct(IncusExtension $extension)
    {
        $this->extension = $extension;
    }

    // =========================================================================
    // 支付成功回调
    // =========================================================================

    /**
     * 处理支付成功回调
     *
     * 流程：验证订单 → 标记已支付 → 触发 VM 创建 → 发送通知
     *
     * @param int    $orderId    订单 ID
     * @param string $gatewayTxn 支付网关交易号
     * @param float  $amount     实际支付金额
     * @return array{success: bool, message: string, vm_name?: string}
     */
    public function handlePaymentSuccess(int $orderId, string $gatewayTxn, float $amount): array
    {
        return DB::transaction(function () use ($orderId, $gatewayTxn, $amount) {
            $order = DB::table('orders')->where('id', $orderId)->lockForUpdate()->first();

            if (!$order) {
                Log::error('[支付回调] 订单不存在', ['order_id' => $orderId]);
                return ['success' => false, 'message' => '订单不存在'];
            }

            if ($order->status === 'paid') {
                Log::warning('[支付回调] 重复回调已忽略', ['order_id' => $orderId]);
                return ['success' => true, 'message' => '订单已处理'];
            }

            if ($order->status !== 'pending') {
                Log::error('[支付回调] 订单状态异常', [
                    'order_id' => $orderId,
                    'status'   => $order->status,
                ]);
                return ['success' => false, 'message' => '订单状态异常: ' . $order->status];
            }

            // 金额校验（允许 0.01 误差，处理浮点精度）
            if (abs($amount - $order->total) > 0.01) {
                Log::error('[支付回调] 金额不匹配', [
                    'order_id' => $orderId,
                    'expected' => $order->total,
                    'actual'   => $amount,
                ]);
                return ['success' => false, 'message' => '金额不匹配'];
            }

            // 更新订单状态
            DB::table('orders')->where('id', $orderId)->update([
                'status'      => 'paid',
                'gateway_txn' => $gatewayTxn,
                'paid_at'     => now(),
                'updated_at'  => now(),
            ]);

            // 记录支付日志
            $this->logPayment($orderId, 'payment_success', $amount, $gatewayTxn);

            // 触发 VM 创建
            $product = DB::table('products')->where('id', $order->product_id)->first();
            $user = DB::table('users')->where('id', $order->user_id)->first();
            $options = json_decode($order->options ?? '{}', true);

            try {
                $result = $this->extension->createServer($user, [
                    'order_id'   => $orderId,
                    'product_id' => $order->product_id,
                ], $order, $product, $options);

                Log::info('[支付回调] VM 创建成功', [
                    'order_id' => $orderId,
                    'vm_name'  => $result['vm_name'] ?? 'unknown',
                ]);

                return [
                    'success' => true,
                    'message' => 'VM 创建成功',
                    'vm_name' => $result['vm_name'] ?? null,
                ];
            } catch (\Throwable $e) {
                // VM 创建失败 → 自动退款 + 回收资源 + P1 告警
                Log::error('[支付回调] VM 创建失败，执行回滚', [
                    'order_id' => $orderId,
                    'error'    => $e->getMessage(),
                ]);

                $this->handleCreateFailure($orderId, $order, $amount, $e);

                return [
                    'success' => false,
                    'message' => 'VM 创建失败，已自动退款到余额',
                ];
            }
        });
    }

    /**
     * VM 创建失败的回滚处理
     */
    private function handleCreateFailure(int $orderId, object $order, float $amount, \Throwable $e): void
    {
        // 退款到余额
        $this->refundToBalance($order->user_id, $amount, "订单 #{$orderId} VM 创建失败自动退款");

        // 更新订单状态
        DB::table('orders')->where('id', $orderId)->update([
            'status'     => 'failed',
            'note'       => '创建失败: ' . mb_substr($e->getMessage(), 0, 500),
            'updated_at' => now(),
        ]);

        // 记录审计日志
        $this->logPayment($orderId, 'create_failed_refund', $amount, null, [
            'error' => $e->getMessage(),
        ]);

        // 通知用户
        $this->notifyUser($order->user_id, 'create_failed', [
            'order_id' => $orderId,
            'amount'   => $amount,
        ]);

        // P1 告警通知运维
        Log::critical('[P1 告警] VM 创建失败', [
            'order_id' => $orderId,
            'user_id'  => $order->user_id,
            'error'    => $e->getMessage(),
        ]);
    }

    // =========================================================================
    // 支付失败 / 超时取消
    // =========================================================================

    /**
     * 处理支付失败回调
     */
    public function handlePaymentFailure(int $orderId, string $reason = ''): array
    {
        return DB::transaction(function () use ($orderId, $reason) {
            $order = DB::table('orders')->where('id', $orderId)->lockForUpdate()->first();

            if (!$order || $order->status !== 'pending') {
                return ['success' => false, 'message' => '订单不存在或已处理'];
            }

            DB::table('orders')->where('id', $orderId)->update([
                'status'     => 'cancelled',
                'note'       => '支付失败: ' . $reason,
                'updated_at' => now(),
            ]);

            $this->logPayment($orderId, 'payment_failed', 0, null, ['reason' => $reason]);

            Log::info('[支付] 订单支付失败已取消', ['order_id' => $orderId, 'reason' => $reason]);

            return ['success' => true, 'message' => '订单已取消'];
        });
    }

    /**
     * 清理超时未支付的订单（由 Cron 定期调用）
     *
     * 超过 30 分钟未支付的 pending 订单自动取消
     *
     * @return int 取消的订单数量
     */
    public function cancelTimedOutOrders(): int
    {
        $cutoff = now()->subSeconds(self::PAYMENT_TIMEOUT);

        $timedOutOrderIds = DB::table('orders')
            ->where('status', 'pending')
            ->where('created_at', '<', $cutoff)
            ->pluck('id');

        $count = 0;
        foreach ($timedOutOrderIds as $orderId) {
            // 逐单事务 + 行锁，防止与 handlePaymentSuccess 竞争
            $cancelled = DB::transaction(function () use ($orderId) {
                $order = DB::table('orders')->where('id', $orderId)->lockForUpdate()->first();

                // 二次检查状态，可能已被支付回调处理
                if (!$order || $order->status !== 'pending') {
                    return false;
                }

                DB::table('orders')->where('id', $orderId)->update([
                    'status'     => 'cancelled',
                    'note'       => '支付超时自动取消（30 分钟）',
                    'updated_at' => now(),
                ]);

                $this->logPayment($orderId, 'payment_timeout', 0);

                // 回收预分配的 IP（如有）
                DB::table('ip_addresses')
                    ->where('order_id', $orderId)
                    ->where('status', 'allocated')
                    ->update([
                        'status'   => 'available',
                        'vm_name'  => null,
                        'order_id' => null,
                    ]);

                return true;
            });

            if ($cancelled) {
                $count++;
            }
        }

        if ($count > 0) {
            Log::info('[支付] 超时订单批量取消', ['count' => $count]);
        }

        return $count;
    }

    // =========================================================================
    // 退款逻辑
    // =========================================================================

    /**
     * 按比例退款到余额
     *
     * 计算未使用天数，按比例退款到用户账户余额（非原路退款）。
     *
     * @param int    $orderId 订单 ID
     * @param string $reason  退款原因
     * @return array{success: bool, refund_amount: float, message: string}
     */
    public function refundProrated(int $orderId, string $reason = '用户申请退款'): array
    {
        return DB::transaction(function () use ($orderId, $reason) {
            $order = DB::table('orders')->where('id', $orderId)->lockForUpdate()->first();

            if (!$order) {
                return ['success' => false, 'refund_amount' => 0, 'message' => '订单不存在'];
            }

            if (!in_array($order->status, ['paid', 'active'])) {
                return ['success' => false, 'refund_amount' => 0, 'message' => '订单状态不支持退款'];
            }

            // 计算已使用天数和退款金额
            $paidAt = new \DateTime($order->paid_at);
            $expiresAt = new \DateTime($order->expires_at);
            $now = new \DateTime();

            $totalDays = max(1, $paidAt->diff($expiresAt)->days);
            $usedDays = max(1, $paidAt->diff($now)->days); // 最少按 1 天计费
            $remainingDays = max(0, $totalDays - $usedDays);

            $refundAmount = round(($order->total / $totalDays) * $remainingDays, 2);

            if ($refundAmount <= 0) {
                return ['success' => false, 'refund_amount' => 0, 'message' => '无可退款金额'];
            }

            // 退款到余额
            $this->refundToBalance($order->user_id, $refundAmount, $reason);

            // 更新订单
            DB::table('orders')->where('id', $orderId)->update([
                'status'        => 'refunded',
                'refund_amount' => $refundAmount,
                'refunded_at'   => now(),
                'note'          => "{$reason}（已用 {$usedDays} 天，退款 {$remainingDays} 天）",
                'updated_at'    => now(),
            ]);

            $this->logPayment($orderId, 'refund_prorated', $refundAmount, null, [
                'total_days'     => $totalDays,
                'used_days'      => $usedDays,
                'remaining_days' => $remainingDays,
                'reason'         => $reason,
            ]);

            Log::info('[退款] 按比例退款完成', [
                'order_id'      => $orderId,
                'refund_amount' => $refundAmount,
                'used_days'     => $usedDays,
            ]);

            return [
                'success'       => true,
                'refund_amount' => $refundAmount,
                'message'       => "已退款 {$refundAmount} 到账户余额",
            ];
        });
    }

    /**
     * 退款到用户余额
     */
    private function refundToBalance(int $userId, float $amount, string $description): void
    {
        DB::table('users')->where('id', $userId)->increment('balance', $amount);

        DB::table('balance_transactions')->insert([
            'user_id'     => $userId,
            'type'        => 'refund',
            'amount'      => $amount,
            'description' => $description,
            'created_at'  => now(),
        ]);
    }

    // =========================================================================
    // 自动续费
    // =========================================================================

    /**
     * 执行自动续费（由 Cron 每日调用）
     *
     * 流程：
     * 1. 查询启用自动续费且即将到期的订单
     * 2. 尝试从余额扣除续费金额
     * 3. 扣费失败则记录重试次数，最多重试 3 次
     * 4. 3 次均失败则通知用户手动续费
     *
     * @return array{renewed: int, failed: int, skipped: int}
     */
    public function processAutoRenewals(): array
    {
        $stats = ['renewed' => 0, 'failed' => 0, 'skipped' => 0];

        // 查询需要续费的订单：启用自动续费 + 到期日在 24 小时内
        $orders = DB::table('orders')
            ->where('auto_renew', true)
            ->where('status', 'active')
            ->where('expires_at', '<=', now()->addDay())
            ->where('expires_at', '>', now())
            ->get();

        foreach ($orders as $order) {
            $result = $this->attemptRenewal($order);

            if ($result === 'renewed') {
                $stats['renewed']++;
            } elseif ($result === 'failed') {
                $stats['failed']++;
            } else {
                $stats['skipped']++;
            }
        }

        if ($stats['renewed'] > 0 || $stats['failed'] > 0) {
            Log::info('[自动续费] 批量处理完成', $stats);
        }

        return $stats;
    }

    /**
     * 尝试单个订单续费
     *
     * @return string 'renewed' | 'failed' | 'skipped'
     */
    private function attemptRenewal(object $order): string
    {
        $lockKey = "renewal_lock:{$order->id}";

        // 防止并发处理
        if (!Cache::add($lockKey, true, 300)) {
            return 'skipped';
        }

        try {
            return DB::transaction(function () use ($order) {
                $user = DB::table('users')->where('id', $order->user_id)->lockForUpdate()->first();
                $product = DB::table('products')->where('id', $order->product_id)->first();

                if (!$user || !$product) {
                    return 'skipped';
                }

                $renewalAmount = $product->price;

                // 检查余额是否充足
                if ($user->balance >= $renewalAmount) {
                    return $this->executeRenewal($order, $user, $renewalAmount);
                }

                // 余额不足 → 记录失败 + 重试逻辑
                return $this->handleRenewalFailure($order, $user, $renewalAmount);
            });
        } finally {
            Cache::forget($lockKey);
        }
    }

    /**
     * 执行余额扣除续费
     */
    private function executeRenewal(object $order, object $user, float $amount): string
    {
        // 扣除余额
        DB::table('users')->where('id', $user->id)->decrement('balance', $amount);

        // 记录余额变动
        DB::table('balance_transactions')->insert([
            'user_id'     => $user->id,
            'type'        => 'renewal',
            'amount'      => -$amount,
            'description' => "订单 #{$order->id} 自动续费",
            'created_at'  => now(),
        ]);

        // 延长到期时间（按原周期）
        $currentExpiry = new \DateTime($order->expires_at);
        $newExpiry = clone $currentExpiry;
        $newExpiry->modify('+1 month');

        DB::table('orders')->where('id', $order->id)->update([
            'expires_at'      => $newExpiry->format('Y-m-d H:i:s'),
            'renewal_retries' => 0,
            'updated_at'      => now(),
        ]);

        $this->logPayment($order->id, 'auto_renewal', $amount, null, [
            'old_expiry' => $currentExpiry->format('Y-m-d'),
            'new_expiry' => $newExpiry->format('Y-m-d'),
        ]);

        // 通知用户续费成功
        $this->notifyUser($user->id, 'renewal_success', [
            'order_id'   => $order->id,
            'amount'     => $amount,
            'new_expiry' => $newExpiry->format('Y-m-d'),
        ]);

        Log::info('[自动续费] 续费成功', [
            'order_id'   => $order->id,
            'amount'     => $amount,
            'new_expiry' => $newExpiry->format('Y-m-d'),
        ]);

        return 'renewed';
    }

    /**
     * 处理续费失败（余额不足）
     *
     * 重试逻辑：最多重试 3 次，间隔递增（1h → 2h → 4h）
     */
    private function handleRenewalFailure(object $order, object $user, float $amount): string
    {
        $retries = ($order->renewal_retries ?? 0) + 1;

        DB::table('orders')->where('id', $order->id)->update([
            'renewal_retries'    => $retries,
            'last_renewal_retry' => now(),
            'updated_at'         => now(),
        ]);

        $this->logPayment($order->id, 'renewal_failed', 0, null, [
            'retry'   => $retries,
            'balance' => $user->balance,
            'needed'  => $amount,
        ]);

        if ($retries >= self::RENEWAL_MAX_RETRIES) {
            // 3 次均失败 → 通知用户手动续费，禁用自动续费
            DB::table('orders')->where('id', $order->id)->update([
                'auto_renew' => false,
                'updated_at' => now(),
            ]);

            $this->notifyUser($user->id, 'renewal_final_failure', [
                'order_id'    => $order->id,
                'amount'      => $amount,
                'balance'     => $user->balance,
                'expires_at'  => $order->expires_at,
            ]);

            Log::warning('[自动续费] 3 次重试均失败，已禁用自动续费', [
                'order_id' => $order->id,
                'user_id'  => $user->id,
            ]);
        } else {
            // 通知用户余额不足
            $this->notifyUser($user->id, 'renewal_insufficient_balance', [
                'order_id'   => $order->id,
                'amount'     => $amount,
                'balance'    => $user->balance,
                'retry'      => $retries,
                'max_retry'  => self::RENEWAL_MAX_RETRIES,
                'expires_at' => $order->expires_at,
            ]);
        }

        return 'failed';
    }

    /**
     * 处理续费重试（由 Cron 调用，检查需要重试的订单）
     *
     * @return int 重试成功的数量
     */
    public function processRenewalRetries(): int
    {
        $renewed = 0;

        for ($retry = 0; $retry < self::RENEWAL_MAX_RETRIES; $retry++) {
            $interval = self::RENEWAL_RETRY_INTERVALS[$retry] ?? 14400;

            $orders = DB::table('orders')
                ->where('auto_renew', true)
                ->where('status', 'active')
                ->where('renewal_retries', $retry + 1)
                ->where('last_renewal_retry', '<=', now()->subSeconds($interval))
                ->get();

            foreach ($orders as $order) {
                $result = $this->attemptRenewal($order);
                if ($result === 'renewed') {
                    $renewed++;
                }
            }
        }

        return $renewed;
    }

    // =========================================================================
    // 辅助方法
    // =========================================================================

    /**
     * 记录支付日志
     */
    private function logPayment(
        int $orderId,
        string $event,
        float $amount,
        ?string $gatewayTxn = null,
        array $meta = []
    ): void {
        DB::table('payment_logs')->insert([
            'order_id'    => $orderId,
            'event'       => $event,
            'amount'      => $amount,
            'gateway_txn' => $gatewayTxn,
            'meta'        => json_encode($meta, JSON_UNESCAPED_UNICODE),
            'created_at'  => now(),
        ]);
    }

    /**
     * 发送用户通知
     */
    private function notifyUser(int $userId, string $template, array $data): void
    {
        try {
            $user = DB::table('users')->where('id', $userId)->first();
            if (!$user || !$user->email) {
                return;
            }

            DB::table('notifications')->insert([
                'user_id'    => $userId,
                'type'       => $template,
                'data'       => json_encode($data, JSON_UNESCAPED_UNICODE),
                'read_at'    => null,
                'created_at' => now(),
            ]);

            // 邮件通知由 queue worker 异步处理
            // Mail::to($user->email)->queue(new PaymentNotification($template, $data));
        } catch (\Throwable $e) {
            Log::error('[通知] 发送失败', [
                'user_id'  => $userId,
                'template' => $template,
                'error'    => $e->getMessage(),
            ]);
        }
    }
}
