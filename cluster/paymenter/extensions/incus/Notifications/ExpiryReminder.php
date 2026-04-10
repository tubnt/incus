<?php

namespace Extensions\Incus\Notifications;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Notifications\Notification;

/**
 * 到期提醒通知（D-7 / D-3 / D-1）
 */
class ExpiryReminder extends Notification implements ShouldQueue
{
    use Queueable;

    public function __construct(
        private string $vmName,
        private string $ip,
        private int $daysRemaining,
        private string $expiresAt,
    ) {}

    public function via($notifiable): array
    {
        return ['mail'];
    }

    public function toMail($notifiable): MailMessage
    {
        $urgency = match (true) {
            $this->daysRemaining <= 1 => '【紧急】',
            $this->daysRemaining <= 3 => '【重要】',
            default => '',
        };

        $message = (new MailMessage)
            ->subject("{$urgency}云主机 {$this->vmName} 将在 {$this->daysRemaining} 天后到期")
            ->greeting("您好，{$notifiable->name}！")
            ->line("您的云主机 **{$this->vmName}**（{$this->ip}）将于 **{$this->expiresAt}** 到期。")
            ->line("距到期还有 **{$this->daysRemaining} 天**。");

        if ($this->daysRemaining <= 1) {
            $message->line('> ⚠ 到期后云主机将被暂停，暂停 7 天后数据将被永久删除。请尽快续费。');
        }

        return $message
            ->action('立即续费', url('/'))
            ->line('续费后服务将自动延期，无需其他操作。');
    }
}
