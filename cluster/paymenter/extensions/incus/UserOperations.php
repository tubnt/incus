<?php

namespace Extensions\Incus;

/**
 * VM 用户自助操作
 *
 * 封装用户可执行的 VM 操作（reboot/reinstall/密码/SSH Key），
 * 所有操作均通过并发锁保护并记录审计日志。
 */
class UserOperations
{
    private IncusClient $client;

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    /**
     * 重启 VM
     *
     * PUT /1.0/instances/{name}/state action=restart
     */
    public function reboot(string $vmName, int $orderId): array
    {
        return VmOperationLock::withLock($vmName, 'reboot', function () use ($vmName, $orderId) {
            try {
                $result = $this->client->put("/1.0/instances/{$vmName}/state", [
                    'action'  => 'restart',
                    'timeout' => 60,
                    'force'   => false,
                ]);

                AuditLogger::success('reboot', $vmName, $orderId);

                return $result;
            } catch (\Throwable $e) {
                AuditLogger::failure('reboot', $vmName, $orderId, ['error' => $e->getMessage()]);
                throw $e;
            }
        });
    }

    /**
     * 重装系统
     *
     * 删除 VM → 使用相同 IP 和配置重建
     *
     * @param string $vmName    VM 名称
     * @param int $orderId      订单 ID
     * @param string $image     新镜像（如 images:ubuntu/24.04）
     * @param string $password  新 root 密码
     * @param array $config     原始 VM 配置（IP、带宽、ACL 等）
     */
    public function reinstall(string $vmName, int $orderId, string $image, string $password, array $config): array
    {
        return VmOperationLock::withLock($vmName, 'reinstall', function () use ($vmName, $orderId, $image, $password, $config) {
            try {
                // 1. 强制停止并删除当前 VM
                $this->client->put("/1.0/instances/{$vmName}/state", [
                    'action'  => 'stop',
                    'timeout' => 30,
                    'force'   => true,
                ]);
                $this->client->delete("/1.0/instances/{$vmName}");

                // 2. 使用相同名称和 IP 重建
                $cloudInit = $this->buildReinstallCloudInit($password, $config);

                $result = $this->client->post('/1.0/instances', [
                    'name'   => $vmName,
                    'type'   => 'virtual-machine',
                    'source' => [
                        'type'     => 'image',
                        'alias'    => $image,
                    ],
                    'config' => [
                        'limits.cpu'           => (string)($config['cpu'] ?? 1),
                        'limits.memory'        => ($config['memory'] ?? '1') . 'GiB',
                        'cloud-init.user-data' => $cloudInit,
                        'security.ipv4_filtering' => 'true',
                        'security.mac_filtering'  => 'true',
                        'security.port_isolation' => 'true',
                    ],
                    'devices' => [
                        'eth0' => [
                            'type'            => 'nic',
                            'network'         => $config['network'] ?? 'br0',
                            'ipv4.address'    => $config['ip'],
                            'limits.ingress'  => $config['bandwidth'] ?? '200Mbit',
                            'limits.egress'   => $config['bandwidth'] ?? '200Mbit',
                            'security.acls'   => $config['acl_name'] ?? "acl-{$vmName}",
                        ],
                        'root' => [
                            'type' => 'disk',
                            'pool' => $config['storage_pool'] ?? 'ceph-pool',
                            'path' => '/',
                            'size' => ($config['disk'] ?? '20') . 'GiB',
                        ],
                    ],
                ]);

                // 3. 启动 VM
                $this->client->put("/1.0/instances/{$vmName}/state", [
                    'action'  => 'start',
                    'timeout' => 60,
                ]);

                AuditLogger::success('reinstall', $vmName, $orderId, [
                    'image' => $image,
                    'ip'    => $config['ip'],
                ]);

                return $result;
            } catch (\Throwable $e) {
                AuditLogger::failure('reinstall', $vmName, $orderId, [
                    'image' => $image,
                    'error' => $e->getMessage(),
                ]);
                throw $e;
            }
        });
    }

    /**
     * 修改 root 密码
     *
     * 通过 incus exec 执行 chpasswd，密码经 stdin 传递，不记录输出
     */
    public function changePassword(string $vmName, int $orderId, string $newPassword): array
    {
        return VmOperationLock::withLock($vmName, 'change_password', function () use ($vmName, $orderId, $newPassword) {
            try {
                $result = $this->client->post("/1.0/instances/{$vmName}/exec", [
                    'command'            => ['chpasswd'],
                    'wait-for-websocket' => false,
                    'record-output'      => false,
                    'stdin-data'         => "root:{$newPassword}\n",
                ]);

                AuditLogger::success('change_password', $vmName, $orderId);

                return $result;
            } catch (\Throwable $e) {
                AuditLogger::failure('change_password', $vmName, $orderId, ['error' => $e->getMessage()]);
                throw $e;
            }
        });
    }

