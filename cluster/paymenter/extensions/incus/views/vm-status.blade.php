{{-- VM 状态页 --}}
@extends('layouts.main')

@section('title', 'VM 状态 — ' . $vm['name'])

@section('content')
<div class="container py-4">

    {{-- 基本信息卡片 --}}
    <div class="card mb-4">
        <div class="card-header d-flex justify-content-between align-items-center">
            <h5 class="mb-0">{{ $vm['name'] }}</h5>
            <span class="badge bg-{{ $vm['status'] === 'Running' ? 'success' : 'danger' }}">
                {{ $vm['status'] === 'Running' ? '运行中' : '已停止' }}
            </span>
        </div>
        <div class="card-body">
            <div class="row">
                <div class="col-md-6">
                    <table class="table table-borderless mb-0">
                        <tr>
                            <th class="text-muted" style="width: 120px;">IP 地址</th>
                            <td><code>{{ $vm['ip'] }}</code></td>
                        </tr>
                        <tr>
                            <th class="text-muted">网关</th>
                            <td><code>{{ $vm['gateway'] }}</code></td>
                        </tr>
                        <tr>
                            <th class="text-muted">操作系统</th>
                            <td>{{ $vm['os'] }}</td>
                        </tr>
                        <tr>
                            <th class="text-muted">规格</th>
                            <td>{{ $vm['cpu'] }} vCPU / {{ $vm['memory'] }} / {{ $vm['disk'] }}</td>
                        </tr>
                    </table>
                </div>
                <div class="col-md-6">
                    <table class="table table-borderless mb-0">
                        <tr>
                            <th class="text-muted" style="width: 120px;">创建时间</th>
                            <td>{{ $vm['created_at'] }}</td>
                        </tr>
                        <tr>
                            <th class="text-muted">到期时间</th>
                            <td>
                                {{ $vm['expires_at'] }}
                                @if($vm['days_remaining'] <= 7)
                                    <span class="badge bg-warning ms-1">{{ $vm['days_remaining'] }} 天后到期</span>
                                @endif
                            </td>
                        </tr>
                        <tr>
                            <th class="text-muted">月流量</th>
                            <td>
                                {{ $bandwidth['total_formatted'] }} / {{ $bandwidth['quota_formatted'] }}
                                @if($bandwidth['is_throttled'])
                                    <span class="badge bg-danger ms-1">已限速 10Mbps</span>
                                @endif
                            </td>
                        </tr>
                    </table>
                </div>
            </div>
        </div>
    </div>

    {{-- SSH 连接信息 --}}
    <div class="card mb-4">
        <div class="card-header">
            <h6 class="mb-0">SSH 连接</h6>
        </div>
        <div class="card-body">
            <div class="input-group">
                <span class="input-group-text"><i class="bi bi-terminal"></i></span>
                <input type="text" class="form-control font-monospace" readonly
                       value="ssh root@{{ $vm['ip'] }}" id="ssh-command">
                <button class="btn btn-outline-secondary" type="button"
                        onclick="navigator.clipboard.writeText(document.getElementById('ssh-command').value)">
                    复制
                </button>
            </div>
            <small class="text-muted mt-1 d-block">
                默认端口 22。首次连接请使用创建时设置的密码或 SSH 密钥。
            </small>
        </div>
    </div>

    {{-- 资源使用图表占位 --}}
    <div class="card mb-4">
        <div class="card-header d-flex justify-content-between align-items-center">
            <h6 class="mb-0">资源监控</h6>
            <div class="btn-group btn-group-sm" role="group">
                <button type="button" class="btn btn-outline-primary active" data-range="1h">1 小时</button>
                <button type="button" class="btn btn-outline-primary" data-range="24h">24 小时</button>
                <button type="button" class="btn btn-outline-primary" data-range="7d">7 天</button>
                <button type="button" class="btn btn-outline-primary" data-range="30d">30 天</button>
            </div>
        </div>
        <div class="card-body">
            <div class="row">
                <div class="col-md-6 mb-3">
                    <h6 class="text-muted">CPU 使用率</h6>
                    <div id="chart-cpu" class="bg-light rounded d-flex align-items-center justify-content-center"
                         style="height: 200px;">
                        <span class="text-muted">{{ $metrics['cpu'] ?? '加载中...' }}</span>
                    </div>
                </div>
                <div class="col-md-6 mb-3">
                    <h6 class="text-muted">内存使用</h6>
                    <div id="chart-memory" class="bg-light rounded d-flex align-items-center justify-content-center"
                         style="height: 200px;">
                        <span class="text-muted">{{ $metrics['memory'] ?? '加载中...' }}</span>
                    </div>
                </div>
                <div class="col-md-6 mb-3">
                    <h6 class="text-muted">磁盘 IO</h6>
                    <div id="chart-disk" class="bg-light rounded d-flex align-items-center justify-content-center"
                         style="height: 200px;">
                        <span class="text-muted">{{ $metrics['disk_io'] ?? '加载中...' }}</span>
                    </div>
                </div>
                <div class="col-md-6 mb-3">
                    <h6 class="text-muted">网络带宽</h6>
                    <div id="chart-network" class="bg-light rounded d-flex align-items-center justify-content-center"
                         style="height: 200px;">
                        <span class="text-muted">{{ $metrics['network'] ?? '加载中...' }}</span>
                    </div>
                </div>
            </div>
        </div>
    </div>

    {{-- 操作按钮 --}}
    <div class="card">
        <div class="card-header">
            <h6 class="mb-0">操作</h6>
        </div>
        <div class="card-body">
            <div class="d-flex flex-wrap gap-2">
                @if($vm['status'] === 'Running')
                    <form method="POST" action="{{ route('extensions.incus.stop', $vm['id']) }}">
                        @csrf
                        <button type="submit" class="btn btn-warning"
                                onclick="return confirm('确认停止 VM？')">
                            <i class="bi bi-stop-circle"></i> 停止
                        </button>
                    </form>
                    <form method="POST" action="{{ route('extensions.incus.reboot', $vm['id']) }}">
                        @csrf
                        <button type="submit" class="btn btn-info"
                                onclick="return confirm('确认重启 VM？')">
                            <i class="bi bi-arrow-clockwise"></i> 重启
                        </button>
                    </form>
                @else
                    <form method="POST" action="{{ route('extensions.incus.start', $vm['id']) }}">
                        @csrf
                        <button type="submit" class="btn btn-success">
                            <i class="bi bi-play-circle"></i> 启动
                        </button>
                    </form>
                @endif

                <a href="{{ route('extensions.incus.console', $vm['id']) }}" class="btn btn-dark" target="_blank">
                    <i class="bi bi-terminal"></i> VNC 控制台
                </a>

                <button type="button" class="btn btn-danger"
                        data-bs-toggle="modal" data-bs-target="#reinstall-modal">
                    <i class="bi bi-arrow-repeat"></i> 重装系统
                </button>
            </div>
        </div>
    </div>

</div>

{{-- 重装系统确认弹窗 --}}
<div class="modal fade" id="reinstall-modal" tabindex="-1">
    <div class="modal-dialog">
        <div class="modal-content">
            <div class="modal-header">
                <h5 class="modal-title">重装系统</h5>
                <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
            </div>
            <form method="POST" action="{{ route('extensions.incus.reinstall', $vm['id']) }}">
                @csrf
                <div class="modal-body">
                    <div class="alert alert-danger">
                        <strong>警告：</strong>重装系统将删除所有数据，此操作不可撤销！
                    </div>
                    <div class="mb-3">
                        <label class="form-label">请输入 VM 名称 <strong>{{ $vm['name'] }}</strong> 以确认</label>
                        <input type="text" class="form-control" name="confirm_name" required
                               pattern="{{ preg_replace('/[.*+?^${}()|[\]\\\\]/', '\\\\$0', $vm['name']) }}"
                               placeholder="{{ $vm['name'] }}">
                    </div>
                </div>
                <div class="modal-footer">
                    <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">取消</button>
                    <button type="submit" class="btn btn-danger">确认重装</button>
                </div>
            </form>
        </div>
    </div>
</div>
@endsection
