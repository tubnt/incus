<?php

namespace Extensions\Incus;

use Illuminate\Support\Facades\Redis;
use Illuminate\Support\Facades\Log;

/**
 * 频率限制器 — 基于 Redis 的滑动窗口
 *
 * 用途：
 * - 用户 Web 请求防刷（每分钟 60 次）
 * - Extension API 调用限制（每分钟创建 VM ≤ 2）
 * - 可配置的通用限流器
 */
class RateLimiter
{
    /** @var string Redis key 前缀 */
    private const KEY_PREFIX = 'ratelimit:';

    // 预定义限流规则
    private const RULES = [
        // 用户 Web 请求通用限流
        'web_request' => [
            'max_requests' => 60,
            'window'       => 60, // 秒
        ],
        // VM 创建操作限流
        'vm_create' => [
            'max_requests' => 2,
            'window'       => 60,
        ],
        // VM 操作（重启/重装等）
        'vm_action' => [
            'max_requests' => 10,
            'window'       => 60,
        ],
        // 登录尝试
        'login_attempt' => [
            'max_requests' => 5,
            'window'       => 300, // 5 分钟
        ],
        // API 通用调用
        'api_general' => [
            'max_requests' => 120,
            'window'       => 60,
        ],
    ];

    /**
     * 检查请求是否被允许（滑动窗口算法）
     *
     * 使用 Redis Sorted Set 实现精确的滑动窗口：
     * - 每个请求记录为 member=唯一ID, score=时间戳
     * - 清理窗口外的旧记录
     * - 统计窗口内的请求数
     *
     * @param string $identifier 标识符（如 user_id, ip 等）
     * @param string $rule       规则名称（对应 RULES 常量）
     * @return array{allowed: bool, remaining: int, retry_after: int|null}
     */
    public function check(string $identifier, string $rule): array
    {
        $config = self::RULES[$rule] ?? null;
        if (!$config) {
            // fail-closed：未知规则默认拒绝，防止拼写错误导致限流失效
            Log::error('[限流] 未知规则，默认拒绝', ['rule' => $rule]);
            return ['allowed' => false, 'remaining' => 0, 'retry_after' => 60, 'limit' => 0, 'window' => 60];
        }

        return $this->checkWithParams($identifier, $rule, $config['max_requests'], $config['window']);
    }

    /**
     * 自定义参数检查
     *
     * @param string $identifier  标识符
     * @param string $rule        规则名称（用于 key 命名）
     * @param int    $maxRequests 窗口内最大请求数
     * @param int    $window      窗口大小（秒）
     * @return array{allowed: bool, remaining: int, retry_after: int|null}
     */
    /**
     * Lua 脚本：原子滑动窗口限流
     *
     * KEYS[1] = sorted set key
     * ARGV[1] = window_start, ARGV[2] = now, ARGV[3] = member, ARGV[4] = max_requests, ARGV[5] = expire_ttl
     *
     * 返回: {current_count, oldest_score}
     */
    private const LUA_SLIDING_WINDOW = <<<'LUA'
redis.call('zremrangebyscore', KEYS[1], '-inf', ARGV[1])
local count = redis.call('zcard', KEYS[1])
if count < tonumber(ARGV[4]) then
    redis.call('zadd', KEYS[1], ARGV[2], ARGV[3])
    count = count + 1
end
redis.call('expire', KEYS[1], tonumber(ARGV[5]))
local oldest = redis.call('zrange', KEYS[1], 0, 0, 'WITHSCORES')
local oldest_score = 0
if #oldest >= 2 then oldest_score = tonumber(oldest[2]) end
return {count, tostring(oldest_score)}
LUA;

