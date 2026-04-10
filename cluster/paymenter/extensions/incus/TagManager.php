<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * VM 标签管理
 *
 * 标签双写：Incus config（user.tags）+ 本地数据库，
 * 用于资源分组、筛选和批量操作。
 */
class TagManager
{
    private IncusClient $client;

    /** 标签名称最大长度 */
    private const TAG_MAX_LENGTH = 64;

    /** 单个 VM 最大标签数 */
    private const MAX_TAGS_PER_VM = 20;

    /** 标签名称合法字符 */
    private const TAG_PATTERN = '/^[a-zA-Z0-9\x{4e00}-\x{9fff}_\-\.]+$/u';

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    /**
     * 添加标签
     *
     * @param string $vmName VM 实例名称
     * @param string $tag 标签名称
     * @param int|null $userId 所属用户 ID
     * @throws \InvalidArgumentException
     * @throws \RuntimeException
     */
    public function addTag(string $vmName, string $tag, ?int $userId = null): void
    {
        $this->validateTag($tag);

        $currentTags = $this->listTags($vmName);

        if (in_array($tag, $currentTags, true)) {
            return; // 已存在，幂等操作
        }

        if (count($currentTags) >= self::MAX_TAGS_PER_VM) {
            throw new \RuntimeException(
                "VM {$vmName} 标签数已达上限（" . self::MAX_TAGS_PER_VM . "）"
            );
        }

        // 先写 Incus（如果失败则不写 DB，保持一致性）
        $currentTags[] = $tag;
        $this->syncTagsToIncus($vmName, $currentTags);

        // Incus 写入成功后再写数据库
        DB::transaction(function () use ($vmName, $tag, $userId) {
            DB::table('vm_tags')->insert([
                'vm_name' => $vmName,
                'tag' => $tag,
                'user_id' => $userId,
                'created_at' => now(),
            ]);
        });

        Log::info("VM {$vmName} 添加标签：{$tag}");
    }

    /**
     * 移除标签
     */
    public function removeTag(string $vmName, string $tag): void
    {
        $currentTags = $this->listTags($vmName);
        $newTags = array_values(array_filter($currentTags, fn($t) => $t !== $tag));

        // 更新 Incus config
        $this->syncTagsToIncus($vmName, $newTags);

        // 从本地数据库删除
        DB::table('vm_tags')
            ->where('vm_name', $vmName)
            ->where('tag', $tag)
            ->delete();

        Log::info("VM {$vmName} 移除标签：{$tag}");
    }

    /**
     * 获取 VM 的所有标签
     *
     * @return string[]
     */
    public function listTags(string $vmName): array
    {
        $instance = $this->client->request('GET', "/1.0/instances/{$vmName}");
        $tagsString = $instance['metadata']['config']['user.tags'] ?? '';

        if ($tagsString === '') {
            return [];
        }

        return array_filter(explode(',', $tagsString));
    }

    /**
     * 按标签筛选用户的 VM 列表
     *
     * @param int $userId 用户 ID
     * @param string $tag 标签名称
     * @return array VM 名称列表
     */
    public function listVmsByTag(int $userId, string $tag): array
    {
        return DB::table('vm_tags')
            ->where('user_id', $userId)
            ->where('tag', $tag)
            ->pluck('vm_name')
            ->unique()
            ->values()
            ->toArray();
    }

    /**
     * 获取用户所有标签（去重）
     *
     * @return string[]
     */
    public function listUserTags(int $userId): array
    {
        return DB::table('vm_tags')
            ->where('user_id', $userId)
            ->distinct()
            ->pluck('tag')
            ->toArray();
    }

    /**
     * 批量设置标签（替换 VM 的所有标签）
     *
     * @param string $vmName VM 实例名称
     * @param string[] $tags 新标签列表
     * @param int|null $userId 所属用户 ID
     */
    public function setTags(string $vmName, array $tags, ?int $userId = null): void
    {
        foreach ($tags as $tag) {
            $this->validateTag($tag);
        }

        $tags = array_unique($tags);

        if (count($tags) > self::MAX_TAGS_PER_VM) {
            throw new \RuntimeException(
                "标签数超出上限（" . self::MAX_TAGS_PER_VM . "）"
            );
        }

        // 先写 Incus（失败则不动 DB）
        $this->syncTagsToIncus($vmName, $tags);

        // Incus 成功后事务更新数据库
        DB::transaction(function () use ($vmName, $tags, $userId) {
            DB::table('vm_tags')->where('vm_name', $vmName)->delete();

            if (!empty($tags)) {
                $rows = array_map(fn($tag) => [
                    'vm_name' => $vmName,
                    'tag' => $tag,
                    'user_id' => $userId,
                    'created_at' => now(),
                ], $tags);

                DB::table('vm_tags')->insert($rows);
            }
        });

        Log::info("VM {$vmName} 标签已更新为：" . implode(', ', $tags));
    }

    /**
     * 统计标签使用次数
     *
     * @param int $userId 用户 ID
     * @return array<string, int> tag => count
     */
    public function getTagCounts(int $userId): array
    {
        return DB::table('vm_tags')
            ->where('user_id', $userId)
            ->select('tag', DB::raw('COUNT(DISTINCT vm_name) as count'))
            ->groupBy('tag')
            ->pluck('count', 'tag')
            ->toArray();
    }

    private function validateTag(string $tag): void
    {
        if ($tag === '') {
            throw new \InvalidArgumentException('标签名称不能为空');
        }

        if (mb_strlen($tag) > self::TAG_MAX_LENGTH) {
            throw new \InvalidArgumentException(
                "标签名称不能超过 " . self::TAG_MAX_LENGTH . " 个字符"
            );
        }

        if (!preg_match(self::TAG_PATTERN, $tag)) {
            throw new \InvalidArgumentException(
                '标签名称只能包含字母、数字、中文、下划线、连字符和点号'
            );
        }
    }

    /**
     * 同步标签到 Incus config（user.tags）
     */
    /**
     * 同步标签到 Incus config（user.tags），使用 ETag 乐观锁防止竞态覆盖
     */
    private function syncTagsToIncus(string $vmName, array $tags): void
    {
        $instance = $this->client->request('GET', "/1.0/instances/{$vmName}");
        $etag = $instance['etag'] ?? '';
        $config = $instance['metadata']['config'] ?? [];
        $config['user.tags'] = implode(',', $tags);

        $headers = [];
        if ($etag !== '') {
            $headers['If-Match'] = $etag;
        }

        $this->client->request('PATCH', "/1.0/instances/{$vmName}", [
            'config' => $config,
        ], $headers);
    }
}
