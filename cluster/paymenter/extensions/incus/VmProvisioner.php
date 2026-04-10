<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Notification;

/**
 * VM 创建编排器
 *
 * 封装 7 步 VM 创建流程，记录每步完成状态，
 * 失败时按已完成的步骤逆序精确回滚。
 */
class VmProvisioner
{
    private IncusClient $client;
    private IpPoolManager $ipPool;
    private array $config;

    /** 回滚追踪：记录每步已完成的资源 */
    private ?array $allocatedIp = null;
    private ?string $createdVm = null;
    private ?string $createdAcl = null;

    public function __construct(IncusClient $client, IpPoolManager $ipPool, array $config)
    {
        $this->client = $client;
        $this->ipPool = $ipPool;
        $this->config = $config;
    }

    /**
     * 执行完整的 VM 创建流程
     *
     * @param object $order 订单对象
     * @param array $params 服务器参数（cpu, memory, disk, os_image, network, ip_pool_id, bandwidth_*）
     * @param mixed $options 可选配置（ssh_key 等）
     * @return array 创建结果（vm_name, ip, password）
     * @throws ProvisionException 创建失败时抛出，内含原始错误
     */
    public function provision(object $order, array $params, $options = []): array
    {
        $vmName = 'vm-' . $order->id;
        $password = $params['password'] ?? bin2hex(random_bytes(12));

        try {
            // 步骤 1：从 IP 池分配 IP（事务锁）
            $poolId = $params['ip_pool_id'] ?? 1;
            $this->allocatedIp = $this->ipPool->allocate($poolId, $vmName, $order->id);

            Log::info('VM 创建步骤 1/7：IP 已分配', [
                'vm_name' => $vmName,
                'ip' => $this->allocatedIp['ip'],
            ]);

            // 步骤 2：生成 cloud-init 配置（静态 IP + 密码 + SSH Key）
            $cloudInit = $this->buildCloudInit($vmName, $password, $this->allocatedIp, $options);

            Log::info('VM 创建步骤 2/7：cloud-init 已生成', ['vm_name' => $vmName]);

            // 步骤 3：调用 Incus API 创建 VM
            $instanceConfig = $this->buildInstanceConfig(
                $vmName, $params, $this->allocatedIp, $cloudInit
            );
            $this->client->post('/1.0/instances', $instanceConfig);
            $this->createdVm = $vmName;

            Log::info('VM 创建步骤 3/7：实例已创建', ['vm_name' => $vmName]);

            // 步骤 4：安全过滤已在步骤 3 的 device 配置中一并设置
            // （ipv4_filtering + mac_filtering + port_isolation 作为 eth0 设备属性）
            Log::info('VM 创建步骤 4/7：安全过滤已绑定', ['vm_name' => $vmName]);

            // 步骤 5：带宽限速已在步骤 3 的 device 配置中一并设置
            // （limits.ingress + limits.egress 作为 eth0 设备属性）
            Log::info('VM 创建步骤 5/7：带宽限速已设置', ['vm_name' => $vmName]);

            // 步骤 6：创建默认 ACL（ingress drop + 放行 SSH 22）并绑定到 VM
            $aclName = 'acl-order-' . $order->id;
            $this->createAndBindAcl($vmName, $aclName);
            $this->createdAcl = $aclName;

            Log::info('VM 创建步骤 6/7：ACL 已创建并绑定', [
                'vm_name' => $vmName,
                'acl' => $aclName,
            ]);

            // 步骤 7：启动 VM
            $this->client->put('/1.0/instances/' . $vmName . '/state', [
                'action' => 'start',
            ]);

            Log::info('VM 创建步骤 7/7：VM 已启动', [
                'vm_name' => $vmName,
                'ip' => $this->allocatedIp['ip'],
                'order_id' => $order->id,
            ]);

            return [
                'vm_name' => $vmName,
                'ip' => $this->allocatedIp['ip'],
                'password' => $password,
            ];

        } catch (\Exception $e) {
            Log::error('VM 创建失败，开始回滚', [
                'vm_name' => $vmName,
                'order_id' => $order->id,
                'error' => $e->getMessage(),
                'allocated_ip' => $this->allocatedIp['ip'] ?? null,
                'created_vm' => $this->createdVm,
                'created_acl' => $this->createdAcl,
            ]);

            $this->rollback();

            throw new ProvisionException(
                "VM [{$vmName}] 创建失败: " . $e->getMessage(),
                $order->id,
                $e
            );
        }
    }