    public function checkWithParams(string $identifier, string $rule, int $maxRequests, int $window): array
    {
        $key = self::KEY_PREFIX . $rule . ':' . $identifier;
        $now = microtime(true);
        $windowStart = $now - $window;
        $member = $now . ':' . bin2hex(random_bytes(4));

        // 原子 Lua 脚本：清理过期 → 检查计数 → 仅在未超限时添加
        $results = Redis::eval(
            self::LUA_SLIDING_WINDOW,
            1,
            $key,
            (string) $windowStart,
            (string) $now,
            $member,
            (string) $maxRequests,
            (string) ($window + 10)
        );

        $currentCount = (int) ($results[0] ?? 0);
        $oldestScore = (float) ($results[1] ?? 0);
        $allowed = $currentCount <= $maxRequests;

        $retryAfter = null;
        if (!$allowed) {
            $retryAfter = $oldestScore > 0
                ? (int) ceil($oldestScore + $window - $now)
                : $window;

            Log::info('[限流] 请求被限制', [
                'identifier' => $identifier,
                'rule'       => $rule,
                'count'      => $currentCount,
                'limit'      => $maxRequests,
            ]);
        }

        return [
            'allowed'     => $allowed,
            'remaining'   => max(0, $maxRequests - $currentCount),
            'retry_after' => $retryAfter,
            'limit'       => $maxRequests,
            'window'      => $window,
        ];
    }

    /**
     * 中间件入口 — 检查 Web 请求频率
     *
     * 用于 Laravel 中间件集成：
     *   Route::middleware('throttle.incus:web_request')->group(...)
     *
     * @param mixed    $request Laravel Request 对象
     * @param \Closure $next
     * @param string   $rule    限流规则名称
     * @return mixed
     */
    public function handleRequest($request, \Closure $next, string $rule = 'web_request')
    {
        $identifier = $this->resolveIdentifier($request);
        $result = $this->check($identifier, $rule);

        if (!$result['allowed']) {
            return response()->json([
                'error'       => '请求过于频繁，请稍后再试',
                'retry_after' => $result['retry_after'],
            ], 429, [
                'X-RateLimit-Limit'     => $result['limit'],
                'X-RateLimit-Remaining' => 0,
                'Retry-After'           => $result['retry_after'],
            ]);
        }

        $response = $next($request);

        // 添加限流 header
        if (method_exists($response, 'header')) {
            $response->header('X-RateLimit-Limit', $result['limit']);
            $response->header('X-RateLimit-Remaining', $result['remaining']);
        }

        return $response;
    }

    /**
     * 检查 Extension API 调用频率
     *
     * 用于 Extension 内部调用前检查：
     *   if (!$rateLimiter->checkApiCall($userId, 'vm_create')) { throw ... }
     *
     * @param int    $userId 用户 ID
     * @param string $action 操作类型
     * @return bool 是否允许
     * @throws \RuntimeException 被限流时抛出
     */
    public function checkApiCall(int $userId, string $action): bool
    {
        $result = $this->check("user:{$userId}", $action);

        if (!$result['allowed']) {
            throw new \RuntimeException(
                "操作过于频繁（{$action}），请在 {$result['retry_after']} 秒后重试"
            );
        }

        return true;
    }

    /**
     * 获取指定标识符的当前使用状态
     *
     * @param string $identifier
     * @param string $rule
     * @return array{current: int, limit: int, remaining: int, window: int}
     */
    public function getUsage(string $identifier, string $rule): array
    {
        $config = self::RULES[$rule] ?? ['max_requests' => 0, 'window' => 60];
        $key = self::KEY_PREFIX . $rule . ':' . $identifier;
        $now = microtime(true);

        // 清理过期记录后统计
        Redis::zremrangebyscore($key, '-inf', $now - $config['window']);
        $current = (int) Redis::zcard($key);

        return [
            'current'   => $current,
            'limit'     => $config['max_requests'],
            'remaining' => max(0, $config['max_requests'] - $current),
            'window'    => $config['window'],
        ];
    }

    /**
     * 重置指定标识符的限流计数
     *
     * @param string $identifier
     * @param string $rule
     */
    public function reset(string $identifier, string $rule): void
    {
        $key = self::KEY_PREFIX . $rule . ':' . $identifier;
        Redis::del($key);
    }

    /**
     * 从请求中解析标识符
     *
     * 优先使用认证用户 ID，其次使用 IP 地址
     */
    private function resolveIdentifier($request): string
    {
        if ($request->user()) {
            return 'user:' . $request->user()->id;
        }

        return 'ip:' . $request->ip();
    }
}
