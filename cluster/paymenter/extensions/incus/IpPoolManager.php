<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * IP 池管理器
 *
 * 负责 IP 地址的分配、回收、冷却期管理和统计。
 * 使用数据库事务 + FOR UPDATE 行锁保证并发安全。
 */
class IpPoolManager
{
    /**
     * 从指定池中分配一个可用 IP
     *
     * @param int $poolId IP 池 ID
     * @param string $vmName VM 名称
     * @param int $orderId 订单 ID
     * @return array IP 地址记录（含 ip, gateway, netmask）
     * @throws \RuntimeException 无可用 IP 时抛出
     */
    public function allocate(int $poolId, string $vmName, int $orderId): array
    {
        return DB::transaction(function () use ($poolId, $vmName, $orderId) {
            // FOR UPDATE 行锁，防止并发分配同一 IP
            $ip = DB::table('ip_addresses')
                ->where('pool_id', $poolId)
                ->where('status', 'available')
                ->orderByRaw('INET_ATON(ip)')
                ->lockForUpdate()
                ->first();

            if (!$ip) {
                Log::error('IP 池耗尽', ['pool_id' => $poolId]);
                throw new \RuntimeException("IP 池 [{$poolId}] 无可用地址");
            }

            DB::table('ip_addresses')
                ->where('id', $ip->id)
                ->update([
                    'status' => 'allocated',
                    'vm_name' => $vmName,
                    'order_id' => $orderId,
                    'allocated_at' => now(),
                    'released_at' => null,
                    'cooldown_until' => null,
                ]);

            // 获取池的网关和掩码信息
            $pool = DB::table('ip_pools')->where('id', $poolId)->first();

            Log::info('IP 已分配', [
                'ip' => $ip->ip,
                'pool_id' => $poolId,
                'vm_name' => $vmName,
                'order_id' => $orderId,
            ]);

            return [
                'ip' => $ip->ip,
                'gateway' => $pool->gateway,
                'netmask' => $pool->netmask,
                'subnet' => $pool->subnet,
            ];
        });
    }

    /**
     * 释放 IP，进入冷却期（默认 24 小时）
     *
     * @param string $ip IP 地址
     * @param int $cooldownHours 冷却时长（小时）
     */
    public function release(string $ip, int $cooldownHours = 24): void
    {
        $affected = DB::table('ip_addresses')
            ->where('ip', $ip)
            ->where('status', 'allocated')
            ->update([
                'status' => 'cooldown',
                'vm_name' => null,
                'order_id' => null,
                'released_at' => now(),
                'cooldown_until' => now()->addHours($cooldownHours),
            ]);

        if ($affected === 0) {
            Log::warning('IP 释放失败：未找到已分配记录', ['ip' => $ip]);
            return;
        }

        Log::info('IP 已释放，进入冷却期', [
            'ip' => $ip,
            'cooldown_hours' => $cooldownHours,
        ]);
    }

    /**
     * 将冷却期已过的 IP 恢复为可用状态
     *
     * @return int 恢复的 IP 数量
     */
    public function getCooldownExpired(): int
    {
        $affected = DB::table('ip_addresses')
            ->where('status', 'cooldown')
            ->where('cooldown_until', '<', now())
            ->update([
                'status' => 'available',
                'released_at' => null,
                'cooldown_until' => null,
            ]);

        if ($affected > 0) {
            Log::info('冷却期 IP 已恢复', ['count' => $affected]);
        }

        return $affected;
    }

    /**
     * 获取 IP 池统计信息
     *
     * @param int $poolId IP 池 ID
     * @return array 各状态的 IP 数量
     */
    public function getPoolStats(int $poolId): array
    {
        $stats = DB::table('ip_addresses')
            ->where('pool_id', $poolId)
            ->selectRaw("
                COUNT(*) as total,
                SUM(CASE WHEN status = 'available' THEN 1 ELSE 0 END) as available,
                SUM(CASE WHEN status = 'allocated' THEN 1 ELSE 0 END) as allocated,
                SUM(CASE WHEN status = 'cooldown' THEN 1 ELSE 0 END) as cooldown,
                SUM(CASE WHEN status = 'reserved' THEN 1 ELSE 0 END) as reserved
            ")
            ->first();

        return [
            'total' => (int) $stats->total,
            'available' => (int) $stats->available,
            'allocated' => (int) $stats->allocated,
            'cooldown' => (int) $stats->cooldown,
            'reserved' => (int) $stats->reserved,
        ];
    }
}
