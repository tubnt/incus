<?php

namespace Extensions\Incus\CronTasks;

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * IP 冷却期回收（每小时）
 *
 * 将超过 24 小时冷却期的 IP 地址状态重置为 available。
 */
class IpCooldownRecycle
{
    public function __invoke(): void
    {
        $count = DB::table('ip_addresses')
            ->where('status', 'cooldown')
            ->where('cooldown_until', '<=', Carbon::now())
            ->update([
                'status' => 'available',
                'cooldown_until' => null,
                'released_at' => null,
            ]);

        if ($count > 0) {
            Log::info("IpCooldownRecycle: 回收了 {$count} 个冷却期满的 IP 地址");
        }
    }
}
