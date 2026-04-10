<?php

namespace Extensions\Incus;

use Illuminate\Support\Facades\Cache;
use RuntimeException;

/**
 * VM 操作并发锁
 *
 * 基于 Cache（Redis/数据库）实现 per-VM 粒度的互斥锁，
 * 防止同一 VM 同时执行多个操作（创建/迁移/重装等）。
 *
 * 锁超时 5 分钟自动释放，防止死锁。
 */
class VmOperationLock
{
    private const KEY_PREFIX = 'vm_lock:';
    private const DEFAULT_TTL = 300; // 5 分钟

    /**
     * 获取 VM 操作锁
     *
     * @param string $vmName   VM 名称
     * @param string $operation 当前操作名称（用于诊断）
     * @param int $ttl          锁超时秒数
     * @return string 锁 owner 标识（用于安全释放）
     * @throws RuntimeException 获取锁失败时抛出
     */
    public static function acquire(string $vmName, string $operation, int $ttl = self::DEFAULT_TTL): string
    {
        $key = self::KEY_PREFIX . $vmName;
        $owner = bin2hex(random_bytes(16));

        $lock = Cache::lock($key, $ttl, $owner);

        if (!$lock->get()) {
            $existing = Cache::get($key . ':info');
            $runningOp = $existing['operation'] ?? '未知';
            throw new RuntimeException(
                "VM [{$vmName}] 正在执行操作 [{$runningOp}]，无法同时执行 [{$operation}]"
            );
        }

        // 存储锁信息用于诊断
        Cache::put($key . ':info', [
            'operation' => $operation,
            'owner'     => $owner,
            'locked_at' => now()->toIso8601String(),
        ], $ttl);

        return $owner;
    }

    /**
     * 释放 VM 操作锁（验证 owner，防止释放他人持有的锁）
     *
     * @param string $vmName VM 名称
     * @param string $owner  acquire() 返回的 owner 标识
     */
    public static function release(string $vmName, string $owner): void
    {
        $key = self::KEY_PREFIX . $vmName;

        $info = Cache::get($key . ':info');
        if ($info && ($info['owner'] ?? '') !== $owner) {
            // owner 不匹配 — 锁已因 TTL 过期后被其他操作获取，不可释放
            return;
        }

        Cache::lock($key, 0, $owner)->release();
        Cache::forget($key . ':info');
    }

    /**
     * 在锁保护下执行操作
     *
     * @param string $vmName    VM 名称
     * @param string $operation 操作名称
     * @param callable $callback 执行体
     * @param int $ttl           锁超时秒数
     * @return mixed callback 返回值
     * @throws RuntimeException 获取锁失败时抛出
     */
    public static function withLock(string $vmName, string $operation, callable $callback, int $ttl = self::DEFAULT_TTL): mixed
    {
        $owner = self::acquire($vmName, $operation, $ttl);

        try {
            return $callback();
        } finally {
            self::release($vmName, $owner);
        }
    }

    /**
     * 查询 VM 是否被锁定
     *
     * @param string $vmName VM 名称
     * @return array|null 锁信息（operation, locked_at）或 null
     */
    public static function getInfo(string $vmName): ?array
    {
        return Cache::get(self::KEY_PREFIX . $vmName . ':info');
    }
}
