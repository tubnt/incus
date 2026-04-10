<?php

namespace Extensions\Incus\CronTasks;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * IP 池余量检查（每小时）
 *
 * 检查各 IP 池的可用 IP 数量，低于 10% 时发出告警。
 */
class IpPoolAlert
{
    private const ALERT_THRESHOLD = 0.10;

    public function __invoke(): void
    {
        $pools = DB::table('ip_pools')->get();

        foreach ($pools as $pool) {
            $total = DB::table('ip_addresses')
                ->where('pool_id', $pool->id)
                ->count();

            if ($total === 0) {
                continue;
            }

            $available = DB::table('ip_addresses')
                ->where('pool_id', $pool->id)
                ->where('status', 'available')
                ->count();

            $ratio = $available / $total;

            if ($ratio < self::ALERT_THRESHOLD) {
                Log::critical("IpPoolAlert: IP 池 {$pool->name}（ID: {$pool->id}）可用 IP 不足！" .
                    "可用 {$available}/{$total}（{$this->percent($ratio)}），低于阈值 " .
                    $this->percent(self::ALERT_THRESHOLD));
            }
        }
    }

    private function percent(float $ratio): string
    {
        return round($ratio * 100, 1) . '%';
    }
}
