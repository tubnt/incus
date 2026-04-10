<?php

namespace Extensions\Incus\CronTasks;

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Log;

/**
 * Cron 自身健康监控（deadman switch）
 *
 * 每次执行时更新心跳时间戳。外部监控（如 Alertmanager）检查该时间戳，
 * 如果超过预期间隔未更新则触发告警，说明 Cron 调度器已停止工作。
 */
class CronHealthCheck
{
    private const CACHE_KEY = 'cron:healthcheck:last_heartbeat';
    private const HEARTBEAT_FILE = '/tmp/cron-healthcheck-heartbeat';

    public function __invoke(): void
    {
        $now = Carbon::now();

        // 更新 Redis 心跳
        Cache::put(self::CACHE_KEY, $now->toIso8601String(), now()->addHours(2));

        // 同时写入文件，供 Prometheus node_exporter textfile collector 采集
        $metrics = "# HELP cron_healthcheck_last_heartbeat_timestamp_seconds Cron 调度器最后心跳时间\n"
            . "# TYPE cron_healthcheck_last_heartbeat_timestamp_seconds gauge\n"
            . "cron_healthcheck_last_heartbeat_timestamp_seconds {$now->timestamp}\n";

        file_put_contents(self::HEARTBEAT_FILE, $metrics);

        Log::debug("CronHealthCheck: 心跳更新 {$now->toIso8601String()}");
    }
}
