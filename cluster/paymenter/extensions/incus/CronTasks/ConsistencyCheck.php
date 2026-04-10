<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\IncusClient;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * Paymenter ↔ Incus 一致性巡检（每 6 小时）
 *
 * 检测 Paymenter 订单状态与 Incus 实际 VM 状态之间的不一致。
 * 发现不一致时记录日志并告警，不自动修复。
 */
class ConsistencyCheck
{
    public function __invoke(): void
    {
        $client = app(IncusClient::class);
        $anomalies = [];

        // 获取 Incus 中所有实例
        $incusInstances = collect($client->listInstances())
            ->keyBy(fn ($name) => $name);

        // 检查 Paymenter 中活跃/暂停订单对应的 VM
        $orders = DB::table('orders')
            ->whereIn('status', ['active', 'suspended'])
            ->whereNotNull('vm_name')
            ->get();

        foreach ($orders as $order) {
            if (!$incusInstances->has($order->vm_name)) {
                $anomalies[] = "VM 缺失: 订单 #{$order->id} 的 VM {$order->vm_name} 在 Incus 中不存在";
                continue;
            }

            try {
                $state = $client->getInstanceState($order->vm_name);
                $vmStatus = $state['status'] ?? 'Unknown';

                // 活跃订单的 VM 应为 Running
                if ($order->status === 'active' && $vmStatus !== 'Running') {
                    $anomalies[] = "状态不一致: 订单 #{$order->id}（active）的 VM {$order->vm_name} 状态为 {$vmStatus}";
                }

                // 暂停订单的 VM 应为 Stopped
                if ($order->status === 'suspended' && $vmStatus !== 'Stopped') {
                    $anomalies[] = "状态不一致: 订单 #{$order->id}（suspended）的 VM {$order->vm_name} 状态为 {$vmStatus}";
                }
            } catch (\Throwable $e) {
                $anomalies[] = "检查失败: VM {$order->vm_name} 状态查询异常: {$e->getMessage()}";
            }

            $incusInstances->forget($order->vm_name);
        }

        // 检查 Incus 中存在但 Paymenter 无对应订单的 VM（排除系统 VM）
        foreach ($incusInstances as $vmName) {
            if (str_starts_with($vmName, 'sys-') || str_starts_with($vmName, 'mgmt-')) {
                continue;
            }
            $anomalies[] = "孤立 VM: {$vmName} 在 Incus 中存在但无对应活跃订单";
        }

        if (count($anomalies) > 0) {
            Log::warning('ConsistencyCheck: 发现 ' . count($anomalies) . ' 项不一致', $anomalies);
        } else {
            Log::info('ConsistencyCheck: 一致性巡检通过，未发现异常');
        }
    }
}
