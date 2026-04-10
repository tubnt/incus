<?php

namespace Extensions\Incus;

use Extensions\Incus\Notifications\SuspensionNotice;
use Extensions\Incus\Notifications\DeletionNotice;
use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * 到期处理器
 *
 * D+0: 暂停 VM（incus stop）
 * D+7: 删除 VM + 回收 IP
 *
 * 幂等设计：重复执行不会重复操作，通过检查 VM 当前状态避免重复动作。
 */
class ExpiryHandler
{
    public function __construct(
        private IncusClient $client,
    ) {}

    /**
     * 暂停到期 VM（D+0）
     *
     * 幂等：仅对状态为 Running 的已到期 VM 执行 stop 操作。
     */
    public function suspendExpired(): array
    {
        $suspended = [];

        $orders = DB::table('orders')
            ->where('status', 'active')
            ->where('expires_at', '<=', Carbon::now())
            ->get();

        foreach ($orders as $order) {
            $vmName = $order->vm_name ?? null;
            if (!$vmName) {
                continue;
            }

            try {
                $state = $this->client->getInstanceState($vmName);

                // 幂等：已停止的 VM 不重复操作
                if (($state['status'] ?? '') === 'Stopped') {
                    Log::info("ExpiryHandler: VM {$vmName} 已是停止状态，跳过暂停操作");
                    continue;
                }

                $this->client->stopInstance($vmName);

                DB::table('orders')
                    ->where('id', $order->id)
                    ->update(['status' => 'suspended', 'suspended_at' => Carbon::now()]);

                $deletionDate = Carbon::now()->addDays(7)->format('Y-m-d');
                $user = DB::table('users')->find($order->user_id);
                if ($user) {
                    $user = (object) $user;
                    if (method_exists($user, 'notify')) {
                        $user->notify(new SuspensionNotice(
                            $vmName,
                            $order->ip ?? '',
                            $deletionDate,
                        ));
                    }
                }

                $suspended[] = $vmName;
                Log::info("ExpiryHandler: VM {$vmName} 已暂停（到期）");
            } catch (\Throwable $e) {
                Log::error("ExpiryHandler: 暂停 VM {$vmName} 失败: {$e->getMessage()}");
            }
        }

        return $suspended;
    }

    /**
     * 删除过期 VM（D+7）+ 回收 IP
     *
     * 幂等：仅对已暂停超过 7 天的订单执行删除，删除前检查 VM 是否仍存在。
     */
    public function deleteOverdue(): array
    {
        $deleted = [];

        $orders = DB::table('orders')
            ->where('status', 'suspended')
            ->where('suspended_at', '<=', Carbon::now()->subDays(7))
            ->get();

        foreach ($orders as $order) {
            $vmName = $order->vm_name ?? null;
            if (!$vmName) {
                continue;
            }

            try {
                // 幂等：检查 VM 是否仍存在
                $exists = $this->client->instanceExists($vmName);
                if ($exists) {
                    // 确保 VM 已停止后再删除
                    $state = $this->client->getInstanceState($vmName);
                    if (($state['status'] ?? '') !== 'Stopped') {
                        $this->client->stopInstance($vmName, force: true);
                    }
                    $this->client->deleteInstance($vmName);
                }

                // 回收 IP — 设置冷却期
                if ($order->ip ?? null) {
                    DB::table('ip_addresses')
                        ->where('ip', $order->ip)
                        ->update([
                            'status' => 'cooldown',
                            'vm_name' => null,
                            'order_id' => null,
                            'released_at' => Carbon::now(),
                            'cooldown_until' => Carbon::now()->addHours(24),
                        ]);
                }

                DB::table('orders')
                    ->where('id', $order->id)
                    ->update(['status' => 'terminated', 'terminated_at' => Carbon::now()]);

                $user = DB::table('users')->find($order->user_id);
                if ($user) {
                    $user = (object) $user;
                    if (method_exists($user, 'notify')) {
                        $user->notify(new DeletionNotice($vmName, $order->ip ?? ''));
                    }
                }

                $deleted[] = $vmName;
                Log::info("ExpiryHandler: VM {$vmName} 已删除，IP {$order->ip} 已回收");
            } catch (\Throwable $e) {
                Log::error("ExpiryHandler: 删除 VM {$vmName} 失败: {$e->getMessage()}");
            }
        }

        return $deleted;
    }
}
