<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\IncusClient;
use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * 备份清理（每日 04:00，保留 7 天）
 *
 * 删除超过 7 天的自动备份快照。
 */
class BackupCleanup
{
    private const RETENTION_DAYS = 7;

    public function __invoke(): void
    {
        $client = app(IncusClient::class);
        $cutoff = Carbon::now()->subDays(self::RETENTION_DAYS);

        $expiredBackups = DB::table('backups')
            ->where('type', 'auto')
            ->where('created_at', '<', $cutoff)
            ->get();

        $cleaned = 0;

        foreach ($expiredBackups as $backup) {
            try {
                $client->deleteSnapshot($backup->vm_name, $backup->snapshot_name);
            } catch (\Throwable $e) {
                $msg = $e->getMessage();
                // 快照已不存在（404/Not Found），仍然清理 DB 记录
                if (str_contains($msg, '404') || str_contains($msg, 'not found') || str_contains($msg, 'Not Found')) {
                    Log::info("BackupCleanup: 快照 {$backup->vm_name}/{$backup->snapshot_name} 已不存在，清理 DB 记录");
                } else {
                    Log::warning("BackupCleanup: 删除快照 {$backup->vm_name}/{$backup->snapshot_name} 失败: {$msg}");
                    continue;
                }
            }

            DB::table('backups')->where('id', $backup->id)->delete();
            $cleaned++;
            Log::info("BackupCleanup: 删除过期快照 {$backup->vm_name}/{$backup->snapshot_name}");
        }

        if ($cleaned > 0) {
            Log::info("BackupCleanup: 本次清理 {$cleaned} 个过期备份");
        }
    }
}
