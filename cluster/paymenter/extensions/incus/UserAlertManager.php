<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Facades\Log;

/**
 * 用户级告警管理器
 *
 * 允许用户为自己的 VM 设置阈值告警，支持 CPU / 内存 / 带宽 / 磁盘 等指标。
 * 防 spam 机制：同一告警 1 小时内不重复通知。
 */
class UserAlertManager
{
    /** 支持的监控指标（cpu_percent 需要两次采样计算差值，Incus API 仅返回累计纳秒，暂不支持） */
    private const VALID_METRICS = [
        'memory_percent',
        'bandwidth_in',
        'bandwidth_out',
        'disk_percent',
    ];

    /** 支持的告警方向 */
    private const VALID_DIRECTIONS = ['above', 'below'];

    /** 支持的通知渠道 */
    private const VALID_CHANNELS = ['email', 'webhook'];

    /** 同一告警最小通知间隔（秒） */
    private const NOTIFY_COOLDOWN = 3600;

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

        $cpu = $state['metadata']['cpu'] ?? [];
        $memory = $state['metadata']['memory'] ?? [];
        $disk = $state['metadata']['disk'] ?? [];
        $network = $state['metadata']['network'] ?? [];

        $memUsage = $memory['usage'] ?? 0;
        $memTotal = $memory['total'] ?? 1;

        $diskUsage = $disk['root']['usage'] ?? 0;
        $diskTotal = $disk['root']['total'] ?? 1;

        // 聚合所有网卡（排除 loopback）的流量
        $rxBytes = 0;
        $txBytes = 0;
        foreach ($network as $iface => $stats) {
            if ($iface === 'lo') {
                continue;
            }
            $rxBytes += $stats['counters']['bytes_received'] ?? 0;
            $txBytes += $stats['counters']['bytes_sent'] ?? 0;
        }

        return [
            'memory_percent' => $memTotal > 0 ? round($memUsage / $memTotal * 100, 2) : 0,
            'bandwidth_in'   => $rxBytes,
            'bandwidth_out'  => $txBytes,
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
            Http::timeout(10)->post($alert->webhook_url, [
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
            'bandwidth_in'   => '入站带宽 (bytes)',
            'bandwidth_out'  => '出站带宽 (bytes)',
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
     */
    private function validateWebhookUrl(string $url): void
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

        // 禁止内网/回环/link-local 地址
        $forbidden = [
            '127.0.0.0/8',      // 回环
            '10.0.0.0/8',       // RFC1918
            '172.16.0.0/12',    // RFC1918
            '192.168.0.0/16',   // RFC1918
            '169.254.0.0/16',   // link-local
            '100.64.0.0/10',    // CGN
            '0.0.0.0/8',       // 本地
            '::1/128',          // IPv6 回环
            'fc00::/7',         // IPv6 ULA
            'fe80::/10',        // IPv6 link-local
        ];

        $ipLong = ip2long($ip);
        if ($ipLong !== false) {
            foreach ($forbidden as $cidr) {
                if (str_contains($cidr, ':')) {
                    continue; // 跳过 IPv6 CIDR（当前 IP 是 v4）
                }
                [$subnet, $bits] = explode('/', $cidr);
                $subnetLong = ip2long($subnet);
                $mask = -1 << (32 - (int) $bits);
                if (($ipLong & $mask) === ($subnetLong & $mask)) {
                    throw new \InvalidArgumentException('webhook_url 不允许指向内网/回环/link-local 地址');
                }
            }
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
