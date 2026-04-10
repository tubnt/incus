{{-- Reserved IP 管理 --}}
@extends('layouts.main')

@section('title', '保留 IP')

@section('content')
<div class="container py-4">

    {{-- 页头 --}}
    <div class="d-flex justify-content-between align-items-center mb-4">
        <div>
            <h4 class="mb-1">保留 IP</h4>
            <p class="text-muted mb-0 small">
                保留 IP 在 VM 删除后不会释放，可绑定到新 VM。按小时计费（¥{{ $hourlyRate }}/小时）。
            </p>
        </div>
    </div>

    @if(empty($reservedIps))
        <div class="card">
            <div class="card-body text-center text-muted py-5">
                <i class="bi bi-globe2 display-4 d-block mb-3"></i>
                <p>没有保留的 IP 地址。</p>
                <p class="small">您可以在 VM 管理页面将已分配的 IP 标记为保留。</p>
            </div>
        </div>
    @else
        <div class="card">
            <div class="table-responsive">
                <table class="table table-hover mb-0">
                    <thead>
                        <tr>
                            <th>IP 地址</th>
                            <th>绑定 VM</th>
                            <th>保留时间</th>
                            <th>已保留时长</th>
                            <th>累计费用</th>
                            <th style="width: 180px;">操作</th>
                        </tr>
                    </thead>
                    <tbody>
                        @foreach($reservedIps as $rip)
                        <tr>
                            <td><code>{{ $rip['ip'] }}</code></td>
                            <td>
                                @if($rip['vm_name'])
                                    <code>{{ $rip['vm_name'] }}</code>
                                @else
                                    <span class="text-muted">未绑定</span>
                                @endif
                            </td>
                            <td>{{ $rip['reserved_at'] }}</td>
                            <td>{{ $rip['reserved_hours'] }} 小时</td>
                            <td>¥{{ $rip['accrued_cost'] }}</td>
                            <td>
                                <div class="d-flex gap-1">
                                    @if(!$rip['vm_name'])
                                        {{-- 绑定到 VM --}}
                                        <form method="POST"
                                              action="{{ route('extensions.incus.reserved-ip.assign', $rip['id']) }}"
                                              class="d-flex gap-1">
                                            @csrf
                                            <select name="vm_name" class="form-select form-select-sm"
                                                    style="width: 140px;" required>
                                                <option value="">选择 VM</option>
                                                @foreach($availableVms as $vm)
                                                    <option value="{{ $vm['name'] }}">{{ $vm['name'] }}</option>
                                                @endforeach
                                            </select>
                                            <button type="submit" class="btn btn-outline-primary btn-sm">绑定</button>
                                        </form>

                                        {{-- 释放 --}}
                                        <form method="POST"
                                              action="{{ route('extensions.incus.reserved-ip.release', $rip['id']) }}"
                                              onsubmit="return confirm('释放后 IP 将不再保留，确认释放 ' + {{ Js::from($rip['ip']) }} + '？')">
                                            @csrf
                                            @method('DELETE')
                                            <button type="submit" class="btn btn-outline-danger btn-sm">释放</button>
                                        </form>
                                    @else
                                        <span class="text-muted small">绑定中，需先在 VM 页面解绑</span>
                                    @endif
                                </div>
                            </td>
                        </tr>
                        @endforeach
                    </tbody>
                </table>
            </div>
        </div>

        {{-- 费用汇总 --}}
        <div class="card mt-3">
            <div class="card-body">
                <div class="d-flex justify-content-between">
                    <span>保留 IP 总数</span>
                    <strong>{{ count($reservedIps) }} 个</strong>
                </div>
                <div class="d-flex justify-content-between mt-1">
                    <span>累计总费用</span>
                    <strong>¥{{ array_sum(array_column($reservedIps, 'accrued_cost')) }}</strong>
                </div>
            </div>
        </div>
    @endif

</div>
@endsection
