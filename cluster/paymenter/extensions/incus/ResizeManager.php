<?php

namespace App\Extensions\Incus;

/**
 * 升降配管理器
 *
 * - 升配（CPU/内存增加）：热操作，不停机
 * - 降配（CPU/内存减少）：需停机 → 修改 → 启动
 * - 磁盘扩容：热操作，不停机
 * - 磁盘缩小：禁止（数据安全）
 * - 补差价：按剩余天数折算
 */
class ResizeManager
{
    private IncusClient $client;

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    /**
     * 升配（热操作，不停机）
     *
     * CPU/内存只增不减时可热操作
     *
     * @param int    $newCpu 新 CPU 核数
     * @param string $newMem 新内存大小（如 "4GB"）
     * @throws \InvalidArgumentException 参数无效
     */
    public function upgrade(string $vmName, int $newCpu, string $newMem): array
    {
        $this->validateCpuMem($newCpu, $newMem);

        // 获取当前配置，确认是升配
        $current = $this->getCurrentConfig($vmName);
        $currentCpu = (int) ($current['limits.cpu'] ?? 0);
        $currentMem = $current['limits.memory'] ?? '0';

        if ($newCpu < $currentCpu || $this->parseMemoryBytes($newMem) < $this->parseMemoryBytes($currentMem)) {
            throw new \InvalidArgumentException(
                '升配操作不允许减少 CPU 或内存，请使用降配接口。' .
                '当前配置：' . $currentCpu . ' 核 / ' . $currentMem .
                '，请求配置：' . $newCpu . ' 核 / ' . $newMem
            );
        }

        // 热修改 — Incus 支持在线调整 limits.cpu 和 limits.memory
        return $this->client->request('PATCH', '/1.0/instances/' . $vmName . '?project=customers', [
            'config' => [
                'limits.cpu'    => (string) $newCpu,
                'limits.memory' => $newMem,
            ],
        ]);
    }

    /**
     * 降配（冷操作，需停机）
     *
     * 流程：停机 → 修改配置 → 启动
     *
     * @param int    $newCpu 新 CPU 核数
     * @param string $newMem 新内存大小（如 "2GB"）
     * @throws \InvalidArgumentException 参数无效
     * @throws \RuntimeException 操作失败
     */
    public function downgrade(string $vmName, int $newCpu, string $newMem): array
    {
        $this->validateCpuMem($newCpu, $newMem);

        // 获取当前配置，确认是降配
        $current = $this->getCurrentConfig($vmName);
        $currentCpu = (int) ($current['limits.cpu'] ?? 0);
        $currentMem = $current['limits.memory'] ?? '0';

        if ($newCpu >= $currentCpu && $this->parseMemoryBytes($newMem) >= $this->parseMemoryBytes($currentMem)) {
            throw new \InvalidArgumentException(
                '降配操作要求至少一项（CPU 或内存）减少，请使用升配接口。' .
                '当前配置：' . $currentCpu . ' 核 / ' . $currentMem .
                '，请求配置：' . $newCpu . ' 核 / ' . $newMem
            );
        }

        // 1. 停机
        $this->stopVm($vmName);

        // 2. 修改配置
        $result = $this->client->request('PATCH', '/1.0/instances/' . $vmName . '?project=customers', [
            'config' => [
                'limits.cpu'    => (string) $newCpu,
                'limits.memory' => $newMem,
            ],
        ]);

        // 3. 启动
        $this->startVm($vmName);

        return $result;
    }

    /**
     * 磁盘扩容（热操作，不停机）
     *
     * 仅允许扩大，禁止缩小
     *
     * @param string $newSize 新磁盘大小（如 "100GB"）
     * @throws \InvalidArgumentException 参数无效或试图缩小磁盘
     */
    public function expandDisk(string $vmName, string $newSize): array
    {
        $this->validateDiskSize($newSize);

        // 获取当前磁盘大小
        $instance = $this->client->request('GET', '/1.0/instances/' . $vmName . '?project=customers');
        $devices = $instance['metadata']['devices'] ?? [];
        $currentSize = $devices['root']['size'] ?? '0';

        $currentBytes = $this->parseDiskBytes($currentSize);
        $newBytes     = $this->parseDiskBytes($newSize);

        if ($newBytes <= $currentBytes) {
            throw new \InvalidArgumentException(
                '磁盘只允许扩容，不允许缩小。当前大小：' . $currentSize . '，请求大小：' . $newSize
            );
        }

        // 热扩容 — Incus 支持在线扩展 root 磁盘
        return $this->client->request('PATCH', '/1.0/instances/' . $vmName . '?project=customers', [
            'devices' => [
                'root' => [
                    'size' => $newSize,
                ],
            ],
        ]);
    }

