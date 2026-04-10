<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\ExpiryHandler;
use Extensions\Incus\IncusClient;
use Illuminate\Support\Facades\Log;

/**
 * 过期 VM 删除（D+7，每日 00:00，幂等）
 *
 * 调用 ExpiryHandler::deleteOverdue() 删除所有暂停超过 7 天的 VM 并回收 IP。
 */
class OverdueVmDelete
{
    public function __invoke(): void
    {
        $handler = new ExpiryHandler(app(IncusClient::class));
        $deleted = $handler->deleteOverdue();

        if (count($deleted) > 0) {
            Log::info('OverdueVmDelete: 本次删除 ' . count($deleted) . ' 台 VM: ' . implode(', ', $deleted));
        }
    }
}
