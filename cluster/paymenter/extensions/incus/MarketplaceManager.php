<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\Log;
use Illuminate\Support\Facades\File;

/**
 * Marketplace 管理器
 *
 * 负责应用市场的应用列表、详情查询和一键部署功能。
 * 应用定义以 JSON 文件形式存储在 marketplace/ 目录中。
 */
class MarketplaceManager
{
    /**
     * marketplace 目录路径
     */
    protected string $marketplacePath;

    /**
     * Incus 客户端实例
     */
    protected IncusClient $client;

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
        $this->marketplacePath = __DIR__ . '/marketplace';
    }

    /**
     * 获取所有可用应用列表
     *
     * @return array 应用列表
     */
    public function listApps(): array
    {
        $apps = [];

        if (!File::isDirectory($this->marketplacePath)) {
            Log::warning('Marketplace 目录不存在: ' . $this->marketplacePath);
            return $apps;
        }

        $files = File::glob($this->marketplacePath . '/*.json');

        foreach ($files as $file) {
            try {
                $content = File::get($file);
                $app = json_decode($content, true, 512, JSON_THROW_ON_ERROR);
                $apps[] = $app;
            } catch (\JsonException $e) {
                Log::error("解析应用 JSON 文件失败: {$file}", ['error' => $e->getMessage()]);
            }
        }

        // 按名称排序
        usort($apps, fn($a, $b) => strcmp($a['name'] ?? '', $b['name'] ?? ''));

        return $apps;
    }

    /**
     * 获取单个应用详情
     *
     * @param string $appId 应用 ID
     * @return array|null 应用信息，不存在则返回 null
     */
    public function getApp(string $appId): ?array
    {
        $filePath = $this->marketplacePath . '/' . $appId . '.json';

        if (!File::exists($filePath)) {
            Log::warning("应用不存在: {$appId}");
            return null;
        }

        try {
            $content = File::get($filePath);
            return json_decode($content, true, 512, JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            Log::error("解析应用 JSON 失败: {$appId}", ['error' => $e->getMessage()]);
            return null;
        }
    }

    /**
     * 一键部署应用
     *
     * 根据应用 JSON 定义创建虚拟机并注入 cloud-init 初始化脚本。
     *
     * @param int    $userId  用户 ID
     * @param string $appId   应用 ID
     * @param array  $vmConfig 虚拟机配置覆盖（可选 cpu, memory, disk, name）
     * @return array 部署结果，包含实例名称和状态
     *
     * @throws \RuntimeException 应用不存在或部署失败时抛出
     */
    public function deployApp(int $userId, string $appId, array $vmConfig = []): array
    {
        // 获取应用定义
        $app = $this->getApp($appId);
        if (!$app) {
            throw new \RuntimeException("应用不存在: {$appId}");
        }

        // 合并配置：用户配置覆盖应用最低要求
        $cpu = max($vmConfig['cpu'] ?? $app['min_cpu'], $app['min_cpu']);
        $memory = $this->parseMemory($vmConfig['memory'] ?? $app['min_memory'], $app['min_memory']);
        $disk = $this->parseDisk($vmConfig['disk'] ?? $app['min_disk'], $app['min_disk']);

        // 生成实例名称
        $instanceName = $vmConfig['name'] ?? sprintf(
            'app-%s-%s-%s',
            $appId,
            $userId,
            substr(md5(uniqid()), 0, 6)
        );

        // 构建 cloud-init 用户数据
        $cloudInit = $this->buildCloudInit($app);

        // 创建虚拟机实例
        try {
            $result = $this->client->createInstance([
                'name'   => $instanceName,
                'type'   => 'virtual-machine',
                'source' => [
                    'type'  => 'image',
                    'alias' => $app['image'],
                ],
                'config' => [
                    'limits.cpu'           => (string) $cpu,
                    'limits.memory'        => $memory,
                    'user.user-data'       => $cloudInit,
                    'user.marketplace-app' => $appId,
                    'user.owner-id'        => (string) $userId,
                ],
                'devices' => [
                    'root' => [
                        'path' => '/',
                        'pool' => 'default',
                        'type' => 'disk',
                        'size' => $disk,
                    ],
                ],
            ]);

            // 启动实例
            $this->client->startInstance($instanceName);

            Log::info("应用部署成功", [
                'user_id'  => $userId,
                'app_id'   => $appId,
                'instance' => $instanceName,
            ]);

            return [
                'instance_name' => $instanceName,
                'app_id'        => $appId,
                'status'        => 'deploying',
                'config'        => [
                    'cpu'    => $cpu,
                    'memory' => $memory,
                    'disk'   => $disk,
                ],
                'ports' => $app['ports'] ?? [],
            ];
        } catch (\Exception $e) {
            Log::error("应用部署失败", [
                'user_id' => $userId,
                'app_id'  => $appId,
                'error'   => $e->getMessage(),
            ]);
            throw new \RuntimeException("部署失败: {$e->getMessage()}", 0, $e);
        }
    }

    /**
     * 构建 cloud-init 用户数据
     *
     * @param array $app 应用定义
     * @return string cloud-init YAML
     */
    protected function buildCloudInit(array $app): string
    {
        $script = $app['cloud_init'] ?? '';

        return "#cloud-config\npackage_update: true\npackage_upgrade: true\n\nruncmd:\n" .
            collect(explode("\n", trim($script)))
                ->filter()
                ->map(fn($line) => "  - " . $line)
                ->implode("\n") .
            "\n";
    }

    /**
     * 解析内存配置，确保不低于最小要求
     */
    protected function parseMemory(string $requested, string $minimum): string
    {
        $reqMb = $this->toMegabytes($requested);
        $minMb = $this->toMegabytes($minimum);

        return max($reqMb, $minMb) . 'MiB';
    }

    /**
     * 解析磁盘配置，确保不低于最小要求
     */
    protected function parseDisk(string $requested, string $minimum): string
    {
        $reqGb = $this->toGigabytes($requested);
        $minGb = $this->toGigabytes($minimum);

        return max($reqGb, $minGb) . 'GiB';
    }

    /**
     * 将内存字符串转换为 MB
     */
    protected function toMegabytes(string $value): int
    {
        if (preg_match('/^(\d+)\s*(GiB|GB|G)$/i', $value, $m)) {
            return (int) $m[1] * 1024;
        }
        if (preg_match('/^(\d+)\s*(MiB|MB|M)?$/i', $value, $m)) {
            return (int) $m[1];
        }
        return (int) $value;
    }

    /**
     * 将磁盘字符串转换为 GB
     */
    protected function toGigabytes(string $value): int
    {
        if (preg_match('/^(\d+)\s*(TiB|TB|T)$/i', $value, $m)) {
            return (int) $m[1] * 1024;
        }
        if (preg_match('/^(\d+)\s*(GiB|GB|G)?$/i', $value, $m)) {
            return (int) $m[1];
        }
        return (int) $value;
    }
}
