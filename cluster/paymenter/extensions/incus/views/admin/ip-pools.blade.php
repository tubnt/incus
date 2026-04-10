{{-- 管理后台 — IP 池可视化 --}}
@extends('admin.layouts.app')

@section('title', 'IP 池管理')

@section('content')
<div class="container-fluid py-4">
    <div class="d-flex justify-content-between align-items-center mb-4">
        <h4 class="mb-0">IP 池管理</h4>
        <a href="{{ route('admin.incus.dashboard') }}" class="btn btn-outline-secondary btn-sm">返回概览</a>
    </div>

    @foreach($pools as $pool)
    <div class="card border-0 shadow-sm mb-4">
        <div class="card-header bg-white d-flex justify-content-between align-items-center">
            <div>
                <h5 class="mb-0 d-inline">{{ $pool['name'] }}</h5>
                <span class="text-muted ms-2">{{ $pool['subnet'] }}</span>
                <span class="text-muted ms-2">网关: {{ $pool['gateway'] }}</span>
            </div>
            <div>
                <span class="badge bg-primary">使用率 {{ $pool['utilization'] }}%</span>
                @if($pool['low_warning'])
                    <span class="badge bg-danger ms-1">IP 余量不足</span>
                @endif
            </div>
        </div>

        <div class="card-body">
            {{-- 统计概览 --}}
            <div class="row g-3 mb-4">
                <div class="col-md-3">
                    <div class="border rounded p-3 text-center">
                        <div class="text-muted small">总计</div>
                        <div class="h4 mb-0">{{ $pool['total'] }}</div>
                    </div>
                </div>
                <div class="col-md-3">
                    <div class="border rounded p-3 text-center border-success">
                        <div class="text-success small">可用</div>
                        <div class="h4 mb-0 text-success">{{ $pool['stats']['available'] }}</div>
                    </div>
                </div>
                <div class="col-md-3">
                    <div class="border rounded p-3 text-center border-primary">
                        <div class="text-primary small">已分配</div>
                        <div class="h4 mb-0 text-primary">{{ $pool['stats']['allocated'] }}</div>
                    </div>
                </div>
                <div class="col-md-2">
                    <div class="border rounded p-3 text-center border-warning">
                        <div class="text-warning small">冷却中</div>
                        <div class="h4 mb-0 text-warning">{{ $pool['stats']['cooldown'] }}</div>
                    </div>
                </div>
                <div class="col-md-1">
                    <div class="border rounded p-3 text-center">
                        <div class="text-muted small">保留</div>
                        <div class="h4 mb-0">{{ $pool['stats']['reserved'] }}</div>
                    </div>
                </div>
            </div>

            {{-- IP 地址可视化网格 --}}
            <h6 class="mb-2">地址分布</h6>
            <div class="d-flex flex-wrap gap-1 mb-3">
                @foreach($pool['addresses'] as $addr)
                    @php
                        $colorClass = match($addr['status']) {
                            'available' => 'bg-success',
                            'allocated' => 'bg-primary',
                            'cooldown'  => 'bg-warning',
                            'reserved'  => 'bg-secondary',
                            default     => 'bg-light',
                        };
                        $lastOctet = last(explode('.', $addr['ip']));
                    @endphp
                    <div class="rounded text-white text-center {{ $colorClass }}"
                         style="width: 42px; height: 36px; line-height: 36px; font-size: 12px; cursor: pointer;"
                         data-bs-toggle="tooltip"
                         data-bs-placement="top"
                         data-ip="{{ $addr['ip'] }}"
                         data-addr-status="{{ $addr['status'] }}"
                         data-vm-name="{{ $addr['vm_name'] ?? '' }}"
                         data-cooldown="{{ $addr['cooldown_until'] ?? '' }}"
                         title="{{ $addr['ip'] }} | {{ $addr['status'] }}">
                        .{{ $lastOctet }}
                    </div>
                @endforeach
            </div>

            {{-- 图例 --}}
            <div class="d-flex gap-3 mb-3">
                <small><span class="badge bg-success">&nbsp;</span> 可用</small>
                <small><span class="badge bg-primary">&nbsp;</span> 已分配</small>
                <small><span class="badge bg-warning">&nbsp;</span> 冷却中</small>
                <small><span class="badge bg-secondary">&nbsp;</span> 保留</small>
            </div>

            {{-- 已分配 IP 详细列表 --}}
            <h6 class="mb-2">已分配地址</h6>
            <div class="table-responsive">
                <table class="table table-sm table-hover mb-0">
                    <thead>
                        <tr>
                            <th>IP 地址</th>
                            <th>状态</th>
                            <th>VM 名称</th>
                            <th>订单 ID</th>
                            <th>分配时间</th>
                            <th>冷却截止</th>
                        </tr>
                    </thead>
                    <tbody>
                        @foreach($pool['addresses'] as $addr)
                            @if($addr['status'] !== 'available')
                            <tr>
                                <td><code>{{ $addr['ip'] }}</code></td>
                                <td>
                                    @php
                                        $badgeClass = match($addr['status']) {
                                            'allocated' => 'bg-primary',
                                            'cooldown'  => 'bg-warning text-dark',
                                            'reserved'  => 'bg-secondary',
                                            default     => 'bg-light text-dark',
                                        };
                                        $statusLabel = match($addr['status']) {
                                            'allocated' => '已分配',
                                            'cooldown'  => '冷却中',
                                            'reserved'  => '保留',
                                            default     => $addr['status'],
                                        };
                                    @endphp
                                    <span class="badge {{ $badgeClass }}">{{ $statusLabel }}</span>
                                </td>
                                <td>{{ $addr['vm_name'] ?: '-' }}</td>
                                <td>
                                    @if($addr['order_id'])
                                        <a href="{{ route('admin.orders.show', $addr['order_id']) }}">
                                            #{{ $addr['order_id'] }}
                                        </a>
                                    @else
                                        -
                                    @endif
                                </td>
                                <td>{{ $addr['allocated_at'] ? \Carbon\Carbon::parse($addr['allocated_at'])->format('Y-m-d H:i') : '-' }}</td>
                                <td>
                                    @if($addr['cooldown_until'])
                                        {{ \Carbon\Carbon::parse($addr['cooldown_until'])->format('Y-m-d H:i') }}
                                        <small class="text-muted">
                                            ({{ \Carbon\Carbon::parse($addr['cooldown_until'])->diffForHumans() }})
                                        </small>
                                    @else
                                        -
                                    @endif
                                </td>
                            </tr>
                            @endif
                        @endforeach
                    </tbody>
                </table>
            </div>
        </div>
    </div>
    @endforeach
</div>

@push('scripts')
<script>
    // 启用 Bootstrap tooltip（使用 data 属性构建安全内容，避免 XSS）
    document.addEventListener('DOMContentLoaded', function() {
        var tooltipTriggerList = [].slice.call(document.querySelectorAll('[data-bs-toggle="tooltip"]'));
        tooltipTriggerList.map(function(el) {
            var ip = el.getAttribute('data-ip') || '';
            var status = el.getAttribute('data-addr-status') || '';
            var vmName = el.getAttribute('data-vm-name') || '';
            var cooldown = el.getAttribute('data-cooldown') || '';

            var lines = [ip, '状态: ' + status];
            if (vmName) lines.push('VM: ' + vmName);
            if (cooldown) lines.push('冷却至: ' + cooldown);

            // 用 textContent 安全构建 HTML 避免 XSS
            var container = document.createElement('div');
            lines.forEach(function(line, i) {
                if (i > 0) container.appendChild(document.createElement('br'));
                container.appendChild(document.createTextNode(line));
            });

            return new bootstrap.Tooltip(el, {
                html: true,
                title: container.innerHTML
            });
        });
    });
</script>
@endpush
@endsection
