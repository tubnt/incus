<?php

namespace App\Extensions\Incus;

use App\Classes\ServerExtension;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Notification;

class IncusExtension extends ServerExtension
{
    private IncusClient $client;
    private IpPoolManager $ipPool;
    private array $config;

    public function __construct()
    {
        $this->config = config('incus');
        $this->client = new IncusClient($this->config);
        $this->ipPool = new IpPoolManager();
    }

    // ========== 生命周期 ==========

    /**
     * 创建 VM — 完整 7 步流程 + 失败回滚
     *
     * 流程：
     *   1. 从 IP 池分配 IP（事务锁）
     *   2. 生成 cloud-init 配置（静态 IP + 密码 + SSH Key）
     *   3. 调用 Incus API 创建 VM（Ceph 存储池 + CPU/内存/磁盘）
     *   4. 绑定安全过滤（ipv4_filtering + mac_filtering + port_isolation）
     *   5. 设置带宽限速（limits.ingress/egress）
     *   6. 创建默认 ACL（ingress drop + 放行 SSH 22）
     *   7. 启动 VM
     *
     * 任意步骤失败时回滚：
     *   - 回收 IP（status → available）
     *   - 删除已创建的 VM / ACL
     *   - 订单标记为 "创建失败"
     *   - 自动退款到账户余额
     *   - 发送邮件通知用户
     *   - P1 告警通知运维
     *   - 记录审计日志
     */
    public function createServer($user, $params, $order, $product, $options): bool
    {
        $provisioner = new VmProvisioner($this->client, $this->ipPool, $this->config);

        try {
            $result = $provisioner->provision($order, $params, $options);

            // 审计日志：创建成功
            $this->auditLog('vm_created', $order->id, [
                'vm_name' => $result['vm_name'],
                'ip' => $result['ip'],
                'cpu' => $params['cpu'] ?? 1,
                'memory' => ($params['memory'] ?? 1024) . 'MiB',
                'disk' => ($params['disk'] ?? 25) . 'GiB',
                'os_image' => $params['os_image'] ?? 'ubuntu/24.04',
            ]);

            return true;

        } catch (ProvisionException $e) {
            // VmProvisioner 已完成资源回滚（IP/VM/ACL），
            // 这里处理业务层回滚：订单状态 + 退款 + 通知 + 告警
            $this->handleProvisionFailure($user, $order, $product, $e);

            return false;
        }
    }

