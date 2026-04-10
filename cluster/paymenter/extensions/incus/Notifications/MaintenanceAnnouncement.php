<?php

namespace Extensions\Incus\Notifications;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Notifications\Messages\MailMessage;
use Illuminate\Notifications\Notification;

/**
 * 维护/故障公告通知
 */
class MaintenanceAnnouncement extends Notification implements ShouldQueue
{
    use Queueable;

    public function __construct(
        private string $title,
        private string $content,
        private string $startTime,
        private string $endTime,
        private string $impact,
    ) {}

    public function via($notifiable): array
    {
        return ['mail'];
    }

    public function toMail($notifiable): MailMessage
    {
        return (new MailMessage)
            ->subject("【公告】{$this->title}")
            ->greeting("您好，{$notifiable->name}！")
            ->line($this->content)
            ->line("**计划时间：** {$this->startTime} — {$this->endTime}")
            ->line("**影响范围：** {$this->impact}")
            ->line('维护期间受影响的服务可能出现短暂中断，敬请谅解。')
            ->line('如有紧急问题，请通过工单系统联系我们。');
    }
}
