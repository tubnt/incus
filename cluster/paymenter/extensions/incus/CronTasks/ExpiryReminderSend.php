<?php

namespace Extensions\Incus\CronTasks;

use Extensions\Incus\Notifications\DeletionWarning;
use Extensions\Incus\Notifications\ExpiryReminder;
use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * 到期提醒发送（每日 09:00）
 *
 * D-7 / D-3 / D-1 发送到期提醒邮件。
 * D+5 发送删除前最后警告。
 */
class ExpiryReminderSend
{
    private const REMINDER_DAYS = [7, 3, 1];

    public function __invoke(): void
    {
        $today = Carbon::today();

        // D-7 / D-3 / D-1 到期提醒
        foreach (self::REMINDER_DAYS as $days) {
            $targetDate = $today->copy()->addDays($days);

            $orders = DB::table('orders')
                ->where('status', 'active')
                ->whereDate('expires_at', $targetDate->toDateString())
                ->get();

            foreach ($orders as $order) {
                $user = DB::table('users')->find($order->user_id);
                if (!$user || !($order->vm_name ?? null)) {
                    continue;
                }

                $user = (object) $user;
                if (method_exists($user, 'notify')) {
                    $user->notify(new ExpiryReminder(
                        $order->vm_name,
                        $order->ip ?? '',
                        $days,
                        Carbon::parse($order->expires_at)->format('Y-m-d'),
                    ));
                }

                Log::info("ExpiryReminderSend: 已向用户 {$order->user_id} 发送 D-{$days} 到期提醒（VM: {$order->vm_name}）");
            }
        }

        // D+5 删除前警告
        $suspendedOrders = DB::table('orders')
            ->where('status', 'suspended')
            ->whereDate('suspended_at', $today->copy()->subDays(5)->toDateString())
            ->get();

        foreach ($suspendedOrders as $order) {
            $user = DB::table('users')->find($order->user_id);
            if (!$user || !($order->vm_name ?? null)) {
                continue;
            }

            $deletionDate = Carbon::parse($order->suspended_at)->addDays(7)->format('Y-m-d');
            $user = (object) $user;
            if (method_exists($user, 'notify')) {
                $user->notify(new DeletionWarning(
                    $order->vm_name,
                    $order->ip ?? '',
                    $deletionDate,
                ));
            }

            Log::info("ExpiryReminderSend: 已向用户 {$order->user_id} 发送 D+5 删除警告（VM: {$order->vm_name}）");
        }
    }
}
