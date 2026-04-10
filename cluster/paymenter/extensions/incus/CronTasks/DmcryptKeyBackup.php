<?php

namespace Extensions\Incus\CronTasks;

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\Log;

/**
 * dmcrypt 密钥备份（每日 02:30）
 *
 * 导出 Incus 使用的 dmcrypt 加密密钥到安全存储位置。
 * 密钥丢失将导致加密卷数据不可恢复。
 */
class DmcryptKeyBackup
{
    private const BACKUP_DIR = '/var/backups/dmcrypt-keys';
    private const KEY_SOURCE_DIR = '/var/lib/incus/storage-pools';
    private const RETENTION_COUNT = 7;

    public function __invoke(): void
    {
        $timestamp = Carbon::now()->format('Ymd-His');
        $archivePath = self::BACKUP_DIR . "/dmcrypt-keys-{$timestamp}.tar.gz.enc";

        if (!is_dir(self::BACKUP_DIR)) {
            mkdir(self::BACKUP_DIR, 0700, true);
        }

        // 加密打包密钥文件
        $encryptionKey = config('incus.dmcrypt_backup_key');
        if (!$encryptionKey) {
            Log::error('DmcryptKeyBackup: 未配置 dmcrypt 备份加密密钥（INCUS_DMCRYPT_BACKUP_KEY）');
            return;
        }

        $command = sprintf(
            'tar -czf - -C %s . | openssl enc -aes-256-cbc -salt -pbkdf2 -pass env:BACKUP_KEY -out %s',
            escapeshellarg(self::KEY_SOURCE_DIR),
            escapeshellarg($archivePath),
        );

        putenv("BACKUP_KEY={$encryptionKey}");
        $returnCode = 0;
        exec($command, $output, $returnCode);
        putenv('BACKUP_KEY');

        if ($returnCode !== 0) {
            Log::error("DmcryptKeyBackup: 密钥备份失败，返回码: {$returnCode}");
            return;
        }

        Log::info("DmcryptKeyBackup: 密钥备份完成 → {$archivePath}");

        // 清理旧备份
        $files = glob(self::BACKUP_DIR . '/dmcrypt-keys-*.tar.gz.enc');
        if ($files !== false) {
            sort($files);
            $toDelete = array_slice($files, 0, max(0, count($files) - self::RETENTION_COUNT));
            foreach ($toDelete as $file) {
                unlink($file);
                Log::info("DmcryptKeyBackup: 清理旧备份 {$file}");
            }
        }
    }
}
