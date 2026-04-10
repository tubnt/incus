<?php

namespace Extensions\Incus\CronTasks;

use App\Extensions\Incus\UserAlertManager;
use App\Extensions\Incus\IncusClient;
use Illuminate\Console\Command;
use Illuminate\Support\Facades\Log;

/**
 * 用户告警定时检查任务
 *
 * 建议调度频率：每 5 分钟
 * 在 Kernel.php 中注册：$schedule->command('incus:check-user-alerts')->everyFiveMinutes();
 */
class UserAlertCheck extends Command
{
    protected $signature = 'incus:check-user-alerts';
    protected $description = '检查所有启用的用户 VM 告警规则并发送通知';

    public function handle(): int
    {
        $this->info('开始检查用户告警...');

        try {
            $client = app(IncusClient::class);
            $manager = new UserAlertManager($client);
            $results = $manager->checkAlerts();

            $this->info(sprintf(
                '告警检查完成：已检查 %d 条，触发 %d 条，冷却跳过 %d 条，错误 %d 条',
                $results['checked'],
                $results['triggered'],
                $results['skipped_cooldown'],
                $results['errors']
            ));

            Log::info('用户告警检查完成', $results);

            return self::SUCCESS;
        } catch (\Exception $e) {
            $this->error("告警检查异常: {$e->getMessage()}");
            Log::error('用户告警检查异常', ['error' => $e->getMessage(), 'trace' => $e->getTraceAsString()]);
            return self::FAILURE;
        }
    }
}
