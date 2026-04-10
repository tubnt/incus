<?php

namespace Extensions\Incus\Notifications;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Notifications\Notification;

/**
 * 暂停通知（D+0，到期当天）
 */
class SuspensionNotice extends Notification implements ShouldQueue
{
    use Queueable;

    public function __construct(
        private string $vmName,
        private string $ip,
        private string $deletionDate,
    ) {}

    public function via($notifiable): array
    {
        return ['mail'];
    }

    public function toMail($notifiable): MailMessage
    {
        return (new MailMessage)
            ->subject("【重要】云主机 {$this->vmName} 已暂停")
            ->greeting("您好，{$notifiable->name}！")
            ->line("由于未及时续费，您的云主机 **{$this->vmName}**（{$this->ip}）已被暂停。")
            ->line("**数据保留截止：** {$this->deletionDate}")
            ->line('> ⚠ 暂停期间数据仍保留在服务器上。超过保留期后，数据将被永久删除且无法恢复。')
            ->action('立即续费恢复服务', url('/'))
            ->line('续费后云主机将自动恢复运行。');
    }
}
