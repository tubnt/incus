<?php

namespace Extensions\Incus\Notifications;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Notifications\Notification;

/**
 * 订单确认通知
 *
 * VM 信息（IP/连接方式）通过邮件发送，密码仅在面板内一次性显示，不通过邮件发送。
 */
class OrderConfirmation extends Notification implements ShouldQueue
{
    use Queueable;

    public function __construct(
        private string $vmName,
        private string $ip,
        private string $os,
        private string $specs,
        private string $expiresAt,
    ) {}

    public function via($notifiable): array
    {
        return ['mail'];
    }

    public function toMail($notifiable): MailMessage
    {
        return (new MailMessage)
            ->subject("订单确认 — 云主机 {$this->vmName} 已创建")
            ->greeting("您好，{$notifiable->name}！")
            ->line('您的云主机已创建成功，以下是连接信息：')
            ->line("**主机名称：** {$this->vmName}")
            ->line("**IP 地址：** {$this->ip}")
            ->line("**操作系统：** {$this->os}")
            ->line("**规格配置：** {$this->specs}")
            ->line("**到期时间：** {$this->expiresAt}")
            ->line("**SSH 连接：** `ssh root@{$this->ip}`")
            ->line('> ★ 初始密码已在控制面板中一次性显示，请务必妥善保存。出于安全考虑，密码不会通过邮件发送。')
            ->action('进入控制面板', url('/'))
            ->line('如有任何问题，请通过工单系统联系我们。');
    }
}