    /**
     * 处理 VM 创建失败的业务层回滚
     *
     * 步骤 4-8（资源回滚由 VmProvisioner 内部完成）：
     *   4. 订单标记为 "创建失败"
     *   5. 自动退款到账户余额
     *   6. 发送邮件通知用户
     *   7. P1 告警通知运维
     *   8. 记录审计日志（含 Incus API 错误信息）
     */
    private function handleProvisionFailure(
        $user,
        $order,
        $product,
        ProvisionException $e
    ): void {
        $orderId = $order->id;
        $errorMessage = $e->getMessage();
        $originalError = $e->getPrevious()?->getMessage() ?? $errorMessage;

        // 4. 订单标记为 "创建失败"
        try {
            DB::table('orders')
                ->where('id', $orderId)
                ->update([
                    'status' => 'failed',
                    'notes' => "创建失败: {$originalError}",
                    'updated_at' => now(),
                ]);
            Log::info('创建失败处理：订单已标记为失败', ['order_id' => $orderId]);
        } catch (\Exception $ex) {
            Log::error('创建失败处理：订单状态更新失败', [
                'order_id' => $orderId,
                'error' => $ex->getMessage(),
            ]);
        }

        // 5. 自动退款到账户余额（非原路退款，避免支付手续费）
        try {
            $amount = $order->total ?? $product->price ?? 0;
            if ($amount > 0) {
                DB::table('users')
                    ->where('id', $user->id ?? $order->user_id)
                    ->increment('balance', $amount);

                DB::table('credit_logs')->insert([
                    'user_id' => $user->id ?? $order->user_id,
                    'order_id' => $orderId,
                    'amount' => $amount,
                    'type' => 'refund',
                    'description' => "VM 创建失败自动退款 (订单 #{$orderId})",
                    'created_at' => now(),
                ]);

                Log::info('创建失败处理：已退款到余额', [
                    'order_id' => $orderId,
                    'user_id' => $user->id ?? $order->user_id,
                    'amount' => $amount,
                ]);
            }
        } catch (\Exception $ex) {
            Log::error('创建失败处理：退款失败', [
                'order_id' => $orderId,
                'error' => $ex->getMessage(),
            ]);
        }

        // 6. 发送邮件通知用户
        try {
            $userEmail = $user->email ?? null;
            if ($userEmail) {
                Mail::raw(
                    "您的云主机订单 #{$orderId} 创建失败，已自动退款到账户余额。\n"
                    . "如有疑问，请提交工单联系我们。\n\n"
                    . "— 系统自动通知",
                    function ($message) use ($userEmail, $orderId) {
                        $message->to($userEmail)
                                ->subject("云主机订单 #{$orderId} 创建失败 — 已自动退款");
                    }
                );
                Log::info('创建失败处理：用户通知邮件已发送', [
                    'order_id' => $orderId,
                    'email' => $userEmail,
                ]);
            }
        } catch (\Exception $ex) {
            Log::error('创建失败处理：邮件发送失败', [
                'order_id' => $orderId,
                'error' => $ex->getMessage(),
            ]);
        }

        // 7. P1 告警通知运维
        try {
            $adminEmail = config('incus.alert_email', config('mail.admin_address'));
            if ($adminEmail) {
                Mail::raw(
                    "[P1 告警] VM 创建失败\n\n"
                    . "订单 ID: {$orderId}\n"
                    . "VM 名称: vm-{$orderId}\n"
                    . "用户: " . ($user->email ?? $user->id ?? 'unknown') . "\n"
                    . "错误: {$originalError}\n"
                    . "时间: " . now()->toDateTimeString() . "\n\n"
                    . "资源回滚状态：已自动回滚（IP/VM/ACL）\n"
                    . "退款状态：已退款到账户余额\n\n"
                    . "请检查 Incus 集群状态。",
                    function ($message) use ($adminEmail, $orderId) {
                        $message->to($adminEmail)
                                ->subject("[P1] VM 创建失败 — 订单 #{$orderId}");
                    }
                );
            }
            Log::warning('P1 告警：VM 创建失败', [
                'order_id' => $orderId,
                'error' => $originalError,
            ]);
        } catch (\Exception $ex) {
            Log::error('创建失败处理：P1 告警发送失败', [
                'order_id' => $orderId,
                'error' => $ex->getMessage(),
            ]);
        }

        // 8. 记录审计日志（含 Incus API 错误信息）
        $this->auditLog('vm_creation_failed', $orderId, [
            'vm_name' => 'vm-' . $orderId,
            'error' => $originalError,
            'full_error' => $errorMessage,
            'user_id' => $user->id ?? $order->user_id ?? null,
            'rollback' => 'completed',
        ]);
    }

