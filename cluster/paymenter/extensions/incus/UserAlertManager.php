<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Log;

/**
 * 用户级告警管理器
 *
 * 允许用户为自己的 VM 设置阈值告警，支持内存 / 磁盘等指标。
 * 防 spam 机制：同一告警 1 小时内不重复通知。
 */
class UserAlertManager
{
    /**
     * 支持的监控指标
     *
     * 不支持的指标及原因：
     * - cpu_percent：Incus API 仅返回累计纳秒，需两次采样计算差值
     * - bandwidth_in / bandwidth_out：Incus API 返回累计字节计数器非速率，
     *   阈值比较无意义（只增不减）。需 Prometheus rate() 或本地差值计算，
     *   后续可通过查询 Prometheus 实现带宽速率告警。
     */
    private const VALID_METRICS = [
        'memory_percent',
        'disk_percent',
    ];

    /** 支持的告警方向 */
    private const VALID_DIRECTIONS = ['above', 'below'];

    /** 支持的通知渠道 */
    private const VALID_CHANNELS = ['email', 'webhook'];

    /** 同一告警最小通知间隔（秒） */
    private const NOTIFY_COOLDOWN = 3600;

    /** 每用户告警规则上限 */
    private const MAX_ALERTS_PER_USER = 50;

    private IncusClient $client;

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    // ─── CRUD ────────────────────────────────────────────────

    /**
     * 创建告警规则
     */
    public function createAlert(
        int $userId,
        string $vmName,
        string $metric,
        float $threshold,
        string $direction,
        string $channel,
        ?string $webhookUrl = null
    ): int {
        $this->validateMetric($metric);
        $this->validateDirection($direction);
        $this->validateChannel($channel, $webhookUrl);
        $this->validateVmOwnership($userId, $vmName);

        $currentCount = DB::table('user_alerts')->where('user_id', $userId)->count();
        if ($currentCount >= self::MAX_ALERTS_PER_USER) {
            throw new \RuntimeException(
                "告警规则数已达上限 (" . self::MAX_ALERTS_PER_USER . ")，请删除不需要的规则后重试"
            );
        }

        return DB::table('user_alerts')->insertGetId([
            'user_id'      => $userId,
            'vm_name'      => $vmName,
            'metric'       => $metric,
            'threshold'    => $threshold,
            'direction'    => $direction,
            'channel'      => $channel,
            'webhook_url'  => $channel === 'webhook' ? $webhookUrl : null,
            'enabled'      => true,
            'last_notified_at' => null,
            'created_at'   => now(),
            'updated_at'   => now(),
        ]);
    }

    /**
     * 更新告警规则
     */
    public function updateAlert(int $userId, int $alertId, array $data): bool
    {
        $alert = $this->getOwnedAlert($userId, $alertId);

        $update = [];

        if (isset($data['metric'])) {
            $this->validateMetric($data['metric']);
            $update['metric'] = $data['metric'];
        }
        if (isset($data['threshold'])) {
            $update['threshold'] = (float) $data['threshold'];
        }
        if (isset($data['direction'])) {
            $this->validateDirection($data['direction']);
            $update['direction'] = $data['direction'];
        }
        if (isset($data['channel'])) {
            $webhookUrl = $data['webhook_url'] ?? $alert->webhook_url;
            $this->validateChannel($data['channel'], $webhookUrl);
            $update['channel'] = $data['channel'];
            $update['webhook_url'] = $data['channel'] === 'webhook' ? $webhookUrl : null;
        }
        if (isset($data['webhook_url']) && !isset($data['channel'])) {
            $channel = $alert->channel;
            $this->validateChannel($channel, $data['webhook_url']);
            $update['webhook_url'] = $data['webhook_url'];
        }
        if (isset($data['enabled'])) {
            $update['enabled'] = (bool) $data['enabled'];
        }

        if (empty($update)) {
            return false;
        }

        $update['updated_at'] = now();

        return DB::table('user_alerts')
            ->where('id', $alertId)
            ->update($update) > 0;
    }

    /**
     * 删除告警规则
     */
    public function deleteAlert(int $userId, int $alertId): bool
    {
        $this->getOwnedAlert($userId, $alertId);

        return DB::table('user_alerts')
            ->where('id', $alertId)
            ->delete() > 0;
    }

    /**
     * 列出用户的告警规则
     */
    public function listAlerts(int $userId, ?string $vmName = null): array
    {
        $query = DB::table('user_alerts')->where('user_id', $userId);

        if ($vmName !== null) {
            $query->where('vm_name', $vmName);
        }

        return $query->orderBy('created_at', 'desc')->get()->toArray();
    }

    // ─── 告警检查（Cron 调用）────────────────────────────────

