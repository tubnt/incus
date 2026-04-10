{{-- rDNS 反向 DNS 管理界面 --}}
@extends('layouts.app')

@section('title', 'rDNS 管理')

@section('content')
<div class="container mx-auto px-4 py-6">
    <h2 class="text-2xl font-bold mb-6">反向 DNS 管理</h2>

    {{-- 操作结果提示 --}}
    @if(session('success'))
        <div class="bg-green-100 border border-green-400 text-green-700 px-4 py-3 rounded mb-4">
            {{ session('success') }}
        </div>
    @endif
    @if(session('error'))
        <div class="bg-red-100 border border-red-400 text-red-700 px-4 py-3 rounded mb-4">
            {{ session('error') }}
        </div>
    @endif

    {{-- IP 地址列表及 rDNS 状态 --}}
    <div class="bg-white shadow rounded-lg overflow-hidden">
        <table class="min-w-full divide-y divide-gray-200">
            <thead class="bg-gray-50">
                <tr>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase">IP 地址</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase">虚拟机</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase">rDNS 记录</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase">操作</th>
                </tr>
            </thead>
            <tbody class="bg-white divide-y divide-gray-200">
                @forelse($ipAddresses as $ip)
                <tr>
                    <td class="px-6 py-4 whitespace-nowrap font-mono text-sm">{{ $ip->ip }}</td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-600">{{ $ip->vm_name ?? '-' }}</td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm">
                        @if($ip->rdns_hostname)
                            <span class="text-green-600">{{ $ip->rdns_hostname }}</span>
                        @else
                            <span class="text-gray-400">未设置</span>
                        @endif
                    </td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm">
                        @if($ip->status === 'allocated')
                            <button
                                type="button"
                                class="text-blue-600 hover:text-blue-800 mr-3"
                                onclick="openRdnsModal({{ Js::from($ip->ip) }}, {{ Js::from($ip->rdns_hostname ?? '') }})"
                            >
                                {{ $ip->rdns_hostname ? '修改' : '设置' }}
                            </button>
                            @if($ip->rdns_hostname)
                                <form method="POST" action="{{ route('rdns.delete') }}" class="inline">
                                    @csrf
                                    @method('DELETE')
                                    <input type="hidden" name="ip" value="{{ $ip->ip }}">
                                    <button
                                        type="submit"
                                        class="text-red-600 hover:text-red-800"
                                        onclick="return confirm('确认删除 ' + {{ Js::from($ip->ip) }} + ' 的 rDNS 记录？')"
                                    >
                                        删除
                                    </button>
                                </form>
                            @endif
                        @else
                            <span class="text-gray-400">-</span>
                        @endif
                    </td>
                </tr>
                @empty
                <tr>
                    <td colspan="4" class="px-6 py-8 text-center text-gray-500">暂无 IP 地址记录</td>
                </tr>
                @endforelse
            </tbody>
        </table>
    </div>
</div>

{{-- rDNS 设置弹窗 --}}
<div id="rdns-modal" class="fixed inset-0 bg-black bg-opacity-50 hidden items-center justify-center z-50">
    <div class="bg-white rounded-lg shadow-xl w-full max-w-md mx-4">
        <div class="px-6 py-4 border-b">
            <h3 class="text-lg font-semibold">设置反向 DNS</h3>
        </div>
        <form method="POST" action="{{ route('rdns.set') }}" id="rdns-form">
            @csrf
            <div class="px-6 py-4 space-y-4">
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">IP 地址</label>
                    <input
                        type="text"
                        name="ip"
                        id="rdns-ip"
                        class="w-full border rounded px-3 py-2 bg-gray-100"
                        readonly
                    >
                </div>
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">主机名 (FQDN)</label>
                    <input
                        type="text"
                        name="hostname"
                        id="rdns-hostname"
                        class="w-full border rounded px-3 py-2"
                        placeholder="mail.example.com"
                        required
                        pattern="^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\.?$"
                    >
                    <p class="text-xs text-gray-500 mt-1">
                        请确保该域名的 A/AAAA 记录已指向此 IP 地址
                    </p>
                </div>
            </div>
            <div class="px-6 py-4 border-t flex justify-end space-x-3">
                <button
                    type="button"
                    class="px-4 py-2 text-gray-600 hover:text-gray-800"
                    onclick="closeRdnsModal()"
                >
                    取消
                </button>
                <button
                    type="submit"
                    class="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
                >
                    保存
                </button>
            </div>
        </form>
    </div>
</div>

<script>
function openRdnsModal(ip, hostname) {
    document.getElementById('rdns-ip').value = ip;
    document.getElementById('rdns-hostname').value = hostname;
    document.getElementById('rdns-modal').classList.remove('hidden');
    document.getElementById('rdns-modal').classList.add('flex');
}

function closeRdnsModal() {
    document.getElementById('rdns-modal').classList.add('hidden');
    document.getElementById('rdns-modal').classList.remove('flex');
}

// 点击遮罩关闭
document.getElementById('rdns-modal').addEventListener('click', function(e) {
    if (e.target === this) closeRdnsModal();
});
</script>
@endsection
