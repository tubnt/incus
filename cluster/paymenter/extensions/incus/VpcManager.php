<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * VPC 私有网络管理器
 *
 * 基于 Incus managed network 为每个用户创建隔离的私有网络。
 * 同一 VPC 内的 VM 通过第二块网卡互相通信，不同用户完全隔离。
 *
 * 网段分配规则：10.{user_id % 255 + 1}.0.0/16
 */
class VpcManager
{
    private IncusClient $client;

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    /**
     * 创建 VPC 私有网络
     *
     * @param int $userId 用户 ID
     * @param string $name VPC 名称
     * @param string $subnet 私有网段 CIDR（如 10.1.0.0/16）
     * @return array VPC 记录
     * @throws \RuntimeException 网段冲突或创建失败
     */
    public function createVpc(int $userId, string $name, string $subnet): array
    {
        // 校验网段格式：仅允许 10.x.0.0/16
        if (!preg_match('/^10\.\d{1,3}\.0\.0\/16$/', $subnet)) {
            throw new \InvalidArgumentException('VPC 网段格式无效，仅支持 10.x.0.0/16 格式');
        }

        return DB::transaction(function () use ($userId, $name, $subnet) {
            // lockForUpdate 防止并发创建同一网段
            $existing = DB::table('vpcs')
                ->where('subnet', $subnet)
                ->lockForUpdate()
                ->first();
            if ($existing) {
                throw new \RuntimeException("网段 [{$subnet}] 已被使用");
            }

            // 生成 Incus network 名称：vpc-{userId}-{shortHash}
            $networkName = 'vpc-' . $userId . '-' . substr(md5($name . $userId), 0, 6);

            // 从网段提取网关地址（x.x.0.1）
            $parts = explode('.', explode('/', $subnet)[0]);
            $gateway = $parts[0] . '.' . $parts[1] . '.0.1/16';

            // 在 Incus 创建 managed network
            $this->client->post('/1.0/networks', [
                'name' => $networkName,
                'type' => 'bridge',
                'config' => [
                    'ipv4.address' => $gateway,
                    'ipv4.nat' => 'false',
                    'ipv6.address' => 'none',
                    'ipv4.dhcp' => 'true',
                    'ipv4.dhcp.ranges' => $parts[0] . '.' . $parts[1] . '.1.1-' . $parts[0] . '.' . $parts[1] . '.254.254',
                ],
            ]);

            // 写入数据库（subnet 有 unique 约束兜底）
            $vpcId = DB::table('vpcs')->insertGetId([
                'user_id' => $userId,
                'name' => $name,
                'subnet' => $subnet,
                'incus_network' => $networkName,
                'created_at' => now(),
                'updated_at' => now(),
            ]);

            Log::info('VPC 已创建', [
                'vpc_id' => $vpcId,
                'user_id' => $userId,
                'name' => $name,
                'subnet' => $subnet,
                'incus_network' => $networkName,
            ]);

            return [
                'id' => $vpcId,
                'user_id' => $userId,
                'name' => $name,
                'subnet' => $subnet,
                'incus_network' => $networkName,
            ];
        });
    }

    /**
     * 删除 VPC
     *
     * @param int $vpcId VPC ID
     * @throws \RuntimeException VPC 中仍有 VM 成员
     */
    public function deleteVpc(int $vpcId): void
    {
        $vpc = DB::table('vpcs')->where('id', $vpcId)->first();
        if (!$vpc) {
            throw new \RuntimeException("VPC [{$vpcId}] 不存在");
        }

        // 检查是否还有成员
        $memberCount = DB::table('vpc_members')->where('vpc_id', $vpcId)->count();
        if ($memberCount > 0) {
            throw new \RuntimeException("VPC [{$vpc->name}] 中仍有 {$memberCount} 台 VM，请先移除所有 VM");
        }

        // 删除 Incus network
        try {
            $this->client->delete('/1.0/networks/' . $vpc->incus_network);
        } catch (\Exception $e) {
            Log::warning('Incus 网络删除失败，继续清理数据库', [
                'network' => $vpc->incus_network,
                'error' => $e->getMessage(),
            ]);
        }

        DB::table('vpcs')->where('id', $vpcId)->delete();

        Log::info('VPC 已删除', [
            'vpc_id' => $vpcId,
            'name' => $vpc->name,
        ]);
    }

