<?php

namespace App\Extensions\Incus;

/**
 * 附加磁盘管理 — 热添加/移除 Ceph 卷
 *
 * 注意：VM 内需手动格式化挂载新磁盘（/dev/sdb 等）
 */
class DiskManager
{
    private IncusClient $client;
    private string $storagePool;

    /** Incus 实例/设备名允许的字符：字母、数字、连字符 */
    private const NAME_PATTERN = '/^[a-zA-Z0-9][a-zA-Z0-9\-]{0,62}$/';

    public function __construct(IncusClient $client, string $storagePool = 'ceph-pool')
    {
        $this->client = $client;
        $this->storagePool = $storagePool;
    }

    /**
     * 验证实例/设备名称格式，防止路径注入
     */
    private function validateName(string $name, string $label): void
    {
        if (!preg_match(self::NAME_PATTERN, $name)) {
            throw new \InvalidArgumentException("{$label} 格式无效，仅允许字母、数字和连字符: '{$name}'");
        }
    }

    /**
     * 热添加 Ceph 卷到 VM
     *
     * @param string $vmName  VM 实例名
     * @param int    $size    磁盘大小（GiB）
     * @param string $diskName 磁盘设备名（可选，默认自动生成）
     * @return array{disk_name: string, volume: string, message: string}
     * @throws \RuntimeException
     */
    public function addDisk(string $vmName, int $size, string $diskName = ''): array
    {
        $this->validateName($vmName, 'VM 名称');
        if ($diskName !== '') {
            $this->validateName($diskName, '磁盘设备名');
        }

        if ($size < 1 || $size > 2000) {
            throw new \InvalidArgumentException('磁盘大小必须在 1-2000 GiB 之间');
        }

        $volumeName = sprintf('vol-%s-%s', $vmName, bin2hex(random_bytes(4)));
        if ($diskName === '') {
            $diskName = sprintf('data-disk-%s', bin2hex(random_bytes(4)));
        }

        // 1. 创建 Ceph 存储卷
        $this->client->request('POST', "/1.0/storage-pools/{$this->storagePool}/volumes", [
            'name' => $volumeName,
            'type' => 'custom',
            'config' => [
                'size' => "{$size}GiB",
            ],
        ]);

        // 2. 将卷挂载到 VM 作为磁盘设备
        try {
            $instance = $this->client->request('GET', "/1.0/instances/{$vmName}");
            $devices = $instance['metadata']['devices'] ?? [];
            $devices[$diskName] = [
                'type'   => 'disk',
                'pool'   => $this->storagePool,
                'source' => $volumeName,
            ];

            $this->client->request('PATCH', "/1.0/instances/{$vmName}", [
                'devices' => $devices,
            ]);
        } catch (\Exception $e) {
            // 挂载失败时清理已创建的卷
            $this->client->request('DELETE',
                "/1.0/storage-pools/{$this->storagePool}/volumes/custom/{$volumeName}"
            );
            throw new \RuntimeException("挂载磁盘失败，已回滚卷创建: " . $e->getMessage());
        }

        return [
            'disk_name' => $diskName,
            'volume'    => $volumeName,
            'message'   => "磁盘已添加。请在 VM 内手动格式化并挂载新磁盘（通常为 /dev/sdb 或下一个可用设备）。\n"
                         . "示例：mkfs.ext4 /dev/sdb && mkdir /data && mount /dev/sdb /data\n"
                         . "如需开机自动挂载，请将条目添加到 /etc/fstab。",
        ];
    }

    /**
     * 移除磁盘设备并删除对应的 Ceph 卷
     *
     * @param string $vmName   VM 实例名
     * @param string $diskName 磁盘设备名
     * @throws \RuntimeException
     */
    public function removeDisk(string $vmName, string $diskName): void
    {
        $this->validateName($vmName, 'VM 名称');
        $this->validateName($diskName, '磁盘设备名');

        // 1. 获取实例配置，找到对应的卷名
        $instance = $this->client->request('GET', "/1.0/instances/{$vmName}");
        $devices = $instance['metadata']['devices'] ?? [];

        if (!isset($devices[$diskName])) {
            throw new \RuntimeException("磁盘设备 '{$diskName}' 不存在");
        }

        $device = $devices[$diskName];
        if ($device['type'] !== 'disk') {
            throw new \RuntimeException("设备 '{$diskName}' 不是磁盘类型");
        }

        $volumeName = $device['source'] ?? '';
        $pool = $device['pool'] ?? $this->storagePool;

        // 2. 从实例中移除设备
        unset($devices[$diskName]);
        $this->client->request('PATCH', "/1.0/instances/{$vmName}", [
            'devices' => $devices,
        ]);

        // 3. 删除存储卷（设备已分离，卷删除失败时记录错误避免孤儿卷静默丢失）
        if ($volumeName !== '') {
            try {
                $this->client->request('DELETE',
                    "/1.0/storage-pools/{$pool}/volumes/custom/{$volumeName}"
                );
            } catch (\Exception $e) {
                \Log::error("磁盘设备已分离但卷删除失败，可能产生孤儿卷: pool={$pool}, volume={$volumeName}: " . $e->getMessage());
                throw new \RuntimeException("磁盘设备已移除，但存储卷 '{$volumeName}' 删除失败，请手动清理: " . $e->getMessage());
            }
        }
    }

    /**
     * 列出 VM 的所有磁盘设备
     *
     * @param string $vmName VM 实例名
     * @return array<string, array{pool: string, source: string, size: string}>
     */
    public function listDisks(string $vmName): array
    {
        $this->validateName($vmName, 'VM 名称');
        $instance = $this->client->request('GET', "/1.0/instances/{$vmName}");
        $devices = $instance['metadata']['devices'] ?? [];
        $disks = [];

        foreach ($devices as $name => $device) {
            if ($device['type'] !== 'disk') {
                continue;
            }
            // 跳过根磁盘
            if ($name === 'root') {
                continue;
            }

            $pool = $device['pool'] ?? $this->storagePool;
            $source = $device['source'] ?? '';
            $size = '';

            // 查询卷大小
            if ($source !== '') {
                try {
                    $volume = $this->client->request('GET',
                        "/1.0/storage-pools/{$pool}/volumes/custom/{$source}"
                    );
                    $size = $volume['metadata']['config']['size'] ?? '';
                } catch (\Exception $e) {
                    $size = '未知';
                }
            }

            $disks[$name] = [
                'pool'   => $pool,
                'source' => $source,
                'size'   => $size,
            ];
        }

        return $disks;
    }
}