    /**
     * 逆序回滚已创建的资源
     */
    private function rollback(): void
    {
        // 回滚 ACL
        if ($this->createdAcl) {
            try {
                $this->client->delete('/1.0/network-acls/' . $this->createdAcl);
                Log::info('回滚：ACL 已删除', ['acl' => $this->createdAcl]);
            } catch (\Exception $e) {
                Log::error('回滚：ACL 删除失败', [
                    'acl' => $this->createdAcl,
                    'error' => $e->getMessage(),
                ]);
            }
        }

        // 回滚 VM
        if ($this->createdVm) {
            try {
                // 先尝试强制停止
                try {
                    $this->client->put('/1.0/instances/' . $this->createdVm . '/state', [
                        'action' => 'stop',
                        'force' => true,
                    ]);
                } catch (\Exception $e) {
                    // VM 可能未启动，忽略
                }

                $this->client->delete('/1.0/instances/' . $this->createdVm);
                Log::info('回滚：VM 已删除', ['vm_name' => $this->createdVm]);
            } catch (\Exception $e) {
                Log::error('回滚：VM 删除失败', [
                    'vm_name' => $this->createdVm,
                    'error' => $e->getMessage(),
                ]);
            }
        }

        // 回滚 IP（直接恢复为 available，不进入冷却期）
        if ($this->allocatedIp) {
            try {
                DB::table('ip_addresses')
                    ->where('ip', $this->allocatedIp['ip'])
                    ->where('status', 'allocated')
                    ->update([
                        'status' => 'available',
                        'vm_name' => null,
                        'order_id' => null,
                        'allocated_at' => null,
                    ]);
                Log::info('回滚：IP 已恢复为可用', ['ip' => $this->allocatedIp['ip']]);
            } catch (\Exception $e) {
                Log::error('回滚：IP 恢复失败', [
                    'ip' => $this->allocatedIp['ip'],
                    'error' => $e->getMessage(),
                ]);
            }
        }
    }

    /**
     * 构建 Incus 实例配置
     */
    private function buildInstanceConfig(
        string $vmName,
        array $params,
        array $ipInfo,
        string $cloudInit
    ): array {
        return [
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
                    // 安全过滤（步骤 4）
                    'security.ipv4_filtering' => 'true',
                    'security.mac_filtering' => 'true',
                    'security.port_isolation' => 'true',
                    // 带宽限速（步骤 5）
                    'limits.ingress' => $params['bandwidth_ingress']
                        ?? $this->config['default_bandwidth']['ingress'],
                    'limits.egress' => $params['bandwidth_egress']
                        ?? $this->config['default_bandwidth']['egress'],
                ],
            ],
        ];
    }

    /**
     * 创建默认 ACL 并绑定到 VM
     */
    private function createAndBindAcl(string $vmName, string $aclName): void
    {
        // 创建 ACL
        $this->client->post('/1.0/network-acls', [
            'name' => $aclName,
            'ingress' => $this->config['default_acl']['ingress'],
            'egress' => $this->config['default_acl']['egress'],
        ]);

        // 绑定 ACL 到 VM 的 eth0 设备
        $this->client->patch('/1.0/instances/' . $vmName, [
            'devices' => [
                'eth0' => [
                    'security.acls' => $aclName,
                    'security.acls.default.ingress.action' =>
                        $this->config['default_acl']['default_ingress_action'],
                    'security.acls.default.egress.action' =>
                        $this->config['default_acl']['default_egress_action'],
                ],
            ],
        ]);
    }

    /**
     * 构建 cloud-init user-data 配置
     *
     * 包含：主机名、静态 IP（Netplan）、root 密码、SSH Key
     */
    private function buildCloudInit(
        string $vmName,
        string $password,
        array $ipInfo,
        $options
    ): string {
        $sshKey = $options['ssh_key'] ?? $options->ssh_key ?? null;

        $config = [
            'hostname' => $vmName,
            'manage_etc_hosts' => true,
            'chpasswd' => [
                'expire' => false,
            ],
            'users' => [
                [
                    'name' => 'root',
                    'lock_passwd' => false,
                    'hashed_passwd' => '',
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

/**
 * VM 创建失败异常
 *
 * 携带 orderId 便于上层处理退款和通知。
 */
class ProvisionException extends \RuntimeException
{
    private int $orderId;

    public function __construct(string $message, int $orderId, ?\Throwable $previous = null)
    {
        parent::__construct($message, 0, $previous);
        $this->orderId = $orderId;
    }

    public function getOrderId(): int
    {
        return $this->orderId;
    }
}
