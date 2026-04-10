<?php

namespace Extensions\Incus\Notifications;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Notifications\Notification;

/**
 * 注册欢迎通知
 */
class WelcomeNotification extends Notification implements ShouldQueue
{
    use Queueable;

    public function via($notifiable): array
    {
        return ['mail'];
    }

    public function toMail($notifiable): MailMessage
    {
        return (new MailMessage)
            ->subject('欢迎注册 — 开始使用云主机服务')
            ->greeting("您好，{$notifiable->name}！")
            ->line('感谢您注册我们的云主机服务。')
            ->line('您的账户已创建成功，现在可以开始选购云主机产品。')
            ->action('登录控制面板', url('/'))
            ->line('如有任何问题，请通过工单系统联系我们。');
    }
}
