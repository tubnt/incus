{{-- 救援模式管理视图 --}}
@extends('layouts.app')

@section('title', '救援模式')

@section('content')
<div class="container mx-auto px-4 py-6">
    <div class="bg-white rounded-lg shadow p-6">
        <h2 class="text-xl font-semibold mb-4">救援模式 — {{ $vmName }}</h2>

        @if($inRescue)
            {{-- 救援模式活跃状态 --}}
            <div class="bg-yellow-50 border-l-4 border-yellow-400 p-4 mb-6">
                <div class="flex items-start">
                    <svg class="w-5 h-5 text-yellow-400 mr-2 mt-0.5" fill="currentColor" viewBox="0 0 20 20">
                        <path fill-rule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/>
                    </svg>
                    <div>
                        <p class="font-medium text-yellow-800">VM 当前处于救援模式</p>
                        <p class="text-sm text-yellow-700 mt-1">
                            原系统磁盘已挂载到 <code class="bg-yellow-100 px-1 rounded">/mnt</code>，
                            你可以通过 SSH 或控制台登录修复系统。
                        </p>
                    </div>
                </div>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mb-6">
                <div class="bg-gray-50 rounded p-4">
                    <p class="text-sm text-gray-500">启动时间</p>
                    <p class="text-lg font-mono">{{ $rescueInfo['started_at'] }}</p>
                </div>
                <div class="bg-gray-50 rounded p-4">
                    <p class="text-sm text-gray-500">自动退出时间</p>
                    <p class="text-lg font-mono">{{ $rescueInfo['expires_at'] }}</p>
                </div>
                <div class="bg-gray-50 rounded p-4">
                    <p class="text-sm text-gray-500">剩余时间</p>
                    <p class="text-lg font-mono" id="countdown">
                        {{ gmdate('H:i:s', $rescueInfo['remaining_seconds']) }}
                    </p>
                </div>
            </div>

            <div class="bg-blue-50 border rounded p-4 mb-6">
                <p class="text-sm font-medium text-blue-800 mb-2">使用提示</p>
                <ul class="text-sm text-blue-700 list-disc list-inside space-y-1">
                    <li>原系统盘挂载在 <code class="bg-blue-100 px-1 rounded">/mnt</code></li>
                    <li>修改 fstab：<code class="bg-blue-100 px-1 rounded">vi /mnt/etc/fstab</code></li>
                    <li>重置密码：<code class="bg-blue-100 px-1 rounded">chroot /mnt passwd</code></li>
                    <li>查看日志：<code class="bg-blue-100 px-1 rounded">cat /mnt/var/log/syslog</code></li>
                    <li>救援模式最长 2 小时，超时自动恢复正常启动</li>
                </ul>
            </div>

            <form method="POST" action="{{ route('incus.rescue.exit', $vmName) }}"
                  onsubmit="return confirm('确定退出救援模式？VM 将恢复原系统启动。')">
                @csrf
                <button type="submit"
                        class="bg-green-600 hover:bg-green-700 text-white font-medium py-2 px-6 rounded transition">
                    退出救援模式（恢复正常启动）
                </button>
            </form>

        @else
            {{-- 正常状态 —— 可进入救援模式 --}}
            <div class="mb-6">
                <p class="text-gray-600 mb-4">
                    当 VM 无法正常启动时（如 fstab 配置错误、密码丢失、内核问题等），
                    可通过救援模式以临时系统启动，原系统磁盘将挂载到 /mnt 供修复。
                </p>

                <div class="bg-gray-50 border rounded p-4">
                    <p class="text-sm font-medium text-gray-700 mb-2">注意事项</p>
                    <ul class="text-sm text-gray-600 list-disc list-inside space-y-1">
                        <li>进入救援模式会<strong>停止当前 VM</strong>，请确保已保存重要数据</li>
                        <li>将以 Ubuntu 22.04 救援镜像启动，原系统盘挂载到 /mnt</li>
                        <li>系统会生成临时 root 密码，用于登录救援系统</li>
                        <li>救援模式最长持续 2 小时，超时自动退出</li>
                    </ul>
                </div>
            </div>

            <form method="POST" action="{{ route('incus.rescue.enter', $vmName) }}"
                  onsubmit="return confirm('确定进入救援模式？当前 VM 将被停止。')">
                @csrf
                <button type="submit"
                        class="bg-red-600 hover:bg-red-700 text-white font-medium py-2 px-6 rounded transition">
                    进入救援模式
                </button>
            </form>
        @endif

        @if(session('rescue_password'))
            <div class="mt-6 bg-green-50 border border-green-200 rounded p-4">
                <p class="font-medium text-green-800 mb-2">救援系统临时密码（仅显示一次）</p>
                <div class="flex items-center space-x-2">
                    <code class="bg-white border px-3 py-2 rounded font-mono text-lg select-all"
                          id="rescue-password">{{ session('rescue_password') }}</code>
                    <button type="button" onclick="copyPassword()"
                            class="text-green-600 hover:text-green-800 text-sm underline">
                        复制
                    </button>
                </div>
                <p class="text-sm text-green-600 mt-2">
                    用户名：<code class="bg-green-100 px-1 rounded">root</code> ·
                    过期时间：{{ session('rescue_expires_at') }}
                </p>
            </div>
        @endif
    </div>
</div>

@push('scripts')
<script>
    // 倒计时
    @if($inRescue && isset($rescueInfo['remaining_seconds']))
    (function() {
        let remaining = {{ $rescueInfo['remaining_seconds'] }};
        const el = document.getElementById('countdown');
        if (!el) return;

        const timer = setInterval(function() {
            remaining--;
            if (remaining <= 0) {
                clearInterval(timer);
                el.textContent = '已过期';
                location.reload();
                return;
            }
            const h = Math.floor(remaining / 3600);
            const m = Math.floor((remaining % 3600) / 60);
            const s = remaining % 60;
            el.textContent = String(h).padStart(2, '0') + ':' +
                             String(m).padStart(2, '0') + ':' +
                             String(s).padStart(2, '0');
        }, 1000);
    })();
    @endif

    function copyPassword() {
        const text = document.getElementById('rescue-password').textContent;
        navigator.clipboard.writeText(text);
    }
</script>
@endpush
@endsection
