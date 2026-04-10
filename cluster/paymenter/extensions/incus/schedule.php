<?php

use Extensions\Incus\CronTasks\AutoBackup;
use Extensions\Incus\CronTasks\BackupCleanup;
use Extensions\Incus\CronTasks\ConsistencyCheck;
use Extensions\Incus\CronTasks\CronHealthCheck;
use Extensions\Incus\CronTasks\DmcryptKeyBackup;
use Extensions\Incus\CronTasks\ExpiredVmSuspend;
use Extensions\Incus\CronTasks\ExpiryReminderSend;
use Extensions\Incus\CronTasks\IpCooldownRecycle;
use Extensions\Incus\CronTasks\IpPoolAlert;
use Extensions\Incus\CronTasks\MonthlyTrafficReset;
use Extensions\Incus\CronTasks\MysqlBackup;
use Extensions\Incus\CronTasks\OverdueVmDelete;
use Extensions\Incus\CronTasks\TrafficStats;
use Extensions\Incus\CronTasks\TrafficThrottle;
use Illuminate\Console\Scheduling\Schedule;

/**
 * Incus 扩展 — Laravel 调度注册
 *
 * 在 App\Console\Kernel::schedule() 中引入此文件：
 *   require base_path('extensions/incus/schedule.php');
 */

/** @var Schedule $schedule */

// ── 每小时任务 ──────────────────────────────────────────────
$schedule->call(new IpCooldownRecycle)
    ->hourly()
    ->name('incus:ip-cooldown-recycle')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new TrafficStats)
    ->hourly()
    ->name('incus:traffic-stats')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new TrafficThrottle)
    ->hourly()
    ->name('incus:traffic-throttle')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new IpPoolAlert)
    ->hourly()
    ->name('incus:ip-pool-alert')
    ->withoutOverlapping()
    ->onOneServer();

// ── 每日任务 ──────────────────────────────────────────────
$schedule->call(new ExpiredVmSuspend)
    ->dailyAt('00:00')
    ->name('incus:expired-vm-suspend')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new OverdueVmDelete)
    ->dailyAt('00:00')
    ->name('incus:overdue-vm-delete')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new MysqlBackup)
    ->dailyAt('02:00')
    ->name('incus:mysql-backup')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new DmcryptKeyBackup)
    ->dailyAt('02:30')
    ->name('incus:dmcrypt-key-backup')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new AutoBackup)
    ->dailyAt('03:00')
    ->name('incus:auto-backup')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new BackupCleanup)
    ->dailyAt('04:00')
    ->name('incus:backup-cleanup')
    ->withoutOverlapping()
    ->onOneServer();

$schedule->call(new ExpiryReminderSend)
    ->dailyAt('09:00')
    ->name('incus:expiry-reminder-send')
    ->withoutOverlapping()
    ->onOneServer();

// ── 每 6 小时任务 ──────────────────────────────────────────
$schedule->call(new ConsistencyCheck)
    ->everySixHours()
    ->name('incus:consistency-check')
    ->withoutOverlapping()
    ->onOneServer();

// ── 每月任务 ──────────────────────────────────────────────
$schedule->call(new MonthlyTrafficReset)
    ->monthlyOn(1, '00:00')
    ->name('incus:monthly-traffic-reset')
    ->withoutOverlapping()
    ->onOneServer();

// ── 健康监控（每分钟，deadman switch）──────────────────────
$schedule->call(new CronHealthCheck)
    ->everyMinute()
    ->name('incus:cron-health-check');
