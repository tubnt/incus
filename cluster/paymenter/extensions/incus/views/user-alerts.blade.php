{{-- 用户 VM 告警管理页面 --}}
@extends('layouts.app')

@section('title', 'VM 告警管理')

@section('content')
<div class="container mx-auto px-4 py-6">
    <div class="flex justify-between items-center mb-6">
        <h1 class="text-2xl font-bold">VM 告警管理</h1>
        <button onclick="document.getElementById('create-modal').classList.remove('hidden')"
                class="bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded-lg transition">
            + 创建告警
        </button>
    </div>

    {{-- 告警列表 --}}
    <div class="bg-white rounded-lg shadow overflow-hidden">
        <table class="min-w-full divide-y divide-gray-200">
            <thead class="bg-gray-50">
                <tr>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">VM</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">指标</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">条件</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">渠道</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">状态</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">上次通知</th>
                    <th class="px-6 py-3 text-right text-xs font-medium text-gray-500 uppercase tracking-wider">操作</th>
                </tr>
            </thead>
            <tbody class="bg-white divide-y divide-gray-200">
                @forelse($alerts as $alert)
                <tr>
                    <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-gray-900">
                        {{ $alert->vm_name }}
                    </td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-600">
                        @switch($alert->metric)
                            @case('cpu_percent')    CPU 使用率 @break
                            @case('memory_percent') 内存使用率 @break
                            @case('bandwidth_in')   入站带宽 @break
                            @case('bandwidth_out')  出站带宽 @break
                            @case('disk_percent')   磁盘使用率 @break
                        @endswitch
                    </td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-600">
                        {{ $alert->direction === 'above' ? '高于' : '低于' }} {{ $alert->threshold }}
                    </td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-600">
                        <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium
                            {{ $alert->channel === 'email' ? 'bg-blue-100 text-blue-800' : 'bg-purple-100 text-purple-800' }}">
                            {{ $alert->channel === 'email' ? '邮件' : 'Webhook' }}
                        </span>
                    </td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm">
                        <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium
                            {{ $alert->enabled ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-500' }}">
                            {{ $alert->enabled ? '启用' : '已禁用' }}
                        </span>
                    </td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-500">
                        {{ $alert->last_notified_at ? \Carbon\Carbon::parse($alert->last_notified_at)->diffForHumans() : '从未' }}
                    </td>
                    <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium space-x-2">
                        <form method="POST" action="{{ route('incus.alerts.toggle', $alert->id) }}" class="inline">
                            @csrf @method('PATCH')
                            <input type="hidden" name="enabled" value="{{ $alert->enabled ? '0' : '1' }}">
                            <button type="submit" class="text-yellow-600 hover:text-yellow-900">
                                {{ $alert->enabled ? '禁用' : '启用' }}
                            </button>
                        </form>
                        <form method="POST" action="{{ route('incus.alerts.destroy', $alert->id) }}" class="inline"
                              onsubmit="return confirm('确定删除此告警规则？')">
                            @csrf @method('DELETE')
                            <button type="submit" class="text-red-600 hover:text-red-900">删除</button>
                        </form>
                    </td>
                </tr>
                @empty
                <tr>
                    <td colspan="7" class="px-6 py-12 text-center text-gray-500">
                        暂无告警规则，点击右上角「创建告警」按钮添加
                    </td>
                </tr>
                @endforelse
            </tbody>
        </table>
    </div>
</div>

{{-- 创建告警弹窗 --}}
<div id="create-modal" class="hidden fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-50">
    <div class="bg-white rounded-lg shadow-xl w-full max-w-md p-6">
        <div class="flex justify-between items-center mb-4">
            <h2 class="text-lg font-bold">创建告警规则</h2>
            <button onclick="document.getElementById('create-modal').classList.add('hidden')"
                    class="text-gray-400 hover:text-gray-600">&times;</button>
        </div>

        <form method="POST" action="{{ route('incus.alerts.store') }}">
            @csrf

            <div class="space-y-4">
                {{-- VM 选择 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">虚拟机</label>
                    <select name="vm_name" required
                            class="w-full border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500">
                        <option value="">请选择 VM</option>
                        @foreach($vms as $vm)
                            <option value="{{ $vm }}">{{ $vm }}</option>
                        @endforeach
                    </select>
                </div>

                {{-- 指标 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">监控指标</label>
                    <select name="metric" required
                            class="w-full border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500">
                        <option value="cpu_percent">CPU 使用率 (%)</option>
                        <option value="memory_percent">内存使用率 (%)</option>
                        <option value="bandwidth_in">入站带宽 (bytes)</option>
                        <option value="bandwidth_out">出站带宽 (bytes)</option>
                        <option value="disk_percent">磁盘使用率 (%)</option>
                    </select>
                </div>

                {{-- 方向 + 阈值 --}}
                <div class="flex space-x-3">
                    <div class="w-1/3">
                        <label class="block text-sm font-medium text-gray-700 mb-1">条件</label>
                        <select name="direction" required
                                class="w-full border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500">
                            <option value="above">高于</option>
                            <option value="below">低于</option>
                        </select>
                    </div>
                    <div class="w-2/3">
                        <label class="block text-sm font-medium text-gray-700 mb-1">阈值</label>
                        <input type="number" name="threshold" step="0.01" required
                               class="w-full border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500"
                               placeholder="例如: 80">
                    </div>
                </div>

                {{-- 通知渠道 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">通知渠道</label>
                    <select name="channel" id="channel-select" required
                            onchange="document.getElementById('webhook-field').classList.toggle('hidden', this.value !== 'webhook')"
                            class="w-full border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500">
                        <option value="email">邮件</option>
                        <option value="webhook">Webhook</option>
                    </select>
                </div>

                {{-- Webhook URL（条件显示）--}}
                <div id="webhook-field" class="hidden">
                    <label class="block text-sm font-medium text-gray-700 mb-1">Webhook URL</label>
                    <input type="url" name="webhook_url"
                           class="w-full border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500"
                           placeholder="https://example.com/webhook">
                </div>
            </div>

            <div class="mt-6 flex justify-end space-x-3">
                <button type="button"
                        onclick="document.getElementById('create-modal').classList.add('hidden')"
                        class="px-4 py-2 text-gray-700 bg-gray-100 hover:bg-gray-200 rounded-lg transition">
                    取消
                </button>
                <button type="submit"
                        class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg transition">
                    创建
                </button>
            </div>
        </form>
    </div>
</div>
@endsection
