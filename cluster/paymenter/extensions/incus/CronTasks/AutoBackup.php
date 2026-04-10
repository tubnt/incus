<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\IncusClient;
use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * 自动备份（每日 03:00，Ceph RBD snapshot）
 *
 * 为所有活跃 VM 创建 Ceph RBD 快照备份，保留 7 天。
 */
class AutoBackup
{
    public function __invoke(): void
    {
        $client = app(IncusClient::class);
        $now = Carbon::now();
        $snapshotName = 'auto-' . $now->format('Ymd-His');

        $orders = DB::table('orders')
            ->where('status', 'active')
            ->whereNotNull('vm_name')
            ->get();

        foreach ($orders as $order) {
            try {
                $client->createSnapshot($order->vm_name, $snapshotName, stateful: false);

                DB::table('backups')->insert([
                    'order_id' => $order->id,
                    'vm_name' => $order->vm_name,
                    'snapshot_name' => $snapshotName,
                    'type' => 'auto',
                    'created_at' => $now,
                ]);

                Log::info("AutoBackup: VM {$order->vm_name} 快照 {$snapshotName} 创建成功");
            } catch (\Throwable $e) {
                Log::error("AutoBackup: VM {$order->vm_name} 备份失败: {$e->getMessage()}");
            }
        }
    }
}
