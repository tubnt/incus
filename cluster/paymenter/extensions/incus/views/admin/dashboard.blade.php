{{-- 管理后台 — 概览仪表盘 --}}
@extends('admin.layouts.app')

@section('title', '云主机管理 — 概览')

@section('content')
<div class="container-fluid py-4">
    <h4 class="mb-4">云主机管理概览</h4>

    {{-- 统计卡片 --}}
    <div class="row g-3 mb-4">
        <div class="col-md-3">
            <div class="card border-0 shadow-sm">
                <div class="card-body">
                    <div class="d-flex justify-content-between align-items-center">
                        <div>
                            <p class="text-muted mb-1">用户总数</p>
                            <h3 class="mb-0">{{ $overview['users']['total'] ?? 0 }}</h3>
                            <small class="text-success">本月新增 {{ $overview['users']['new_month'] ?? 0 }}</small>
                        </div>
                        <div class="text-primary" style="font-size: 2rem;">
                            <i class="bi bi-people"></i>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <div class="col-md-3">
            <div class="card border-0 shadow-sm">
                <div class="card-body">
                    <div class="d-flex justify-content-between align-items-center">
                        <div>
                            <p class="text-muted mb-1">运行中 VM</p>
                            <h3 class="mb-0">{{ $overview['vms']['running'] ?? 0 }}</h3>
                            <small class="text-warning">即将到期 {{ $overview['vms']['expiring'] ?? 0 }}</small>
                        </div>
                        <div class="text-success" style="font-size: 2rem;">
                            <i class="bi bi-hdd-stack"></i>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <div class="col-md-3">
            <div class="card border-0 shadow-sm">
                <div class="card-body">
                    <div class="d-flex justify-content-between align-items-center">
                        <div>
                            <p class="text-muted mb-1">IP 使用率</p>
                            <h3 class="mb-0">
                                {{ $overview['ips']['total'] > 0
                                    ? round($overview['ips']['allocated'] / $overview['ips']['total'] * 100, 1)
                                    : 0 }}%
                            </h3>
                            <small class="text-muted">
                                可用 {{ $overview['ips']['available'] ?? 0 }} /
                                总计 {{ $overview['ips']['total'] ?? 0 }}
                            </small>
                        </div>
                        <div class="text-info" style="font-size: 2rem;">
                            <i class="bi bi-globe"></i>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <div class="col-md-3">
            <div class="card border-0 shadow-sm">
                <div class="card-body">
                    <div class="d-flex justify-content-between align-items-center">
                        <div>
                            <p class="text-muted mb-1">本月收入</p>
                            <h3 class="mb-0">${{ number_format($overview['revenue']['this_month'] ?? 0, 2) }}</h3>
                            <small class="text-muted">
                                上月 ${{ number_format($overview['revenue']['last_month'] ?? 0, 2) }}
                            </small>
                        </div>
                        <div class="text-warning" style="font-size: 2rem;">
                            <i class="bi bi-currency-dollar"></i>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <div class="row g-3">
        {{-- 客户搜索 --}}
        <div class="col-md-6">
            <div class="card border-0 shadow-sm">
                <div class="card-header bg-white">
                    <h6 class="mb-0">客户搜索</h6>
                </div>
                <div class="card-body">
                    <form method="GET" action="{{ route('admin.incus.customers.search') }}">
                        <div class="input-group">
                            <input type="text" name="q" class="form-control"
                                   placeholder="搜索邮箱 / 用户名 / VM 名称 / IP 地址"
                                   value="{{ request('q') }}">
                            <button type="submit" class="btn btn-primary">搜索</button>
                        </div>
                    </form>

                    @if(isset($customers) && count($customers) > 0)
                        <table class="table table-sm mt-3 mb-0">
                            <thead>
                                <tr>
                                    <th>用户</th>
                                    <th>邮箱</th>
                                    <th>VM</th>
                                    <th>余额</th>
                                </tr>
                            </thead>
                            <tbody>
                                @foreach($customers as $customer)
                                <tr>
                                    <td>{{ $customer->name }}</td>
                                    <td>{{ $customer->email }}</td>
                                    <td>
                                        <small>{{ $customer->vm_names ?: '-' }}</small>
                                    </td>
                                    <td>${{ number_format($customer->balance, 2) }}</td>
                                </tr>
                                @endforeach
                            </tbody>
                        </table>
                    @endif
                </div>
            </div>
        </div>

        {{-- VM 全局视图 --}}
        <div class="col-md-6">
            <div class="card border-0 shadow-sm">
                <div class="card-header bg-white d-flex justify-content-between align-items-center">
                    <h6 class="mb-0">VM 全局视图</h6>
                    <a href="{{ route('admin.incus.vms') }}" class="btn btn-sm btn-outline-primary">查看全部</a>
                </div>
                <div class="card-body">
                    {{-- 节点汇总 --}}
                    @if(isset($nodes))
                    <div class="mb-3">
                        @foreach($nodes as $node)
                        <span class="badge bg-light text-dark me-2 p-2">
                            <strong>{{ $node['name'] }}</strong>:
                            <span class="text-success">{{ $node['running'] }} 运行</span> /
                            <span class="text-secondary">{{ $node['stopped'] }} 停止</span>
                        </span>
                        @endforeach
                    </div>
                    @endif

                    {{-- 最近活动 VM --}}
                    @if(isset($recentVms))
                    <table class="table table-sm mb-0">
                        <thead>
                            <tr>
                                <th>VM</th>
                                <th>状态</th>
                                <th>节点</th>
                                <th>用户</th>
                                <th>到期</th>
                            </tr>
                        </thead>
                        <tbody>
                            @foreach($recentVms as $vm)
                            <tr>
                                <td><code>{{ $vm['name'] }}</code></td>
                                <td>
                                    @if($vm['status'] === 'Running')
                                        <span class="badge bg-success">运行中</span>
                                    @elseif($vm['status'] === 'Stopped')
                                        <span class="badge bg-secondary">已停止</span>
                                    @elseif($vm['status'] === 'missing')
                                        <span class="badge bg-danger">异常</span>
                                    @else
                                        <span class="badge bg-warning">{{ $vm['status'] }}</span>
                                    @endif
                                </td>
                                <td>{{ $vm['node'] }}</td>
                                <td>{{ $vm['user_name'] }}</td>
                                <td>
                                    @if($vm['expires_at'])
                                        @if(strtotime($vm['expires_at']) < time() + 86400 * 3)
                                            <span class="text-danger">{{ \Carbon\Carbon::parse($vm['expires_at'])->format('m-d') }}</span>
                                        @else
                                            {{ \Carbon\Carbon::parse($vm['expires_at'])->format('m-d') }}
                                        @endif
                                    @else
                                        -
                                    @endif
                                </td>
                            </tr>
                            @endforeach
                        </tbody>
                    </table>
                    @endif
                </div>
            </div>
        </div>
    </div>

    {{-- IP 池快速状态 + 收入趋势 --}}
    <div class="row g-3 mt-1">
        <div class="col-md-6">
            <div class="card border-0 shadow-sm">
                <div class="card-header bg-white d-flex justify-content-between align-items-center">
                    <h6 class="mb-0">IP 池状态</h6>
                    <a href="{{ route('admin.incus.ip-pools') }}" class="btn btn-sm btn-outline-primary">详情</a>
                </div>
                <div class="card-body">
                    @if(isset($ipPools))
                        @foreach($ipPools as $pool)
                        <div class="mb-3">
                            <div class="d-flex justify-content-between mb-1">
                                <strong>{{ $pool['name'] }}</strong>
                                <span>{{ $pool['subnet'] }}</span>
                            </div>
                            <div class="progress" style="height: 20px;">
                                <div class="progress-bar bg-success" style="width: {{ $pool['stats']['allocated'] / max(1, $pool['total']) * 100 }}%">
                                    已用 {{ $pool['stats']['allocated'] }}
                                </div>
                                <div class="progress-bar bg-warning" style="width: {{ $pool['stats']['cooldown'] / max(1, $pool['total']) * 100 }}%">
                                    冷却 {{ $pool['stats']['cooldown'] }}
                                </div>
                                <div class="progress-bar bg-secondary" style="width: {{ $pool['stats']['reserved'] / max(1, $pool['total']) * 100 }}%">
                                    保留 {{ $pool['stats']['reserved'] }}
                                </div>
                            </div>
                            @if($pool['low_warning'])
                                <small class="text-danger mt-1 d-block">IP 余量不足 10%</small>
                            @endif
                        </div>
                        @endforeach
                    @endif
                </div>
            </div>
        </div>

        <div class="col-md-6">
            <div class="card border-0 shadow-sm">
                <div class="card-header bg-white d-flex justify-content-between align-items-center">
                    <h6 class="mb-0">收入趋势</h6>
                    <a href="{{ route('admin.incus.revenue') }}" class="btn btn-sm btn-outline-primary">详细报表</a>
                </div>
                <div class="card-body">
                    @if(isset($revenueChart))
                    <canvas id="revenueChart" height="200"></canvas>
                    <script>
                        document.addEventListener('DOMContentLoaded', function() {
                            const ctx = document.getElementById('revenueChart').getContext('2d');
                            new Chart(ctx, {
                                type: 'bar',
                                data: {
                                    labels: @json(array_column($revenueChart, 'month')),
                                    datasets: [{
                                        label: '新订单',
                                        data: @json(array_column($revenueChart, 'new_revenue')),
                                        backgroundColor: 'rgba(54, 162, 235, 0.7)',
                                    }, {
                                        label: '续费',
                                        data: @json(array_column($revenueChart, 'renewal_revenue')),
                                        backgroundColor: 'rgba(75, 192, 192, 0.7)',
                                    }],
                                },
                                options: {
                                    responsive: true,
                                    scales: { x: { stacked: true }, y: { stacked: true, beginAtZero: true } },
                                },
                            });
                        });
                    </script>
                    @endif
                </div>
            </div>
        </div>
    </div>
</div>
@endsection
