{{-- VPC 私有网络管理 --}}
@extends('layouts.main')

@section('title', 'VPC 私有网络')

@section('content')
<div class="container py-4">

    {{-- 页头 --}}
    <div class="d-flex justify-content-between align-items-center mb-4">
        <h4 class="mb-0">VPC 私有网络</h4>
        <button type="button" class="btn btn-primary" data-bs-toggle="modal" data-bs-target="#create-vpc-modal">
            <i class="bi bi-plus-lg"></i> 创建 VPC
        </button>
    </div>

    @if(empty($vpcs))
        <div class="card">
            <div class="card-body text-center text-muted py-5">
                <i class="bi bi-diagram-3 display-4 d-block mb-3"></i>
                <p>还没有创建 VPC 私有网络。</p>
                <p class="small">VPC 可让您的多台 VM 通过私有网络互相通信，与其他用户完全隔离。</p>
            </div>
        </div>
    @else
        @foreach($vpcs as $vpc)
        <div class="card mb-3">
            <div class="card-header d-flex justify-content-between align-items-center">
                <div>
                    <h5 class="mb-0 d-inline">{{ $vpc['name'] }}</h5>
                    <code class="ms-2">{{ $vpc['subnet'] }}</code>
                </div>
                <div class="d-flex align-items-center gap-2">
                    <span class="badge bg-secondary">{{ $vpc['member_count'] }} 台 VM</span>
                    @if($vpc['member_count'] === 0)
                    <form method="POST" action="{{ route('extensions.incus.vpc.delete', $vpc['id']) }}"
                          onsubmit="return confirm('确认删除 VPC「{{ $vpc['name'] }}」？')">
                        @csrf
                        @method('DELETE')
                        <button type="submit" class="btn btn-outline-danger btn-sm">
                            <i class="bi bi-trash"></i> 删除
                        </button>
                    </form>
                    @endif
                </div>
            </div>
            <div class="card-body">
                {{-- 成员列表 --}}
                @if($vpc['member_count'] > 0)
                <table class="table table-sm mb-3">
                    <thead>
                        <tr>
                            <th>VM 名称</th>
                            <th>私有 IP</th>
                            <th>加入时间</th>
                            <th style="width: 80px;">操作</th>
                        </tr>
                    </thead>
                    <tbody>
                        @foreach($vpc['members'] as $member)
                        <tr>
                            <td><code>{{ $member['vm_name'] }}</code></td>
                            <td><code>{{ $member['private_ip'] ?? '分配中...' }}</code></td>
                            <td>{{ $member['joined_at'] }}</td>
                            <td>
                                <form method="POST"
                                      action="{{ route('extensions.incus.vpc.detach', [$vpc['id'], $member['vm_name']]) }}"
                                      onsubmit="return confirm('确认将 {{ $member['vm_name'] }} 移出 VPC？')">
                                    @csrf
                                    @method('DELETE')
                                    <button type="submit" class="btn btn-outline-danger btn-sm">移除</button>
                                </form>
                            </td>
                        </tr>
                        @endforeach
                    </tbody>
                </table>
                @else
                <p class="text-muted mb-3">暂无 VM 加入此 VPC。</p>
                @endif

                {{-- 添加 VM --}}
                <form method="POST" action="{{ route('extensions.incus.vpc.attach', $vpc['id']) }}" class="d-flex gap-2">
                    @csrf
                    <select name="vm_name" class="form-select form-select-sm" style="max-width: 250px;" required>
                        <option value="">选择 VM...</option>
                        @foreach($availableVms as $vm)
                            <option value="{{ $vm['name'] }}">{{ $vm['name'] }} ({{ $vm['ip'] }})</option>
                        @endforeach
                    </select>
                    <button type="submit" class="btn btn-outline-primary btn-sm">
                        <i class="bi bi-plus"></i> 添加 VM
                    </button>
                </form>
            </div>
        </div>
        @endforeach
    @endif

</div>

{{-- 创建 VPC 弹窗 --}}
<div class="modal fade" id="create-vpc-modal" tabindex="-1">
    <div class="modal-dialog">
        <div class="modal-content">
            <div class="modal-header">
                <h5 class="modal-title">创建 VPC 私有网络</h5>
                <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
            </div>
            <form method="POST" action="{{ route('extensions.incus.vpc.create') }}">
                @csrf
                <div class="modal-body">
                    <div class="mb-3">
                        <label class="form-label">名称</label>
                        <input type="text" class="form-control" name="name" required
                               placeholder="如：内网集群" maxlength="64">
                    </div>
                    <div class="mb-3">
                        <label class="form-label">私有网段</label>
                        <select name="subnet" class="form-select" required>
                            @for($i = 1; $i <= 254; $i++)
                                <option value="10.{{ $i }}.0.0/16">10.{{ $i }}.0.0/16</option>
                            @endfor
                        </select>
                        <div class="form-text">
                            每个 VPC 分配一个 /16 私有网段，VPC 内 VM 通过 DHCP 自动获取 IP。
                        </div>
                    </div>
                </div>
                <div class="modal-footer">
                    <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">取消</button>
                    <button type="submit" class="btn btn-primary">创建</button>
                </div>
            </form>
        </div>
    </div>
</div>
@endsection
