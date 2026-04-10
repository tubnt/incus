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

            // 仅允许保留已分配给当前用户的 IP（禁止保留 available/cooldown 等状态的 IP）
            if ($record->status === 'reserved') {
                throw new \RuntimeException("IP [{$ip}] 已被保留");
            }

            if ($record->status !== 'allocated') {
                throw new \RuntimeException("仅可保留已分配给您的 IP（当前状态：{$record->status}）");
            }

            // 验证 IP 确实属于该用户
            $order = DB::table('orders')->where('id', $record->order_id)->first();
            if (!$order || (int) $order->user_id !== $userId) {
                throw new \RuntimeException("IP [{$ip}] 不属于当前用户");
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
     * @param int $userId 操作用户 ID（用于鉴权）
     * @throws \RuntimeException IP 未处于保留状态或不属于该用户
     */
    public function releaseReservedIp(int $ipId, int $userId): void
    {
        DB::transaction(function () use ($ipId, $userId) {
            $record = DB::table('ip_addresses')
                ->where('id', $ipId)
                ->where('status', 'reserved')
                ->lockForUpdate()
                ->first();

            if (!$record) {
                throw new \RuntimeException("IP 记录 [{$ipId}] 不存在或非保留状态");
            }

            if ((int) $record->reserved_by_user !== $userId) {
                throw new \RuntimeException("无权操作此保留 IP");
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
        });
    }

    /**
     * 将保留 IP 绑定到指定 VM
     *
     * @param int $ipId IP 记录 ID
     * @param string $vmName VM 名称
     * @param int $userId 操作用户 ID（用于鉴权）
     * @return array 更新后的 IP 记录
     * @throws \RuntimeException IP 非保留状态、已绑定或不属于该用户
     */
    public function assignToVm(int $ipId, string $vmName, int $userId): array
    {
        return DB::transaction(function () use ($ipId, $vmName, $userId) {
            $record = DB::table('ip_addresses')
                ->where('id', $ipId)
                ->where('status', 'reserved')
                ->lockForUpdate()
                ->first();

            if (!$record) {
                throw new \RuntimeException("IP 记录 [{$ipId}] 不存在或非保留状态");
            }

            if ((int) $record->reserved_by_user !== $userId) {
                throw new \RuntimeException("无权操作此保留 IP");
            }

            // 验证目标 VM 属于该用户
            $vmIp = DB::table('ip_addresses')->where('vm_name', $vmName)->first();
            if ($vmIp && $vmIp->order_id) {
                $order = DB::table('orders')->where('id', $vmIp->order_id)->first();
                if (!$order || (int) $order->user_id !== $userId) {
                    throw new \RuntimeException("VM [{$vmName}] 不属于当前用户");
                }
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
