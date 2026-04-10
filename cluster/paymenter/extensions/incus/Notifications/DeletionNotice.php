<?php

namespace Extensions\Incus\Notifications;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Notifications\Notification;

/**
 * 删除通知（D+7，VM 已删除）
 */
class DeletionNotice extends Notification implements ShouldQueue
{
    use Queueable;

    public function __construct(
        private string $vmName,
        private string $ip,
    ) {}

    public function via($notifiable): array
    {
        return ['mail'];
    }

    public function toMail($notifiable): MailMessage
    {
        return (new MailMessage)
            ->subject("云主机 {$this->vmName} 已删除")
            ->greeting("您好，{$notifiable->name}！")
            ->line("由于超过保留期限仍未续费，您的云主机 **{$this->vmName}**（{$this->ip}）及其全部数据已被永久删除。")
            ->line('IP 地址已回收，将在冷却期后重新分配。')
            ->line('如需重新开通服务，请访问控制面板选购新的云主机。')
            ->action('重新选购', url('/'));
    }
}
