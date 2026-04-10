<?php

namespace App\Extensions\Incus;

/**
 * 快照管理器
 *
 * 功能：创建、恢复、删除、列出快照
 * 限制：每个 VM 最多 5 个快照
 * 恢复操作需要 VM 处于停机状态
 */
class SnapshotManager
{
    /** 每个 VM 最大快照数 */
    private const MAX_SNAPSHOTS_PER_VM = 5;

    private IncusClient $client;

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    /**
     * 列出 VM 的所有快照
     *
     * @return array 快照列表（含名称、创建时间、是否有状态）
     */
    public function listSnapshots(string $vmName): array
    {
        $response = $this->client->request(
            'GET',
            '/1.0/instances/' . $vmName . '/snapshots?project=customers&recursion=1'
        );

        $snapshots = $response['metadata'] ?? [];

        return array_map(function (array $snap) {
            return [
                'name'       => $snap['name'],
                'created_at' => $snap['created_at'] ?? null,
                'stateful'   => $snap['stateful'] ?? false,
                'size'       => $snap['size'] ?? null,
            ];
        }, $snapshots);
    }

    /**
     * 创建快照
     *
     * @param string|null $snapName 快照名称，为 null 时自动生成
     * @throws \OverflowException 超过快照数量限制
     */
    public function createSnapshot(string $vmName, ?string $snapName = null): array
    {
        // 检查快照数量限制
        $existing = $this->listSnapshots($vmName);
        if (count($existing) >= self::MAX_SNAPSHOTS_PER_VM) {
            throw new \OverflowException(
                '快照数量已达上限（' . self::MAX_SNAPSHOTS_PER_VM . ' 个），请删除旧快照后再创建'
            );
        }

        // 自动生成快照名称：snap-{YmdHis}
        if ($snapName === null) {
            $snapName = 'snap-' . date('YmdHis');
        }

        return $this->client->request(
            'POST',
            '/1.0/instances/' . $vmName . '/snapshots?project=customers',
            [
                'name'     => $snapName,
                'stateful' => false,
            ]
        );
    }

    /**
     * 恢复快照
     *
     * 要求：VM 必须处于停机（Stopped）状态才能恢复
     *
     * @throws \RuntimeException VM 未停机
     */
    public function restoreSnapshot(string $vmName, string $snapName): array
    {
        // 检查 VM 状态 — 必须已停机
        $this->ensureVmStopped($vmName);

        // PUT /1.0/instances/{name} 并设置 restore 字段
        return $this->client->request(
            'PUT',
            '/1.0/instances/' . $vmName . '?project=customers',
            [
                'restore' => $snapName,
            ]
        );
    }

    /**
     * 删除快照
     */
    public function deleteSnapshot(string $vmName, string $snapName): array
    {
        return $this->client->request(
            'DELETE',
            '/1.0/instances/' . $vmName . '/snapshots/' . $snapName . '?project=customers'
        );
    }

    /**
     * 确认 VM 处于停机状态
     *
     * @throws \RuntimeException VM 未停机
     */
    private function ensureVmStopped(string $vmName): void
    {
        $response = $this->client->request(
            'GET',
            '/1.0/instances/' . $vmName . '/state?project=customers'
        );

        $status = $response['metadata']['status'] ?? 'Unknown';

        if (strtolower($status) !== 'stopped') {
            throw new \RuntimeException(
                '恢复快照需要先停止虚拟机（当前状态：' . $status . '）。请先关机后再恢复。'
            );
        }
    }
}
