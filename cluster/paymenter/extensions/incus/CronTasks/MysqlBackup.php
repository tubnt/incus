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

        // 使用 set -o pipefail 确保 mysqldump 失败时整个管道返回错误
        $command = sprintf(
            'set -o pipefail && mysqldump -h %s -u %s %s --single-transaction --routines --triggers | gzip > %s',
            escapeshellarg($dbHost),
            escapeshellarg($dbUser),
            escapeshellarg($dbName),
            escapeshellarg($filename),
        );

        // 通过 proc_open 的 env 参数隔离密码，不污染父进程环境
        $returnCode = $this->execWithEnv($command, ['MYSQL_PWD' => $dbPass]);

        if ($returnCode !== 0) {
            // 清理可能的空/损坏备份文件
            if (file_exists($filename)) {
                unlink($filename);
            }
            Log::error("MysqlBackup: 备份失败，返回码: {$returnCode}");
            return;
        }

        // 限制备份文件权限
        chmod($filename, 0600);

        // 检查备份文件大小是否合理（空 gzip 约 20 字节）
        if (filesize($filename) < 100) {
            Log::error("MysqlBackup: 备份文件异常小（" . filesize($filename) . " 字节），可能为空备份");
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

    /**
     * 在隔离的环境变量中执行命令，防止密码泄漏到父进程
     */
    private function execWithEnv(string $command, array $extraEnv): int
    {
        $env = array_merge(getenv(), $extraEnv);
        $descriptors = [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']];
        $proc = proc_open(['bash', '-c', $command], $descriptors, $pipes, null, $env);

        if (!is_resource($proc)) {
            return 1;
        }

        fclose($pipes[0]);
        fclose($pipes[1]);
        fclose($pipes[2]);

        return proc_close($proc);
    }
}