    /**
     * 暂停 VM（保留状态停机）
     */
    public function suspendServer($user, $params, $order, $product, $options): bool
    {
        $vmName = 'vm-' . $order->id;

        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'stop',
            'stateful' => true,
        ]);

        Log::info('VM 已暂停', ['vm_name' => $vmName]);
        return true;
    }

    /**
     * 恢复 VM
     */
    public function unsuspendServer($user, $params, $order, $product, $options): bool
    {
        $vmName = 'vm-' . $order->id;

        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'start',
        ]);

        Log::info('VM 已恢复', ['vm_name' => $vmName]);
        return true;
    }

    /**
     * 删除 VM
     *
     * 流程：强制删除实例 → 回收 IP（进入冷却期）→ 删除 ACL → 删除附加磁盘卷
     */
    public function terminateServer($user, $params, $order, $product, $options): bool
    {
        $vmName = 'vm-' . $order->id;
        $aclName = 'acl-order-' . $order->id;

        // 1. 强制停止并删除 VM
        try {
            $this->client->put('/1.0/instances/' . $vmName . '/state', [
                'action' => 'stop',
                'force' => true,
            ]);
        } catch (\Exception $e) {
            // VM 可能已停止，忽略
        }

        $this->client->delete('/1.0/instances/' . $vmName);

        // 2. 回收 IP 到冷却池
        $ip = $params['ip'] ?? null;
        if ($ip) {
            $cooldownHours = $this->config['ip_cooldown_hours'] ?? 24;
            $this->ipPool->release($ip, $cooldownHours);
        }

        // 3. 删除 ACL
        try {
            $this->client->delete('/1.0/network-acls/' . $aclName);
        } catch (\Exception $e) {
            Log::warning('ACL 删除失败', ['acl' => $aclName, 'error' => $e->getMessage()]);
        }

        // 4. 删除附加磁盘卷
        $volumeName = 'vol-' . $order->id;
        try {
            $pool = $this->config['storage_pool'];
            $this->client->delete("/1.0/storage-pools/{$pool}/volumes/custom/{$volumeName}");
        } catch (\Exception $e) {
            // 可能无附加磁盘，忽略
        }

        Log::info('VM 已删除', ['vm_name' => $vmName, 'order_id' => $order->id]);
        return true;
    }

    // ========== 用户操作 ==========

    public function reboot($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];

        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'restart',
        ]);

        return true;
    }

    public function reinstall($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];

        $instance = $this->client->get('/1.0/instances/' . $vmName);
        $currentConfig = $instance['metadata'] ?? [];

        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'stop',
            'force' => true,
        ]);
        $this->client->delete('/1.0/instances/' . $vmName);

        $newImage = $params['os_image'] ?? $currentConfig['config']['image.os'] ?? 'ubuntu/24.04';
        $currentConfig['source'] = [
            'type' => 'image',
            'alias' => $newImage,
        ];

        $this->client->post('/1.0/instances', $currentConfig);

        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'start',
        ]);

        Log::info('VM 重装完成', ['vm_name' => $vmName, 'os' => $newImage]);
        return true;
    }

    public function changePassword($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $password = $params['password'];

        $this->client->post('/1.0/instances/' . $vmName . '/exec', [
            'command' => ['chpasswd'],
            'environment' => [],
            'wait-for-websocket' => false,
            'record-output' => false,
            'stdin-data' => "root:{$password}\n",
        ]);

        return true;
    }

    public function getConsoleUrl($params): string
    {
        $vmName = 'vm-' . $params['order_id'];
        $token = bin2hex(random_bytes(32));

        // TODO: 将 token 存入 cache，关联用户和 VM，TTL 5 分钟

        return "/console/{$vmName}?token={$token}";
    }

    // ========== 快照 ==========

    public function createSnapshot($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $snapshotName = $params['name'] ?? 'snap-' . time();

        $this->client->post('/1.0/instances/' . $vmName . '/snapshots', [
            'name' => $snapshotName,
            'stateful' => false,
        ]);

        Log::info('快照已创建', ['vm_name' => $vmName, 'snapshot' => $snapshotName]);
        return true;
    }

    public function restoreSnapshot($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $snapshotName = $params['name'];

        $this->client->put('/1.0/instances/' . $vmName, [
            'restore' => $snapshotName,
        ]);

        Log::info('快照已恢复', ['vm_name' => $vmName, 'snapshot' => $snapshotName]);
        return true;
    }

    public function deleteSnapshot($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $snapshotName = $params['name'];

        $this->client->delete('/1.0/instances/' . $vmName . '/snapshots/' . $snapshotName);

        Log::info('快照已删除', ['vm_name' => $vmName, 'snapshot' => $snapshotName]);
        return true;
    }

    // ========== 升降配 ==========

    public function upgrade($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];

        $config = [];
        if (isset($params['cpu'])) {
            $config['limits.cpu'] = (string) $params['cpu'];
        }
        if (isset($params['memory'])) {
            $config['limits.memory'] = $params['memory'] . 'MiB';
        }

        $this->client->patch('/1.0/instances/' . $vmName, [
            'config' => $config,
        ]);

        Log::info('VM 升配完成', ['vm_name' => $vmName, 'config' => $config]);
        return true;
    }

    public function downgrade($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];

        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'stop',
        ]);

        $config = [];
        if (isset($params['cpu'])) {
            $config['limits.cpu'] = (string) $params['cpu'];
        }
        if (isset($params['memory'])) {
            $config['limits.memory'] = $params['memory'] . 'MiB';
        }

        $this->client->patch('/1.0/instances/' . $vmName, [
            'config' => $config,
        ]);

        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'start',
        ]);

        Log::info('VM 降配完成', ['vm_name' => $vmName, 'config' => $config]);
        return true;
    }

    // ========== 防火墙 ==========

    public function getFirewallRules($params): array
    {
        $aclName = 'acl-order-' . $params['order_id'];
        $response = $this->client->get('/1.0/network-acls/' . $aclName);

        return $response['metadata'] ?? [];
    }

    public function addFirewallRule($params): bool
    {
        $aclName = 'acl-order-' . $params['order_id'];
        $direction = $params['direction'] ?? 'ingress';

        $acl = $this->client->get('/1.0/network-acls/' . $aclName);
        $rules = $acl['metadata'][$direction] ?? [];

        $rules[] = [
            'action' => $params['action'] ?? 'allow',
            'protocol' => $params['protocol'],
            'destination_port' => $params['port'],
            'source' => $params['source'] ?? '0.0.0.0/0',
            'description' => $params['description'] ?? '',
        ];

        $this->client->patch('/1.0/network-acls/' . $aclName, [
            $direction => $rules,
        ]);

        return true;
    }

    public function removeFirewallRule($params): bool
    {
        $aclName = 'acl-order-' . $params['order_id'];
        $direction = $params['direction'] ?? 'ingress';
        $ruleIndex = $params['rule_index'];

        $acl = $this->client->get('/1.0/network-acls/' . $aclName);
        $rules = $acl['metadata'][$direction] ?? [];

        if (!isset($rules[$ruleIndex])) {
            throw new \RuntimeException("防火墙规则不存在: index {$ruleIndex}");
        }

        array_splice($rules, $ruleIndex, 1);

        $this->client->put('/1.0/network-acls/' . $aclName, array_merge(
            $acl['metadata'],
            [$direction => $rules]
        ));

        return true;
    }

    // ========== 附加磁盘 ==========

    public function addDisk($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $volumeName = 'vol-' . $params['order_id'];
        $size = ($params['disk_size'] ?? 50) . 'GiB';
        $pool = $this->config['storage_pool'];

        $this->client->post("/1.0/storage-pools/{$pool}/volumes/custom", [
            'name' => $volumeName,
            'config' => [
                'size' => $size,
            ],
        ]);

        $this->client->patch('/1.0/instances/' . $vmName, [
            'devices' => [
                'data-disk' => [
                    'type' => 'disk',
                    'pool' => $pool,
                    'source' => $volumeName,
                ],
            ],
        ]);

        Log::info('附加磁盘已添加', [
            'vm_name' => $vmName,
            'volume' => $volumeName,
            'size' => $size,
        ]);

        return true;
    }

    public function removeDisk($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $volumeName = 'vol-' . $params['order_id'];
        $pool = $this->config['storage_pool'];

        $instance = $this->client->get('/1.0/instances/' . $vmName);
        $devices = $instance['metadata']['devices'] ?? [];
        unset($devices['data-disk']);

        $this->client->put('/1.0/instances/' . $vmName, [
            'devices' => $devices,
        ]);

        $this->client->delete("/1.0/storage-pools/{$pool}/volumes/custom/{$volumeName}");

        Log::info('附加磁盘已移除', ['vm_name' => $vmName, 'volume' => $volumeName]);
        return true;
    }

    // ========== SSH Key ==========

    public function addSshKey($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $sshKey = $params['ssh_key'];

        $this->client->post('/1.0/instances/' . $vmName . '/exec', [
            'command' => ['bash', '-c', 'mkdir -p /root/.ssh && cat >> /root/.ssh/authorized_keys'],
            'environment' => [],
            'wait-for-websocket' => false,
            'record-output' => false,
            'stdin-data' => $sshKey . "\n",
        ]);

        return true;
    }

    // ========== 监控 ==========

    public function getMetrics($params): array
    {
        $vmName = 'vm-' . $params['order_id'];

        $response = $this->client->get('/1.0/instances/' . $vmName . '/state');
        $state = $response['metadata'] ?? [];

        return [
            'status' => $state['status'] ?? 'Unknown',
            'cpu' => [
                'usage_ns' => $state['cpu']['usage'] ?? 0,
            ],
            'memory' => [
                'usage_bytes' => $state['memory']['usage'] ?? 0,
                'total_bytes' => $state['memory']['total'] ?? 0,
            ],
            'disk' => $state['disk'] ?? [],
            'network' => $state['network'] ?? [],
        ];
    }

    // ========== 内部方法 ==========

    /**
     * 写入审计日志
     */
    private function auditLog(string $event, int $orderId, array $context): void
    {
        try {
            DB::table('audit_logs')->insert([
                'event' => $event,
                'order_id' => $orderId,
                'context' => json_encode($context, JSON_UNESCAPED_UNICODE),
                'created_at' => now(),
            ]);
        } catch (\Exception $e) {
            // 审计日志写入失败不应中断主流程，降级到 Laravel Log
            Log::error('审计日志写入失败', [
                'event' => $event,
                'order_id' => $orderId,
                'context' => $context,
                'error' => $e->getMessage(),
            ]);
        }
    }
}
