<?php

namespace App\Extensions\Incus;

use App\Classes\ServerExtension;
use Illuminate\Support\Facades\Log;

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
     * 创建 VM
     *
     * 流程：分配 IP → 创建实例（cloud-init 注入网络/密码/SSH Key）
     *       → 绑定安全过滤 → 设置带宽限速 → 创建默认 ACL
     */
    public function createServer($user, $params, $order, $product, $options): bool
    {
        $vmName = 'vm-' . $order->id;
        $poolId = $params['ip_pool_id'] ?? 1;
        $ipInfo = null;

        try {
            // 1. 分配 IP
            $ipInfo = $this->ipPool->allocate($poolId, $vmName, $order->id);

            // 2. 构建 cloud-init 配置
            $cloudInit = $this->buildCloudInit($params, $ipInfo, $options);

            // 3. 创建 VM 实例
            $instanceConfig = [
                'name' => $vmName,
                'type' => 'virtual-machine',
                'source' => [
                    'type' => 'image',
                    'alias' => $params['os_image'] ?? 'ubuntu/24.04',
                ],
                'config' => [
                    'limits.cpu' => (string) ($params['cpu'] ?? 1),
                    'limits.memory' => ($params['memory'] ?? 1024) . 'MiB',
                    'cloud-init.user-data' => $cloudInit,
                ],
                'devices' => [
                    'root' => [
                        'type' => 'disk',
                        'pool' => $this->config['storage_pool'],
                        'path' => '/',
                        'size' => ($params['disk'] ?? 25) . 'GiB',
                    ],
                    'eth0' => [
                        'type' => 'nic',
                        'network' => $params['network'] ?? 'br-public',
                        'ipv4.address' => $ipInfo['ip'],
                        // 安全过滤
                        'security.ipv4_filtering' => 'true',
                        'security.mac_filtering' => 'true',
                        'security.port_isolation' => 'true',
                        // 带宽限速
                        'limits.ingress' => $params['bandwidth_ingress']
                            ?? $this->config['default_bandwidth']['ingress'],
                        'limits.egress' => $params['bandwidth_egress']
                            ?? $this->config['default_bandwidth']['egress'],
                    ],
                ],
            ];

            $this->client->post('/1.0/instances', $instanceConfig);

            // 4. 创建默认 ACL
            $aclName = 'acl-order-' . $order->id;
            $this->client->post('/1.0/network-acls', [
                'name' => $aclName,
                'ingress' => $this->config['default_acl']['ingress'],
                'egress' => $this->config['default_acl']['egress'],
            ]);

            // 5. 绑定 ACL 到 VM
            $this->client->patch('/1.0/instances/' . $vmName, [
                'devices' => [
                    'eth0' => [
                        'security.acls' => $aclName,
                        'security.acls.default.ingress.action' => $this->config['default_acl']['default_ingress_action'],
                        'security.acls.default.egress.action' => $this->config['default_acl']['default_egress_action'],
                    ],
                ],
            ]);

            // 6. 启动 VM
            $this->client->put('/1.0/instances/' . $vmName . '/state', [
                'action' => 'start',
            ]);

            Log::info('VM 创建成功', [
                'vm_name' => $vmName,
                'ip' => $ipInfo['ip'],
                'order_id' => $order->id,
            ]);

            return true;

        } catch (\Exception $e) {
            // 创建失败：回滚已分配的 IP
            if ($ipInfo) {
                $this->ipPool->release($ipInfo['ip'], 0);
            }

            Log::error('VM 创建失败', [
                'vm_name' => $vmName,
                'order_id' => $order->id,
                'error' => $e->getMessage(),
            ]);

            throw $e;
        }
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

    /**
     * 重启 VM
     */
    public function reboot($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];

        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'restart',
        ]);

        return true;
    }

    /**
     * 重装系统
     *
     * 保留 IP，删除旧实例后以相同 IP 重新创建
     */
    public function reinstall($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];

        // 获取当前实例配置
        $instance = $this->client->get('/1.0/instances/' . $vmName);
        $currentConfig = $instance['metadata'] ?? [];

        // 停止并删除旧实例（不回收 IP）
        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'stop',
            'force' => true,
        ]);
        $this->client->delete('/1.0/instances/' . $vmName);

        // 使用新的 OS 镜像重新创建（保留原有网络配置）
        $newImage = $params['os_image'] ?? $currentConfig['config']['image.os'] ?? 'ubuntu/24.04';
        $currentConfig['source'] = [
            'type' => 'image',
            'alias' => $newImage,
        ];

        $this->client->post('/1.0/instances', $currentConfig);

        // 启动新实例
        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'start',
        ]);

        Log::info('VM 重装完成', ['vm_name' => $vmName, 'os' => $newImage]);
        return true;
    }

    /**
     * 修改密码（通过 stdin 管道传递，不暴露命令行）
     */
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

    /**
     * 获取 Console 代理 URL
     */
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

    /**
     * 升配（热操作，不停机）
     */
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

    /**
     * 降配（需停机）
     */
    public function downgrade($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];

        // 停机
        $this->client->put('/1.0/instances/' . $vmName . '/state', [
            'action' => 'stop',
        ]);

        // 修改配置
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

        // 重新启动
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

        // 获取当前 ACL
        $acl = $this->client->get('/1.0/network-acls/' . $aclName);
        $rules = $acl['metadata'][$direction] ?? [];

        // 添加新规则
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

        // 获取当前 ACL
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

    /**
     * 添加附加磁盘
     */
    public function addDisk($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $volumeName = 'vol-' . $params['order_id'];
        $size = ($params['disk_size'] ?? 50) . 'GiB';
        $pool = $this->config['storage_pool'];

        // 1. 创建存储卷
        $this->client->post("/1.0/storage-pools/{$pool}/volumes/custom", [
            'name' => $volumeName,
            'config' => [
                'size' => $size,
            ],
        ]);

        // 2. 挂载到 VM
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

    /**
     * 移除附加磁盘
     */
    public function removeDisk($params): bool
    {
        $vmName = 'vm-' . $params['order_id'];
        $volumeName = 'vol-' . $params['order_id'];
        $pool = $this->config['storage_pool'];

        // 1. 从 VM 移除设备
        $instance = $this->client->get('/1.0/instances/' . $vmName);
        $devices = $instance['metadata']['devices'] ?? [];
        unset($devices['data-disk']);

        $this->client->put('/1.0/instances/' . $vmName, [
            'devices' => $devices,
        ]);

        // 2. 删除存储卷
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

    /**
     * 获取 VM 资源指标（CPU/内存/磁盘/网络）
     */
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
     * 构建 cloud-init 配置
     */
    private function buildCloudInit(array $params, array $ipInfo, $options): string
    {
        $password = $params['password'] ?? bin2hex(random_bytes(12));
        $sshKey = $options['ssh_key'] ?? $params['ssh_key'] ?? null;

        $config = [
            'hostname' => 'vm-' . ($params['order_id'] ?? 'unknown'),
            'manage_etc_hosts' => true,
            'chpasswd' => [
                'expire' => false,
            ],
            'users' => [
                [
                    'name' => 'root',
                    'lock_passwd' => false,
                    'hashed_passwd' => '',  // 由 runcmd 设置
                ],
            ],
            'runcmd' => [
                "echo 'root:{$password}' | chpasswd",
            ],
            'write_files' => [
                [
                    'path' => '/etc/netplan/90-static.yaml',
                    'content' => $this->buildNetplanConfig($ipInfo),
                ],
            ],
        ];

        if ($sshKey) {
            $config['users'][0]['ssh_authorized_keys'] = [$sshKey];
        }

        return "#cloud-config\n" . yaml_emit($config, YAML_UTF8_ENCODING);
    }

    /**
     * 构建 Netplan 静态 IP 配置
     */
    private function buildNetplanConfig(array $ipInfo): string
    {
        // 从子网 CIDR 获取前缀长度
        $prefix = explode('/', $ipInfo['subnet'])[1] ?? '27';

        return "network:\n"
            . "  version: 2\n"
            . "  ethernets:\n"
            . "    enp5s0:\n"
            . "      addresses:\n"
            . "        - {$ipInfo['ip']}/{$prefix}\n"
            . "      routes:\n"
            . "        - to: default\n"
            . "          via: {$ipInfo['gateway']}\n"
            . "      nameservers:\n"
            . "        addresses: [1.1.1.1, 8.8.8.8]\n";
    }
}