    /**
     * 添加 SSH 公钥
     *
     * 通过 stdin 传递公钥内容写入 authorized_keys，避免 shell 注入
     */
    public function addSshKey(string $vmName, int $orderId, string $publicKey): array
    {
        return VmOperationLock::withLock($vmName, 'add_ssh_key', function () use ($vmName, $orderId, $publicKey) {
            try {
                // 验证公钥格式：仅允许单行、合法字符的 SSH 公钥
                $trimmed = trim($publicKey);
                if (!preg_match('/^(ssh-rsa|ssh-ed25519|ecdsa-sha2-nistp\d+|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com)\s+[A-Za-z0-9+\/=]+(\s+\S+)?$/', $trimmed)) {
                    throw new \InvalidArgumentException('SSH 公钥格式无效');
                }

                // 先确保 .ssh 目录存在
                $this->client->post("/1.0/instances/{$vmName}/exec", [
                    'command'            => ['bash', '-c', 'mkdir -p /root/.ssh && chmod 700 /root/.ssh'],
                    'wait-for-websocket' => false,
                    'record-output'      => false,
                ]);

                // 通过 stdin 传递公钥，避免 shell 拼接
                $result = $this->client->post("/1.0/instances/{$vmName}/exec", [
                    'command'            => ['tee', '-a', '/root/.ssh/authorized_keys'],
                    'wait-for-websocket' => false,
                    'record-output'      => false,
                    'stdin-data'         => $trimmed . "\n",
                ]);

                // 修正权限
                $this->client->post("/1.0/instances/{$vmName}/exec", [
                    'command'            => ['chmod', '600', '/root/.ssh/authorized_keys'],
                    'wait-for-websocket' => false,
                    'record-output'      => false,
                ]);

                AuditLogger::success('add_ssh_key', $vmName, $orderId);

                return $result;
            } catch (\Throwable $e) {
                AuditLogger::failure('add_ssh_key', $vmName, $orderId, ['error' => $e->getMessage()]);
                throw $e;
            }
        });
    }

    /**
     * 构建重装系统的 cloud-init 配置
     */
    private function buildReinstallCloudInit(string $password, array $config): string
    {
        $sshKeys = $config['ssh_keys'] ?? [];
        $hostname = $config['hostname'] ?? 'vm';
        $ip = $config['ip'];
        $gateway = $config['gateway'];
        $netmask = $config['netmask'] ?? '255.255.255.224';

        // 计算 CIDR 前缀
        $cidr = $this->netmaskToCidr($netmask);

        // 预先 hash 密码，避免明文存储在 cloud-init 配置中
        $hashedPassword = password_hash($password, PASSWORD_BCRYPT);

        $cloudInit = [
            'hostname'          => $hostname,
            'manage_etc_hosts'  => true,
            'chpasswd'          => [
                'expire' => false,
                'users'  => [
                    ['name' => 'root', 'password' => $hashedPassword, 'type' => 'hash'],
                ],
            ],
            'ssh_pwauth'        => true,
            'disable_root'      => false,
        ];

        if (!empty($sshKeys)) {
            $cloudInit['ssh_authorized_keys'] = $sshKeys;
        }

        // Netplan 静态 IP 配置
        $netplanConfig = [
            'network' => [
                'version'   => 2,
                'renderer'  => 'networkd',
                'ethernets' => [
                    'enp5s0' => [
                        'addresses' => ["{$ip}/{$cidr}"],
                        'routes'    => [['to' => 'default', 'via' => $gateway]],
                        'nameservers' => ['addresses' => ['1.1.1.1', '8.8.8.8']],
                    ],
                ],
            ],
        ];

        $cloudInit['write_files'] = [
            [
                'path'    => '/etc/netplan/50-static.yaml',
                'content' => yaml_emit($netplanConfig, YAML_UTF8_ENCODING, YAML_LN_BREAK),
            ],
        ];

        $cloudInit['runcmd'] = [
            ['netplan', 'apply'],
        ];

        return "#cloud-config\n" . yaml_emit($cloudInit, YAML_UTF8_ENCODING, YAML_LN_BREAK);
    }

    /**
     * 子网掩码转 CIDR 前缀长度
     */
    private function netmaskToCidr(string $netmask): int
    {
        return (int)array_sum(array_map(function ($octet) {
            return substr_count(decbin((int)$octet), '1');
        }, explode('.', $netmask)));
    }
}
