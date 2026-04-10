<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\IncusClient;
use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * 超额限速（每小时）
 *
 * 检查当月流量是否超额，超额则限速至 10Mbit。
 * 月初（1日）自动重置限速。
 */
class TrafficThrottle
{
    private const THROTTLE_RATE = '10Mbit';

    public function __invoke(): void
    {
        $client = app(IncusClient::class);
        $currentPeriod = Carbon::now()->format('Y-m');

        $orders = DB::table('orders')
            ->where('status', 'active')
            ->whereNotNull('vm_name')
            ->get();

        foreach ($orders as $order) {
            try {
                $stats = DB::table('traffic_stats')
                    ->where('order_id', $order->id)
                    ->where('period', $currentPeriod)
                    ->first();

                if (!$stats) {
                    continue;
                }

                $totalBytes = ($stats->bytes_in ?? 0) + ($stats->bytes_out ?? 0);
                $quotaBytes = ($order->traffic_quota_gb ?? 0) * 1024 * 1024 * 1024;

                if ($quotaBytes <= 0) {
                    continue;
                }

                $isOverQuota = $totalBytes > $quotaBytes;
                $isThrottled = DB::table('traffic_throttle')
                    ->where('order_id', $order->id)
                    ->where('period', $currentPeriod)
                    ->exists();

                if ($isOverQuota && !$isThrottled) {
                    // 执行限速
                    $client->updateInstance($order->vm_name, [
                        'devices' => [
                            'eth0' => [
                                'type' => 'nic',
                                'limits.ingress' => self::THROTTLE_RATE,
                                'limits.egress' => self::THROTTLE_RATE,
                            ],
                        ],
                    ]);

                    DB::table('traffic_throttle')->insert([
                        'order_id' => $order->id,
                        'period' => $currentPeriod,
                        'throttled_at' => Carbon::now(),
                    ]);

                    Log::info("TrafficThrottle: VM {$order->vm_name} 超额限速至 " . self::THROTTLE_RATE);
                }
            } catch (\Throwable $e) {
                Log::warning("TrafficThrottle: 处理 VM {$order->vm_name} 失败: {$e->getMessage()}");
            }
        }
    }
}