    /**
     * 计算升降配补差价（按剩余天数折算）
     *
     * @param float  $oldMonthlyPrice 原套餐月价
     * @param float  $newMonthlyPrice 新套餐月价
     * @param string $expiryDate      到期日期（Y-m-d 格式）
     * @return float 需补差价金额（正数=需补缴，负数=应退款）
     */
    public function calculatePriceDifference(
        float $oldMonthlyPrice,
        float $newMonthlyPrice,
        string $expiryDate
    ): float {
        $now       = new \DateTimeImmutable('today');
        $expiry    = new \DateTimeImmutable($expiryDate);
        $remaining = $now->diff($expiry);

        if ($remaining->invert) {
            // 已过期
            return 0.0;
        }

        $remainingDays = $remaining->days;
        // 按 30 天/月计算日单价
        $dailyDiff = ($newMonthlyPrice - $oldMonthlyPrice) / 30.0;

        return round($dailyDiff * $remainingDays, 2);
    }

    /**
     * 获取 VM 当前配置
     */
    private function getCurrentConfig(string $vmName): array
    {
        $response = $this->client->request('GET', '/1.0/instances/' . $vmName . '?project=customers');

        return $response['metadata']['config'] ?? [];
    }

    /**
     * 停止 VM
     */
    private function stopVm(string $vmName): void
    {
        $this->client->request('PUT', '/1.0/instances/' . $vmName . '/state?project=customers', [
            'action'  => 'stop',
            'timeout' => 60,
            'force'   => false,
        ]);

        // 等待 VM 停止（轮询状态，最多 90 秒）
        $this->waitForState($vmName, 'stopped', 90);
    }

    /**
     * 启动 VM
     */
    private function startVm(string $vmName): void
    {
        $this->client->request('PUT', '/1.0/instances/' . $vmName . '/state?project=customers', [
            'action' => 'start',
        ]);

        $this->waitForState($vmName, 'running', 60);
    }

    /**
     * 等待 VM 达到指定状态
     *
     * @throws \RuntimeException 超时
     */
    private function waitForState(string $vmName, string $targetState, int $timeoutSeconds): void
    {
        $deadline = time() + $timeoutSeconds;

        while (time() < $deadline) {
            $response = $this->client->request(
                'GET',
                '/1.0/instances/' . $vmName . '/state?project=customers'
            );

            $status = strtolower($response['metadata']['status'] ?? '');
            if ($status === $targetState) {
                return;
            }

            sleep(2);
        }

        throw new \RuntimeException(
            'VM ' . $vmName . ' 未在 ' . $timeoutSeconds . ' 秒内达到 ' . $targetState . ' 状态'
        );
    }

    /**
     * 校验 CPU 和内存参数
     */
    private function validateCpuMem(int $cpu, string $mem): void
    {
        if ($cpu < 1 || $cpu > 64) {
            throw new \InvalidArgumentException('CPU 核数无效：' . $cpu . '（有效范围 1-64）');
        }

        $memBytes = $this->parseMemoryBytes($mem);
        if ($memBytes <= 0) {
            throw new \InvalidArgumentException('内存大小无效：' . $mem);
        }
    }

    /**
     * 校验磁盘大小参数
     */
    private function validateDiskSize(string $size): void
    {
        $bytes = $this->parseDiskBytes($size);
        if ($bytes <= 0) {
            throw new \InvalidArgumentException('磁盘大小无效：' . $size);
        }
    }

    /**
     * 解析内存大小字符串为字节数
     *
     * 支持格式：512MB, 1GB, 2GiB, 16384MB 等
     */
    private function parseMemoryBytes(string $size): int
    {
        if (preg_match('/^(\d+)\s*(GB|GiB|MB|MiB|KB|KiB|TB|TiB)$/i', trim($size), $m)) {
            $value = (int) $m[1];
            $unit  = strtoupper($m[2]);

            return match ($unit) {
                'TB', 'TIB' => $value * 1024 * 1024 * 1024 * 1024,
                'GB', 'GIB' => $value * 1024 * 1024 * 1024,
                'MB', 'MIB' => $value * 1024 * 1024,
                'KB', 'KIB' => $value * 1024,
                default      => 0,
            };
        }

        return 0;
    }

    /**
     * 解析磁盘大小字符串为字节数
     */
    private function parseDiskBytes(string $size): int
    {
        return $this->parseMemoryBytes($size);
    }
}
