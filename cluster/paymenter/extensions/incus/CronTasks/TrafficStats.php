<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\IncusClient;
use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * 流量统计（每小时）
 *
 * 从 Incus 指标 API 采集各 VM 的网络流量数据，写入统计表。
 */
class TrafficStats
{
    public function __invoke(): void
    {
        $client = app(IncusClient::class);

        $orders = DB::table('orders')
            ->whereIn('status', ['active', 'suspended'])
            ->whereNotNull('vm_name')
            ->get();

        $now = Carbon::now();

        foreach ($orders as $order) {
            try {
                $state = $client->getInstanceState($order->vm_name);
                $network = $state['network']['eth0']['counters'] ?? null;

                if (!$network) {
                    continue;
                }

                $bytesIn = $network['bytes_received'] ?? 0;
                $bytesOut = $network['bytes_sent'] ?? 0;

                DB::table('traffic_stats')->updateOrInsert(
                    [
                        'order_id' => $order->id,
                        'period' => $now->format('Y-m'),
                    ],
                    [
                        'bytes_in' => $bytesIn,
                        'bytes_out' => $bytesOut,
                        'updated_at' => $now,
                    ],
                );
            } catch (\Throwable $e) {
                Log::warning("TrafficStats: 采集 VM {$order->vm_name} 流量失败: {$e->getMessage()}");
            }
        }
    }
}
