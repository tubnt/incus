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
                $period = $now->format('Y-m');

                // 原子 upsert：防止并发 INSERT 重复
                $existing = DB::table('traffic_stats')
                    ->where('order_id', $order->id)
                    ->where('period', $period)
                    ->lockForUpdate()
                    ->first();

                if ($existing) {
                    // 增量累加：用当前计数器值减去上次记录的计数器值
                    // 如果当前值小于上次值（VM 重启导致计数器归零），则当前值本身就是增量
                    $prevCounterIn = $existing->last_counter_in ?? 0;
                    $prevCounterOut = $existing->last_counter_out ?? 0;

                    $deltaIn = $bytesIn >= $prevCounterIn ? $bytesIn - $prevCounterIn : $bytesIn;
                    $deltaOut = $bytesOut >= $prevCounterOut ? $bytesOut - $prevCounterOut : $bytesOut;

                    DB::table('traffic_stats')
                        ->where('order_id', $order->id)
                        ->where('period', $period)
                        ->update([
                            'bytes_in' => $existing->bytes_in + $deltaIn,
                            'bytes_out' => $existing->bytes_out + $deltaOut,
                            'last_counter_in' => $bytesIn,
                            'last_counter_out' => $bytesOut,
                            'updated_at' => $now,
                        ]);
                } else {
                    // 首次记录：当前计数器值即为本月初始流量
                    DB::table('traffic_stats')->updateOrInsert(
                        ['order_id' => $order->id, 'period' => $period],
                        [
                            'bytes_in' => $bytesIn,
                            'bytes_out' => $bytesOut,
                            'last_counter_in' => $bytesIn,
                            'last_counter_out' => $bytesOut,
                            'updated_at' => $now,
                        ]
                    );
                }
            } catch (\Throwable $e) {
                Log::warning("TrafficStats: 采集 VM {$order->vm_name} 流量失败: {$e->getMessage()}");
            }
        }
    }
}
