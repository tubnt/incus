<?php

namespace App\Extensions\Incus;

/**
 * 资源监控 — 从 Incus 采集 CPU/内存/磁盘 IO/网络带宽指标
 */
class MetricsCollector
{
    private IncusClient $client;

    /** 支持的时间范围（秒） */
    private const TIME_RANGES = [
        '1h'  => 3600,
        '24h' => 86400,
        '7d'  => 604800,
        '30d' => 2592000,
    ];

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
     * 获取 VM 实时指标
     *
     * @param string $vmName   VM 实例名
     * @param string $range    时间范围：1h/24h/7d/30d
     * @return array{cpu: array, memory: array, disk: array, network: array, timestamp: int}
     */
    public function getVmMetrics(string $vmName, string $range = '1h'): array
    {
        $this->validateVmName($vmName);
        if (!isset(self::TIME_RANGES[$range])) {
            throw new \InvalidArgumentException(
                "不支持的时间范围 '{$range}'，可选值：" . implode('/', array_keys(self::TIME_RANGES))
            );
        }

        $state = $this->client->request('GET', "/1.0/instances/{$vmName}/state");
        $metadata = $state['metadata'] ?? [];

        return [
            'cpu'       => $this->parseCpuMetrics($metadata),
            'memory'    => $this->parseMemoryMetrics($metadata),
            'disk'      => $this->parseDiskMetrics($metadata),
            'network'   => $this->parseNetworkMetrics($metadata),
            'range'     => $range,
            'timestamp' => time(),
        ];
    }

    /**
     * 解析 CPU 指标
     *
     * @return array{usage_ns: int, usage_percent: float}
     */
    private function parseCpuMetrics(array $metadata): array
    {
        $cpuUsage = $metadata['cpu']['usage'] ?? 0; // 纳秒

        return [
            'usage_ns'      => $cpuUsage,
            'usage_percent' => $this->calculateCpuPercent($cpuUsage),
        ];
    }

    /**
     * 估算 CPU 使用百分比
     * Incus 返回的是累计 CPU 纳秒，需结合采样间隔计算
     */
    private function calculateCpuPercent(int $usageNs): float
    {
        // 累计值无法直接转为百分比，返回 0 表示需前端做差值计算
        // 前端采集两个时间点的 usage_ns 差值 / 时间差 × 100 即为百分比
        return 0.0;
    }

    /**
     * 解析内存指标
     *
     * @return array{usage_bytes: int, total_bytes: int, usage_percent: float}
     */
    private function parseMemoryMetrics(array $metadata): array
    {
        $usage = $metadata['memory']['usage'] ?? 0;
        $total = $metadata['memory']['total'] ?? 0;
        $percent = $total > 0 ? round(($usage / $total) * 100, 2) : 0.0;

        return [
            'usage_bytes'   => $usage,
            'total_bytes'   => $total,
            'usage_percent' => $percent,
        ];
    }

    /**
     * 解析磁盘 IO 指标
     *
     * @return array<string, array{read_bytes: int, write_bytes: int}>
     */
    private function parseDiskMetrics(array $metadata): array
    {
        $diskData = $metadata['disk'] ?? [];
        $disks = [];

        foreach ($diskData as $name => $stats) {
            $disks[$name] = [
                'read_bytes'  => $stats['read_bytes'] ?? 0,
                'write_bytes' => $stats['write_bytes'] ?? 0,
            ];
        }

        return $disks;
    }

    /**
     * 解析网络带宽指标
     *
     * @return array<string, array{rx_bytes: int, tx_bytes: int, rx_packets: int, tx_packets: int}>
     */
    private function parseNetworkMetrics(array $metadata): array
    {
        $networkData = $metadata['network'] ?? [];
        $interfaces = [];

        foreach ($networkData as $name => $iface) {
            $counters = $iface['counters'] ?? [];
            $interfaces[$name] = [
                'rx_bytes'   => $counters['bytes_received'] ?? 0,
                'tx_bytes'   => $counters['bytes_sent'] ?? 0,
                'rx_packets' => $counters['packets_received'] ?? 0,
                'tx_packets' => $counters['packets_sent'] ?? 0,
            ];
        }

        return $interfaces;
    }

    /**
     * 获取格式化后的指标摘要（用于状态页展示）
     *
     * @param string $vmName VM 实例名
     * @return array{cpu: string, memory: string, disk_io: string, network: string}
     */
    public function getMetricsSummary(string $vmName): array
    {
        // getVmMetrics 内部已验证 vmName
        $metrics = $this->getVmMetrics($vmName, '1h');

        // 内存
        $memUsed = $this->formatBytes($metrics['memory']['usage_bytes']);
        $memTotal = $this->formatBytes($metrics['memory']['total_bytes']);
        $memPercent = $metrics['memory']['usage_percent'];

        // 网络（取第一个非 lo 接口）
        $netSummary = '无数据';
        foreach ($metrics['network'] as $name => $iface) {
            if ($name === 'lo') {
                continue;
            }
            $rx = $this->formatBytes($iface['rx_bytes']);
            $tx = $this->formatBytes($iface['tx_bytes']);
            $netSummary = "↓ {$rx} / ↑ {$tx}";
            break;
        }

        // 磁盘 IO
        $diskSummary = '无数据';
        foreach ($metrics['disk'] as $name => $disk) {
            $read = $this->formatBytes($disk['read_bytes']);
            $write = $this->formatBytes($disk['write_bytes']);
            $diskSummary = "读 {$read} / 写 {$write}";
            break;
        }

        return [
            'cpu'     => "累计 {$metrics['cpu']['usage_ns']} ns",
            'memory'  => "{$memUsed} / {$memTotal} ({$memPercent}%)",
            'disk_io' => $diskSummary,
            'network' => $netSummary,
        ];
    }

    /**
     * 字节数格式化
     */
    private function formatBytes(int $bytes): string
    {
        if ($bytes <= 0) {
            return '0 B';
        }

        $units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
        $exp = min(floor(log($bytes, 1024)), count($units) - 1);
        $value = round($bytes / pow(1024, $exp), 2);

        return "{$value} {$units[$exp]}";
    }
}
