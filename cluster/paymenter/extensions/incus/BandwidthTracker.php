<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;

/**
 * 带宽用量追踪 — 月度流量统计、超额检测与自动限速
 *
 * 流量统计从 Incus metrics（incus_network_receive/transmit_bytes_total）采集。
 * 超额策略：Hetzner 模式，超出配额后限速到 10Mbps，次月 1 日恢复原速。
 */
class BandwidthTracker
{
    private IncusClient $client;

    /** 超额限速值 */
    private const THROTTLE_RATE = '10Mbit';

    /** 带宽记录表 */
    private const TABLE = 'bandwidth_usage';

    /** Incus 实例名允许的字符 */
    private const NAME_PATTERN = '/^[a-zA-Z0-9][a-zA-Z0-9\-]{0,62}$/';

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    private function validateVmName(string $vmName): void
    {
        if (!preg_match(self::NAME_PATTERN, $vmName)) {
            throw new \InvalidArgumentException("VM 名称格式无效: '{$vmName}'");
        }
    }

    /**
     * 获取当月流量使用情况
     *
     * @param string $vmName VM 实例名
     * @return array{
     *     rx_bytes: int, tx_bytes: int, total_bytes: int,
     *     quota_bytes: int, usage_percent: float,
     *     is_throttled: bool, period: string
     * }
     */
    public function getMonthlyUsage(string $vmName): array
    {
        $this->validateVmName($vmName);
        $period = date('Y-m');

        $record = DB::table(self::TABLE)
            ->where('vm_name', $vmName)
            ->where('period', $period)
            ->first();

        if (!$record) {
            // 从 Incus 实时采集当前计数器
            $current = $this->collectCurrentBytes($vmName);
            $total = $current['rx_bytes'] + $current['tx_bytes'];
            $quota = $this->getQuota($vmName);
            $percent = $quota > 0 ? round(($total / $quota) * 100, 2) : 0.0;

            return [
                'rx_bytes'      => $current['rx_bytes'],
                'tx_bytes'      => $current['tx_bytes'],
                'total_bytes'   => $total,
                'quota_bytes'   => $quota,
                'usage_percent' => $percent,
                'is_throttled'  => false,
                'period'        => $period,
            ];
        }

        $total = $record->rx_bytes + $record->tx_bytes;
        $quota = $record->quota_bytes;
        $percent = $quota > 0 ? round(($total / $quota) * 100, 2) : 0.0;

        return [
            'rx_bytes'      => (int) $record->rx_bytes,
            'tx_bytes'      => (int) $record->tx_bytes,
            'total_bytes'   => $total,
            'quota_bytes'   => (int) $quota,
            'usage_percent' => $percent,
            'is_throttled'  => (bool) $record->is_throttled,
            'period'        => $period,
        ];
    }

    /**
     * 检查是否超出流量配额
     *
     * @param string $vmName VM 实例名
     * @return array{exceeded: bool, overage_bytes: int, usage_percent: float}
     */
    public function checkOverage(string $vmName): array
    {
        $usage = $this->getMonthlyUsage($vmName);
        $exceeded = $usage['total_bytes'] > $usage['quota_bytes'] && $usage['quota_bytes'] > 0;
        $overage = $exceeded ? $usage['total_bytes'] - $usage['quota_bytes'] : 0;

        return [
            'exceeded'      => $exceeded,
            'overage_bytes' => $overage,
            'usage_percent' => $usage['usage_percent'],
        ];
    }

    /**
     * 对超额 VM 应用限速（10Mbps）
     *
     * 通过修改 Incus 实例网络设备的 limits.ingress/egress 实现。
     * 由 Cron 每小时调用。
     *
     * @param string $vmName VM 实例名
     * @throws \RuntimeException
     */
    public function applyThrottle(string $vmName): void
    {
        $this->validateVmName($vmName);
        $instance = $this->client->request('GET', "/1.0/instances/{$vmName}");
        $devices = $instance['metadata']['devices'] ?? [];

        // 找到网络设备并设置限速
        $updated = false;
        foreach ($devices as $name => &$device) {
            if ($device['type'] !== 'nic') {
                continue;
            }
            $device['limits.ingress'] = self::THROTTLE_RATE;
            $device['limits.egress'] = self::THROTTLE_RATE;
            $updated = true;
        }
        unset($device);

        if (!$updated) {
            throw new \RuntimeException("VM '{$vmName}' 未找到网络设备，无法限速");
        }

        $this->client->request('PATCH', "/1.0/instances/{$vmName}", [
            'devices' => $devices,
        ]);

        // 更新数据库标记
        DB::table(self::TABLE)
            ->where('vm_name', $vmName)
            ->where('period', date('Y-m'))
            ->update([
                'is_throttled' => true,
                'throttled_at' => now(),
            ]);
    }

    /**
     * 恢复原始带宽（移除限速）
     *
     * 由 Cron 在每月 1 日执行，恢复所有被限速 VM 的原始带宽。
     */
    public function resetMonthly(): void
    {
        // 只处理上月被限速的记录，避免重复处理历史数据或误触当月记录
        $previousMonth = date('Y-m', strtotime('first day of last month'));
        $throttled = DB::table(self::TABLE)
            ->where('is_throttled', true)
            ->where('period', $previousMonth)
            ->get();

        foreach ($throttled as $record) {
            try {
                $this->removeThrottle($record->vm_name);

                DB::table(self::TABLE)
                    ->where('vm_name', $record->vm_name)
                    ->where('period', $record->period)
                    ->update([
                        'is_throttled' => false,
                    ]);
            } catch (\Exception $e) {
                // 记录失败但继续处理其他 VM
                \Log::error("恢复 VM '{$record->vm_name}' 带宽失败: " . $e->getMessage());
            }
        }
    }

    /**
     * 从 Incus 实时采集网络接口字节数
     *
     * @return array{rx_bytes: int, tx_bytes: int}
     */
    private function collectCurrentBytes(string $vmName): array
    {
        $state = $this->client->request('GET', "/1.0/instances/{$vmName}/state");
        $network = $state['metadata']['network'] ?? [];

        $rxTotal = 0;
        $txTotal = 0;
        foreach ($network as $name => $iface) {
            if ($name === 'lo') {
                continue;
            }
            $counters = $iface['counters'] ?? [];
            $rxTotal += $counters['bytes_received'] ?? 0;
            $txTotal += $counters['bytes_sent'] ?? 0;
        }

        return ['rx_bytes' => $rxTotal, 'tx_bytes' => $txTotal];
    }

    /**
     * 移除单个 VM 的限速配置
     */
    private function removeThrottle(string $vmName): void
    {
        $this->validateVmName($vmName);
        $instance = $this->client->request('GET', "/1.0/instances/{$vmName}");
        $devices = $instance['metadata']['devices'] ?? [];

        foreach ($devices as $name => &$device) {
            if ($device['type'] !== 'nic') {
                continue;
            }
            unset($device['limits.ingress'], $device['limits.egress']);
        }
        unset($device);

        $this->client->request('PATCH', "/1.0/instances/{$vmName}", [
            'devices' => $devices,
        ]);
    }

    /**
     * 获取 VM 的月度流量配额
     *
     * 从订单/产品配置中读取，默认 1TB。
     */
    private function getQuota(string $vmName): int
    {
        $record = DB::table('servers')
            ->where('name', $vmName)
            ->first();

        if ($record && isset($record->bandwidth_limit)) {
            return (int) $record->bandwidth_limit;
        }

        // 默认 1TB
        return 1024 * 1024 * 1024 * 1024;
    }
}