    /**
     * 检查所有已启用的告警规则，触发通知
     */
    public function checkAlerts(): array
    {
        $alerts = DB::table('user_alerts')
            ->where('enabled', true)
            ->get();

        $results = ['checked' => 0, 'triggered' => 0, 'skipped_cooldown' => 0, 'errors' => 0];

        // 按 VM 分组，减少 API 调用
        $grouped = collect($alerts)->groupBy('vm_name');

        foreach ($grouped as $vmName => $vmAlerts) {
            try {
                $metrics = $this->fetchVmMetrics($vmName);
            } catch (\Exception $e) {
                Log::warning("告警检查：无法获取 VM {$vmName} 指标", ['error' => $e->getMessage()]);
                $results['errors'] += count($vmAlerts);
                continue;
            }

            foreach ($vmAlerts as $alert) {
                $results['checked']++;

                $currentValue = $metrics[$alert->metric] ?? null;
                if ($currentValue === null) {
                    $results['errors']++;
                    continue;
                }

                $triggered = $alert->direction === 'above'
                    ? $currentValue > $alert->threshold
                    : $currentValue < $alert->threshold;

                if (!$triggered) {
                    continue;
                }

                // 防 spam：1 小时内不重复通知
                if ($alert->last_notified_at !== null) {
                    $lastNotified = strtotime($alert->last_notified_at);
                    if (time() - $lastNotified < self::NOTIFY_COOLDOWN) {
                        $results['skipped_cooldown']++;
                        continue;
                    }
                }

                try {
                    $this->sendNotification($alert, $currentValue);
                    DB::table('user_alerts')
                        ->where('id', $alert->id)
                        ->update(['last_notified_at' => now()]);
                    $results['triggered']++;
                } catch (\Exception $e) {
                    Log::error("告警通知发送失败", [
                        'alert_id' => $alert->id,
                        'error'    => $e->getMessage(),
                    ]);
                    $results['errors']++;
                }
            }
        }

        return $results;
    }

    // ─── 内部方法 ────────────────────────────────────────────

    /**
     * 从 Incus API 获取 VM 实时指标
     */
    private function fetchVmMetrics(string $vmName): array
    {
        $state = $this->client->request('GET', "/1.0/instances/{$vmName}/state");

        $memory = $state['metadata']['memory'] ?? [];
        $disk = $state['metadata']['disk'] ?? [];

        $memUsage = $memory['usage'] ?? 0;
        $memTotal = $memory['total'] ?? 1;

        $diskUsage = $disk['root']['usage'] ?? 0;
        $diskTotal = $disk['root']['total'] ?? 1;

        return [
            'memory_percent' => $memTotal > 0 ? round($memUsage / $memTotal * 100, 2) : 0,
            'disk_percent'   => $diskTotal > 0 ? round($diskUsage / $diskTotal * 100, 2) : 0,
        ];
    }

    /**
     * 发送告警通知
     */
    private function sendNotification(object $alert, float $currentValue): void
    {
        $user = DB::table('users')->where('id', $alert->user_id)->first();
        if (!$user) {
            throw new \RuntimeException("用户 {$alert->user_id} 不存在");
        }

        $directionLabel = $alert->direction === 'above' ? '高于' : '低于';
        $message = sprintf(
            "VM [%s] 的 %s 当前值 %.2f 已%s阈值 %.2f",
            $alert->vm_name,
            $this->metricLabel($alert->metric),
            $currentValue,
            $directionLabel,
            $alert->threshold
        );

        if ($alert->channel === 'email') {
            Mail::raw($message, function ($mail) use ($user, $alert) {
                $mail->to($user->email)
                     ->subject("VM 告警: {$alert->vm_name} - {$alert->metric}");
            });
        } elseif ($alert->channel === 'webhook') {
            // 发送前重新校验 URL 并锁定解析后的 IP，防止 DNS rebinding
            $resolvedIp = $this->validateWebhookUrl($alert->webhook_url);
            $parsed = parse_url($alert->webhook_url);
            $port = $parsed['port'] ?? 443;
            Http::timeout(10)
                ->withOptions(['curl' => [CURLOPT_RESOLVE => ["{$parsed['host']}:{$port}:{$resolvedIp}"]]])
                ->post($alert->webhook_url, [
                    'alert_id'  => $alert->id,
                    'vm_name'   => $alert->vm_name,
                    'metric'    => $alert->metric,
                    'threshold' => $alert->threshold,
                    'direction' => $alert->direction,
                    'current'   => $currentValue,
                    'message'   => $message,
                    'timestamp' => now()->toIso8601String(),
                ]);
        }

        Log::info("告警通知已发送", [
            'alert_id' => $alert->id,
            'channel'  => $alert->channel,
            'vm_name'  => $alert->vm_name,
        ]);
    }

    private function metricLabel(string $metric): string
    {
        return match ($metric) {
            'memory_percent' => '内存使用率 (%)',
            'disk_percent'   => '磁盘使用率 (%)',
            default          => $metric,
        };
    }

    // ─── 校验 ────────────────────────────────────────────────

    private function validateMetric(string $metric): void
    {
        if (!in_array($metric, self::VALID_METRICS, true)) {
            throw new \InvalidArgumentException(
                "不支持的指标: {$metric}，可选: " . implode(', ', self::VALID_METRICS)
            );
        }
    }

