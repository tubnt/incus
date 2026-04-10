<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\IncusClient;
use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * 月初流量重置（每月 1 日 00:00）
 *
 * 重置所有 VM 的流量统计计数器，并解除上月的超额限速。
 */
class MonthlyTrafficReset
{
    public function __invoke(): void
    {
        $client = app(IncusClient::class);
        $lastPeriod = Carbon::now()->subMonth()->format('Y-m');

        // 解除上月限速的 VM
        $throttled = DB::table('traffic_throttle')
            ->where('period', $lastPeriod)
            ->get();

        foreach ($throttled as $record) {
            $order = DB::table('orders')->find($record->order_id);
            if (!$order || !($order->vm_name ?? null) || $order->status !== 'active') {
                continue;
            }

            try {
                // 移除限速设置，恢复默认带宽
                $client->updateInstance($order->vm_name, [
                    'devices' => [
                        'eth0' => [
                            'type' => 'nic',
                            'limits.ingress' => '',
                            'limits.egress' => '',
                        ],
                    ],
                ]);

                Log::info("MonthlyTrafficReset: VM {$order->vm_name} 限速已解除");
            } catch (\Throwable $e) {
                Log::warning("MonthlyTrafficReset: 解除 VM {$order->vm_name} 限速失败: {$e->getMessage()}");
            }
        }

        // 清理上月限速记录
        DB::table('traffic_throttle')->where('period', $lastPeriod)->delete();

        Log::info("MonthlyTrafficReset: {$lastPeriod} 流量重置完成，解除 {$throttled->count()} 台 VM 限速");
    }
}
