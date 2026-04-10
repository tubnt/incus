<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\ExpiryHandler;
use Extensions\Incus\IncusClient;
use Illuminate\Support\Facades\Log;

/**
 * 到期 VM 暂停（每日 00:00，幂等）
 *
 * 调用 ExpiryHandler::suspendExpired() 暂停所有到期 VM。
 */
class ExpiredVmSuspend
{
    public function __invoke(): void
    {
        $handler = new ExpiryHandler(app(IncusClient::class));
        $suspended = $handler->suspendExpired();

        if (count($suspended) > 0) {
            Log::info('ExpiredVmSuspend: 本次暂停 ' . count($suspended) . ' 台 VM: ' . implode(', ', $suspended));
        }
    }
}
