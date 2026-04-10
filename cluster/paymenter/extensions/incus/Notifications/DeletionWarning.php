<?php

namespace Extensions\Incus\Notifications;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Notifications\Notification;

/**
 * 删除前警告（D+5，删除前 2 天）
 */
class DeletionWarning extends Notification implements ShouldQueue
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
            ->subject("【最后警告】云主机 {$this->vmName} 将在 2 天后被删除")
            ->greeting("您好，{$notifiable->name}！")
            ->line("您的云主机 **{$this->vmName}**（{$this->ip}）已暂停 5 天。")
            ->line("**删除时间：** {$this->deletionDate}")
            ->line('> ⚠ 这是最后一次提醒。删除后所有数据将永久丢失，无法恢复。')
            ->action('立即续费保留数据', url('/'))
            ->line('如您不再需要此服务，可忽略本邮件。');
    }
}