    /**
     * VM 加入 VPC（添加第二块网卡）
     *
     * @param int $vpcId VPC ID
     * @param string $vmName VM 名称
     * @param int $userId 操作用户 ID（用于鉴权）
     * @return array 成员记录（含分配的私有 IP）
     * @throws \RuntimeException VPC 不属于该用户或 VM 不属于该用户
     */
    public function attachVm(int $vpcId, string $vmName, int $userId): array
    {
        $vpc = DB::table('vpcs')->where('id', $vpcId)->first();
        if (!$vpc) {
            throw new \RuntimeException("VPC [{$vpcId}] 不存在");
        }

        // 校验 VPC 归属
        if ((int) $vpc->user_id !== $userId) {
            throw new \RuntimeException("无权操作此 VPC");
        }

        // 校验 VM 归属：通过 ip_addresses 关联的 order 确认用户
        $vmIp = DB::table('ip_addresses')->where('vm_name', $vmName)->first();
        if ($vmIp && $vmIp->order_id) {
            $order = DB::table('orders')->where('id', $vmIp->order_id)->first();
            if (!$order || (int) $order->user_id !== $userId) {
                throw new \RuntimeException("VM [{$vmName}] 不属于当前用户");
            }
        }

        // 检查 VM 是否已在此 VPC 中
        $existing = DB::table('vpc_members')
            ->where('vpc_id', $vpcId)
            ->where('vm_name', $vmName)
            ->first();
        if ($existing) {
            throw new \RuntimeException("VM [{$vmName}] 已在 VPC [{$vpc->name}] 中");
        }

        // 为 VM 添加第二块网卡（eth1），连接到 VPC 的 managed network
        $deviceName = 'eth1-vpc';
        $this->client->patch('/1.0/instances/' . $vmName, [
            'devices' => [
                $deviceName => [
                    'type' => 'nic',
                    'network' => $vpc->incus_network,
                    'name' => 'eth1',
                ],
            ],
        ]);

        // 等待 DHCP 分配 IP 后查询（轮询 Incus 实例状态获取 eth1 的 IP）
        $privateIp = $this->waitForPrivateIp($vmName, 'eth1');

        // 超时未获取到 IP，回滚网卡添加
        if ($privateIp === null) {
            try {
                $this->removeVpcNic($vmName);
            } catch (\Exception $e) {
                Log::error('回滚 VPC 网卡失败', [
                    'vm_name' => $vmName,
                    'error' => $e->getMessage(),
                ]);
            }
            throw new \RuntimeException(
                "VM [{$vmName}] 加入 VPC 失败：DHCP 分配私有 IP 超时，网卡已回滚"
            );
        }

        // 记录成员关系
        $memberId = DB::table('vpc_members')->insertGetId([
            'vpc_id' => $vpcId,
            'vm_name' => $vmName,
            'private_ip' => $privateIp,
            'joined_at' => now(),
        ]);

        Log::info('VM 已加入 VPC', [
            'vpc_id' => $vpcId,
            'vpc_name' => $vpc->name,
            'vm_name' => $vmName,
            'private_ip' => $privateIp,
        ]);

        return [
            'id' => $memberId,
            'vpc_id' => $vpcId,
            'vm_name' => $vmName,
            'private_ip' => $privateIp,
        ];
    }

    /**
     * VM 退出 VPC（移除第二块网卡）
     *
     * @param int $vpcId VPC ID
     * @param string $vmName VM 名称
     */
    public function detachVm(int $vpcId, string $vmName): void
    {
        $vpc = DB::table('vpcs')->where('id', $vpcId)->first();
        if (!$vpc) {
            throw new \RuntimeException("VPC [{$vpcId}] 不存在");
        }

        $member = DB::table('vpc_members')
            ->where('vpc_id', $vpcId)
            ->where('vm_name', $vmName)
            ->first();
        if (!$member) {
            throw new \RuntimeException("VM [{$vmName}] 不在 VPC [{$vpc->name}] 中");
        }

        // 移除 VM 的 VPC 网卡
        $this->removeVpcNic($vmName);

        DB::table('vpc_members')
            ->where('vpc_id', $vpcId)
            ->where('vm_name', $vmName)
            ->delete();

        Log::info('VM 已退出 VPC', [
            'vpc_id' => $vpcId,
            'vpc_name' => $vpc->name,
            'vm_name' => $vmName,
        ]);
    }

    /**
     * 列出用户的所有 VPC
     *
     * @param int $userId 用户 ID
     * @return array VPC 列表（含成员数量）
     */
    public function listVpcs(int $userId): array
    {
        $vpcs = DB::table('vpcs')
            ->where('user_id', $userId)
            ->orderBy('created_at', 'desc')
            ->get();

        return $vpcs->map(function ($vpc) {
            $members = DB::table('vpc_members')
                ->where('vpc_id', $vpc->id)
                ->get();

            return [
                'id' => $vpc->id,
                'name' => $vpc->name,
                'subnet' => $vpc->subnet,
                'incus_network' => $vpc->incus_network,
                'created_at' => $vpc->created_at,
                'members' => $members->map(function ($m) {
                    return [
                        'vm_name' => $m->vm_name,
                        'private_ip' => $m->private_ip,
                        'joined_at' => $m->joined_at,
                    ];
                })->toArray(),
                'member_count' => $members->count(),
            ];
        })->toArray();
    }

    /**
     * 移除 VM 的 VPC 网卡
     */
    private function removeVpcNic(string $vmName): void
    {
        $instance = $this->client->get('/1.0/instances/' . $vmName);
        $devices = $instance['metadata']['devices'] ?? [];
        unset($devices['eth1-vpc']);

        $this->client->put('/1.0/instances/' . $vmName, [
            'devices' => $devices,
        ]);
    }

    /**
     * 轮询获取 VM 的 VPC 私有 IP
     *
     * @param string $vmName VM 名称
     * @param string $iface 网卡名称
     * @param int $maxWait 最大等待秒数
     * @return string|null 分配到的私有 IP
     */
    private function waitForPrivateIp(string $vmName, string $iface, int $maxWait = 30): ?string
    {
        $deadline = time() + $maxWait;

        while (time() < $deadline) {
            $state = $this->client->get('/1.0/instances/' . $vmName . '/state');
            $networks = $state['metadata']['network'] ?? [];

            if (isset($networks[$iface])) {
                foreach ($networks[$iface]['addresses'] ?? [] as $addr) {
                    if ($addr['family'] === 'inet' && $addr['scope'] === 'global') {
                        return $addr['address'];
                    }
                }
            }

            sleep(2);
        }

        Log::warning('等待 VPC 私有 IP 超时', [
            'vm_name' => $vmName,
            'iface' => $iface,
        ]);

        return null;
    }
}