    private function validateDirection(string $direction): void
    {
        if (!in_array($direction, self::VALID_DIRECTIONS, true)) {
            throw new \InvalidArgumentException(
                "不支持的方向: {$direction}，可选: above, below"
            );
        }
    }

    private function validateChannel(string $channel, ?string $webhookUrl): void
    {
        if (!in_array($channel, self::VALID_CHANNELS, true)) {
            throw new \InvalidArgumentException(
                "不支持的渠道: {$channel}，可选: email, webhook"
            );
        }
        if ($channel === 'webhook') {
            if (empty($webhookUrl)) {
                throw new \InvalidArgumentException('webhook 渠道必须提供 webhook_url');
            }
            $this->validateWebhookUrl($webhookUrl);
        }
    }

    /**
     * 校验 webhook URL，防止 SSRF 攻击
     *
     * 返回解析后的 IP，供 sendNotification 直接使用以防止 DNS rebinding。
     */
    private function validateWebhookUrl(string $url): string
    {
        $parsed = parse_url($url);
        if ($parsed === false || !isset($parsed['scheme'], $parsed['host'])) {
            throw new \InvalidArgumentException('webhook_url 格式无效');
        }

        // 仅允许 HTTPS
        if (strtolower($parsed['scheme']) !== 'https') {
            throw new \InvalidArgumentException('webhook_url 仅允许 https 协议');
        }

        $host = $parsed['host'];

        // 解析域名为 IP
        $ip = filter_var($host, FILTER_VALIDATE_IP) ? $host : gethostbyname($host);
        if ($ip === $host && !filter_var($host, FILTER_VALIDATE_IP)) {
            throw new \InvalidArgumentException('webhook_url 域名无法解析');
        }

        $this->assertPublicIp($ip);

        return $ip;
    }

    /**
     * 检查 IP 是否为公网地址（同时支持 IPv4 和 IPv6）
     */
    private function assertPublicIp(string $ip): void
    {
        // IPv4 检查
        $ipLong = ip2long($ip);
        if ($ipLong !== false) {
            $forbiddenV4 = [
                '127.0.0.0/8',      // 回环
                '10.0.0.0/8',       // RFC1918
                '172.16.0.0/12',    // RFC1918
                '192.168.0.0/16',   // RFC1918
                '169.254.0.0/16',   // link-local
                '100.64.0.0/10',    // CGN
                '0.0.0.0/8',        // 本地
            ];
            foreach ($forbiddenV4 as $cidr) {
                [$subnet, $bits] = explode('/', $cidr);
                $subnetLong = ip2long($subnet);
                $mask = -1 << (32 - (int) $bits);
                if (($ipLong & $mask) === ($subnetLong & $mask)) {
                    throw new \InvalidArgumentException('webhook_url 不允许指向内网/回环/link-local 地址');
                }
            }
            return;
        }

        // IPv6 检查
        $packed = inet_pton($ip);
        if ($packed === false) {
            throw new \InvalidArgumentException('webhook_url IP 地址格式无效');
        }

        $forbiddenV6 = [
            '::1/128',          // 回环
            'fc00::/7',         // ULA
            'fe80::/10',        // link-local
            '::ffff:127.0.0.0/104', // v4-mapped 回环
            '::ffff:10.0.0.0/104',  // v4-mapped RFC1918
            '::ffff:172.16.0.0/108', // v4-mapped RFC1918
            '::ffff:192.168.0.0/112', // v4-mapped RFC1918
        ];
        foreach ($forbiddenV6 as $cidr) {
            [$subnet, $bits] = explode('/', $cidr);
            $subnetPacked = inet_pton($subnet);
            if ($subnetPacked === false) {
                continue;
            }
            $bits = (int) $bits;
            // 按位比较前缀
            $fullBytes = intdiv($bits, 8);
            $remainBits = $bits % 8;
            if (substr($packed, 0, $fullBytes) !== substr($subnetPacked, 0, $fullBytes)) {
                continue;
            }
            if ($remainBits > 0) {
                $mask = 0xFF << (8 - $remainBits) & 0xFF;
                if ((ord($packed[$fullBytes]) & $mask) !== (ord($subnetPacked[$fullBytes]) & $mask)) {
                    continue;
                }
            }
            throw new \InvalidArgumentException('webhook_url 不允许指向内网/回环/link-local 地址');
        }
    }

    private function validateVmOwnership(int $userId, string $vmName): void
    {
        $exists = DB::table('orders')
            ->join('order_products', 'orders.id', '=', 'order_products.order_id')
            ->where('orders.user_id', $userId)
            ->where('order_products.config->vm_name', $vmName)
            ->where('orders.status', 'active')
            ->exists();

        if (!$exists) {
            throw new \RuntimeException("VM {$vmName} 不属于当前用户或不存在");
        }
    }

    private function getOwnedAlert(int $userId, int $alertId): object
    {
        $alert = DB::table('user_alerts')
            ->where('id', $alertId)
            ->where('user_id', $userId)
            ->first();

        if (!$alert) {
            throw new \RuntimeException("告警规则 #{$alertId} 不存在或无权操作");
        }

        return $alert;
    }
}
