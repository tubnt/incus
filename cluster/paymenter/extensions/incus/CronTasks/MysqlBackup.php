<?php

namespace Extensions\Incus\CronTasks;

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\Log;

/**
 * MySQL 备份（每日 02:00）
 *
 * 使用 mysqldump 备份 Paymenter 数据库到独立存储。
 * 保留最近 7 份备份。
 */
class MysqlBackup
{
    private const BACKUP_DIR = '/var/backups/mysql';
    private const RETENTION_COUNT = 7;

    public function __invoke(): void
    {
        $timestamp = Carbon::now()->format('Ymd-His');
        $filename = self::BACKUP_DIR . "/paymenter-{$timestamp}.sql.gz";

        $dbHost = config('database.connections.mysql.host');
        $dbName = config('database.connections.mysql.database');
        $dbUser = config('database.connections.mysql.username');
        $dbPass = config('database.connections.mysql.password');

        if (!is_dir(self::BACKUP_DIR)) {
            mkdir(self::BACKUP_DIR, 0700, true);
        }

        $command = sprintf(
            'mysqldump -h %s -u %s %s --single-transaction --routines --triggers | gzip > %s',
            escapeshellarg($dbHost),
            escapeshellarg($dbUser),
            escapeshellarg($dbName),
            escapeshellarg($filename),
        );

        // 通过环境变量传递密码，不暴露在命令行中
        putenv("MYSQL_PWD={$dbPass}");
        $returnCode = 0;
        exec($command, $output, $returnCode);
        putenv('MYSQL_PWD');

        if ($returnCode !== 0) {
            Log::error("MysqlBackup: 备份失败，返回码: {$returnCode}");
            return;
        }

        Log::info("MysqlBackup: 备份完成 → {$filename}");

        // 清理旧备份，保留最近 N 份
        $files = glob(self::BACKUP_DIR . '/paymenter-*.sql.gz');
        if ($files !== false) {
            sort($files);
            $toDelete = array_slice($files, 0, max(0, count($files) - self::RETENTION_COUNT));
            foreach ($toDelete as $file) {
                unlink($file);
                Log::info("MysqlBackup: 清理旧备份 {$file}");
            }
        }
    }
}
