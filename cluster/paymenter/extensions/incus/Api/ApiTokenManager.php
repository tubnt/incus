<?php

namespace Extensions\Incus\Api;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;

class ApiTokenManager
{
    /** 每分钟请求上限 */
    private const RATE_LIMIT = 120;

    /** 有效权限类型 */
    private const VALID_PERMISSIONS = ['read-only', 'full-access', 'custom'];

    /** custom 模式下可选权限 */
    private const CUSTOM_PERMISSION_SCOPES = [
        'instances.list',
        'instances.read',
        'instances.actions',
        'snapshots.list',
        'snapshots.create',
        'firewall.read',
        'firewall.write',
        'metrics.read',
        'account.read',
        'tokens.manage',
    ];

    /**
     * 创建 API Token
     *
     * @return array{token: string, id: int} 返回明文 token（仅此一次）和记录 ID
     */
    /** Token 最长有效期（天） */
    private const MAX_EXPIRY_DAYS = 365;

    /** Token 默认有效期（天），null 表示不过期 */
    private const DEFAULT_EXPIRY_DAYS = 90;

    /**
     * 创建 API Token
     *
     * @param int|null $expiryDays 有效天数，null 使用默认值，0 表示永不过期
     * @return array{token: string, id: int} 返回明文 token（仅此一次）和记录 ID
     */
    public function createToken(int $userId, string $name, string $permission = 'read-only', ?array $customPermissions = null, ?int $expiryDays = null): array
    {
        if (!in_array($permission, self::VALID_PERMISSIONS, true)) {
            throw new \InvalidArgumentException("无效权限类型: {$permission}");
        }

        if ($permission === 'custom') {
            if (empty($customPermissions)) {
                throw new \InvalidArgumentException('custom 权限模式必须指定具体权限列表');
            }
            $invalid = array_diff($customPermissions, self::CUSTOM_PERMISSION_SCOPES);
            if (!empty($invalid)) {
                throw new \InvalidArgumentException('无效权限范围: ' . implode(', ', $invalid));
            }
        }

        // 计算过期时间
        $days = $expiryDays ?? self::DEFAULT_EXPIRY_DAYS;
        if ($days < 0 || $days > self::MAX_EXPIRY_DAYS) {
            throw new \InvalidArgumentException("有效天数必须在 0-" . self::MAX_EXPIRY_DAYS . " 之间（0 = 永不过期）");
        }
        $expiresAt = $days > 0 ? now()->addDays($days) : null;

        // 限制每用户最多 20 个 token
        $count = DB::table('incus_api_tokens')->where('user_id', $userId)->count();
        if ($count >= 20) {
            throw new \RuntimeException('每个用户最多创建 20 个 API Token');
        }

        $plainToken = 'incus_' . Str::random(48);
        $tokenHash = hash('sha256', $plainToken);
        $tokenPrefix = substr($plainToken, 0, 14); // "incus_" + 8 字符

        $id = DB::table('incus_api_tokens')->insertGetId([
            'user_id'            => $userId,
            'name'               => $name,
            'token_hash'         => $tokenHash,
            'token_prefix'       => $tokenPrefix,
            'permission'         => $permission,
            'custom_permissions' => $permission === 'custom' ? json_encode($customPermissions) : null,
            'expires_at'         => $expiresAt,
            'created_at'         => now(),
            'updated_at'         => now(),
        ]);

        return ['token' => $plainToken, 'id' => $id];
    }

    /**
     * 吊销 Token
     */
    public function revokeToken(int $tokenId, ?int $userId = null): bool
    {
        $query = DB::table('incus_api_tokens')->where('id', $tokenId);
        if ($userId !== null) {
            $query->where('user_id', $userId);
        }

        return $query->delete() > 0;
    }

    /**
     * 列出用户所有 Token（不含哈希）
     */
    public function listTokens(int $userId): array
    {
        return DB::table('incus_api_tokens')
            ->where('user_id', $userId)
            ->select(['id', 'name', 'token_prefix', 'permission', 'custom_permissions', 'last_used_at', 'expires_at', 'created_at'])
            ->orderByDesc('created_at')
            ->get()
            ->map(function ($token) {
                $token->custom_permissions = $token->custom_permissions
                    ? json_decode($token->custom_permissions, true)
                    : null;
                return $token;
            })
            ->toArray();
    }

    /**
     * 验证 Bearer token，返回 token 记录或 null
     */
    public function validateToken(string $plainToken): ?object
    {
        $tokenHash = hash('sha256', $plainToken);

        $token = DB::table('incus_api_tokens')
            ->where('token_hash', $tokenHash)
            ->first();

        if (!$token) {
            return null;
        }

        // 检查过期
        if ($token->expires_at && now()->greaterThan($token->expires_at)) {
            return null;
        }

        // 更新最后使用时间
        DB::table('incus_api_tokens')
            ->where('id', $token->id)
            ->update(['last_used_at' => now()]);

        $token->custom_permissions = $token->custom_permissions
            ? json_decode($token->custom_permissions, true)
            : null;

        return $token;
    }

    /**
     * 检查 token 是否拥有指定权限
     */
    public function hasPermission(object $token, string $scope): bool
    {
        if ($token->permission === 'full-access') {
            return true;
        }

        if ($token->permission === 'read-only') {
            return str_ends_with($scope, '.list') || str_ends_with($scope, '.read');
        }

        // custom
        return is_array($token->custom_permissions) && in_array($scope, $token->custom_permissions, true);
    }

    /**
     * Rate limiting 检查，返回 true 表示允许
     */
    public function checkRateLimit(int $userId): bool
    {
        $windowStart = now()->startOfMinute();

        // upsert 防止并发请求竞态条件绕过限速
        DB::statement(
            'INSERT INTO incus_api_rate_limits (user_id, request_count, window_start) VALUES (?, 1, ?)
             ON DUPLICATE KEY UPDATE request_count = request_count + 1',
            [$userId, $windowStart]
        );

        $count = DB::table('incus_api_rate_limits')
            ->where('user_id', $userId)
            ->where('window_start', $windowStart)
            ->value('request_count');

        if ($count > self::RATE_LIMIT) {
            return false;
        }

        return true;
    }

    /**
     * 清理过期的 rate limit 记录（Cron 调用）
     */
    public function cleanupRateLimits(): int
    {
        return DB::table('incus_api_rate_limits')
            ->where('window_start', '<', now()->subMinutes(5))
            ->delete();
    }

    /**
     * 获取可用的权限范围列表
     */
    public static function getAvailableScopes(): array
    {
        return self::CUSTOM_PERMISSION_SCOPES;
    }
}
