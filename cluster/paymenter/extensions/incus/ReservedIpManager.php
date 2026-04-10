<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * Reserved IP 管理器
 *
 * 允许用户保留 IP 地址（VM 删除后 IP 不释放），可绑定到新 VM。
 * 保留 IP 按小时计费，即使未绑定 VM 也产生费用。
 */
class ReservedIpManager
{
    /** 保留 IP 每小时费用（元） */
    private const HOURLY_RATE = 0.05;

    /**
     * 将 IP 标记为用户保留
     *
     * @param int $userId 用户 ID
     * @param string $ip IP 地址
     * @return array IP 记录
     * @throws \RuntimeException IP 不可保留
     */
    public function reserveIp(int $userId, string $ip): array
    {
        return DB::transaction(function () use ($userId, $ip) {
            $record = DB::table('ip_addresses')
                ->where('ip', $ip)
                ->lockForUpdate()
                ->first();

            if (!$record) {
                throw new \RuntimeException("IP [{$ip}] 不存在");
            }

            // 仅允许保留 "已分配给该用户的" 或 "可用" 的 IP
            if ($record->status === 'reserved') {
                throw new \RuntimeException("IP [{$ip}] 已被保留");
            }

            if ($record->status === 'cooldown') {
                throw new \RuntimeException("IP [{$ip}] 处于冷却期，暂不可操作");
            }

            // 如果 IP 已分配，需要确认是该用户的 VM
            if ($record->status === 'allocated') {
                $order = DB::table('orders')->where('id', $record->order_id)->first();
                if (!$order || $order->user_id !== $userId) {
                    throw new \RuntimeException("IP [{$ip}] 不属于当前用户");
                }
            }

            DB::table('ip_addresses')
                ->where('id', $record->id)
                ->update([
                    'status' => 'reserved',
                    'reserved_by_user' => $userId,
                    'reserved_at' => now(),
                ]);

            Log::info('IP 已保留', [
                'ip' => $ip,
                'user_id' => $userId,
                'previous_status' => $record->status,
            ]);

            return [
                'id' => $record->id,
                'ip' => $ip,
                'user_id' => $userId,
                'vm_name' => $record->vm_name,
                'reserved_at' => now()->toDateTimeString(),
                'hourly_rate' => self::HOURLY_RATE,
            ];
        });
    }

    /**
     * 释放保留的 IP（恢复为可用）
     *
     * @param int $ipId IP 记录 ID
     * @throws \RuntimeException IP 未处于保留状态
     */
    public function releaseReservedIp(int $ipId): void
    {
        $record = DB::table('ip_addresses')
            ->where('id', $ipId)
            ->where('status', 'reserved')
            ->first();

        if (!$record) {
            throw new \RuntimeException("IP 记录 [{$ipId}] 不存在或非保留状态");
        }

        // 如果仍绑定 VM，先解绑
        if ($record->vm_name) {
            throw new \RuntimeException("IP [{$record->ip}] 仍绑定在 VM [{$record->vm_name}] 上，请先解绑");
        }

        DB::table('ip_addresses')
            ->where('id', $ipId)
            ->update([
                'status' => 'available',
                'reserved_by_user' => null,
                'reserved_at' => null,
                'vm_name' => null,
                'order_id' => null,
                'allocated_at' => null,
            ]);

        Log::info('保留 IP 已释放', [
            'ip_id' => $ipId,
            'ip' => $record->ip,
            'user_id' => $record->reserved_by_user,
        ]);
    }

    /**
     * 将保留 IP 绑定到指定 VM
     *
     * @param int $ipId IP 记录 ID
     * @param string $vmName VM 名称
     * @return array 更新后的 IP 记录
     * @throws \RuntimeException IP 非保留状态或已绑定
     */
    public function assignToVm(int $ipId, string $vmName): array
    {
        return DB::transaction(function () use ($ipId, $vmName) {
            $record = DB::table('ip_addresses')
                ->where('id', $ipId)
                ->where('status', 'reserved')
                ->lockForUpdate()
                ->first();

            if (!$record) {
                throw new \RuntimeException("IP 记录 [{$ipId}] 不存在或非保留状态");
            }

            if ($record->vm_name) {
                throw new \RuntimeException("保留 IP [{$record->ip}] 已绑定到 VM [{$record->vm_name}]");
            }

            DB::table('ip_addresses')
                ->where('id', $ipId)
                ->update([
                    'vm_name' => $vmName,
                    'allocated_at' => now(),
                ]);

            Log::info('保留 IP 已绑定到 VM', [
                'ip_id' => $ipId,
                'ip' => $record->ip,
                'vm_name' => $vmName,
                'user_id' => $record->reserved_by_user,
            ]);

            return [
                'id' => $record->id,
                'ip' => $record->ip,
                'vm_name' => $vmName,
                'reserved_by_user' => $record->reserved_by_user,
            ];
        });
    }

    /**
     * 列出用户的所有保留 IP
     *
     * @param int $userId 用户 ID
     * @return array 保留 IP 列表（含费用信息）
     */
    public function listReservedIps(int $userId): array
    {
        $ips = DB::table('ip_addresses')
            ->where('reserved_by_user', $userId)
            ->where('status', 'reserved')
            ->get();

        return $ips->map(function ($ip) {
            $reservedHours = $ip->reserved_at
                ? now()->diffInHours($ip->reserved_at)
                : 0;

            return [
                'id' => $ip->id,
                'ip' => $ip->ip,
                'vm_name' => $ip->vm_name,
                'reserved_at' => $ip->reserved_at,
                'reserved_hours' => $reservedHours,
                'accrued_cost' => round($reservedHours * self::HOURLY_RATE, 2),
                'hourly_rate' => self::HOURLY_RATE,
            ];
        })->toArray();
    }
}
