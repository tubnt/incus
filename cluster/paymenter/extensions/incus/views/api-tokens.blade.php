{{-- Incus Extension — API Token 管理界面 --}}
@extends('layouts.app')

@section('title', 'API Token 管理')

@section('content')
<div class="container mx-auto px-4 py-6 max-w-4xl">

    {{-- 页头 --}}
    <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold">API Token 管理</h1>
        <button onclick="document.getElementById('create-modal').classList.remove('hidden')"
                class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition">
            + 创建 Token
        </button>
    </div>

    {{-- 新建 Token 成功提示（仅显示一次） --}}
    @if(session('new_token'))
    <div class="mb-6 p-4 bg-green-50 border border-green-200 rounded-lg" id="token-reveal">
        <div class="flex items-start gap-3">
            <svg class="w-5 h-5 text-green-600 mt-0.5 shrink-0" fill="currentColor" viewBox="0 0 20 20">
                <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd"/>
            </svg>
            <div class="flex-1">
                <p class="font-semibold text-green-800 mb-2">Token 已创建 — 请立即复制保存</p>
                <div class="flex items-center gap-2">
                    <code class="flex-1 px-3 py-2 bg-white border rounded font-mono text-sm break-all select-all"
                          id="new-token-value">{{ session('new_token') }}</code>
                    <button onclick="copyToken()" class="px-3 py-2 bg-green-600 text-white rounded hover:bg-green-700 text-sm">
                        复制
                    </button>
                </div>
                <p class="text-sm text-green-700 mt-2">此 Token 不会再次显示，关闭后无法找回。</p>
            </div>
        </div>
    </div>
    @endif

    {{-- Token 列表 --}}
    <div class="bg-white rounded-lg shadow overflow-hidden">
        <table class="w-full text-left">
            <thead class="bg-gray-50 border-b">
                <tr>
                    <th class="px-4 py-3 text-sm font-medium text-gray-600">名称</th>
                    <th class="px-4 py-3 text-sm font-medium text-gray-600">Token 前缀</th>
                    <th class="px-4 py-3 text-sm font-medium text-gray-600">权限</th>
                    <th class="px-4 py-3 text-sm font-medium text-gray-600">最后使用</th>
                    <th class="px-4 py-3 text-sm font-medium text-gray-600">过期时间</th>
                    <th class="px-4 py-3 text-sm font-medium text-gray-600">创建时间</th>
                    <th class="px-4 py-3 text-sm font-medium text-gray-600">操作</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-100">
                @forelse($tokens as $token)
                <tr class="hover:bg-gray-50">
                    <td class="px-4 py-3 font-medium">{{ $token->name }}</td>
                    <td class="px-4 py-3">
                        <code class="text-sm bg-gray-100 px-2 py-1 rounded">{{ $token->token_prefix }}...</code>
                    </td>
                    <td class="px-4 py-3">
                        @php
                            $badgeColor = match($token->permission) {
                                'full-access' => 'bg-red-100 text-red-700',
                                'read-only'   => 'bg-blue-100 text-blue-700',
                                'custom'      => 'bg-yellow-100 text-yellow-700',
                                default       => 'bg-gray-100 text-gray-700',
                            };
                            $label = match($token->permission) {
                                'full-access' => '完全访问',
                                'read-only'   => '只读',
                                'custom'      => '自定义',
                                default       => $token->permission,
                            };
                        @endphp
                        <span class="px-2 py-1 text-xs font-medium rounded {{ $badgeColor }}">{{ $label }}</span>
                        @if($token->permission === 'custom' && $token->custom_permissions)
                            <div class="mt-1 flex flex-wrap gap-1">
                                @foreach($token->custom_permissions as $scope)
                                    <span class="px-1.5 py-0.5 text-xs bg-gray-100 rounded">{{ $scope }}</span>
                                @endforeach
                            </div>
                        @endif
                    </td>
                    <td class="px-4 py-3 text-sm text-gray-500">
                        {{ $token->last_used_at ? \Carbon\Carbon::parse($token->last_used_at)->diffForHumans() : '从未使用' }}
                    </td>
                    <td class="px-4 py-3 text-sm">
                        @if($token->expires_at)
                            @php $expired = \Carbon\Carbon::parse($token->expires_at)->isPast(); @endphp
                            <span class="{{ $expired ? 'text-red-600 font-medium' : 'text-gray-500' }}">
                                {{ \Carbon\Carbon::parse($token->expires_at)->format('Y-m-d') }}
                                @if($expired) (已过期) @endif
                            </span>
                        @else
                            <span class="text-gray-400">永不过期</span>
                        @endif
                    </td>
                    <td class="px-4 py-3 text-sm text-gray-500">
                        {{ \Carbon\Carbon::parse($token->created_at)->format('Y-m-d H:i') }}
                    </td>
                    <td class="px-4 py-3">
                        <form method="POST" action="{{ route('incus.api-tokens.revoke', $token->id) }}"
                              onsubmit="return confirm('确定要吊销此 Token？此操作不可撤销。')">
                            @csrf
                            @method('DELETE')
                            <button type="submit" class="text-sm text-red-600 hover:text-red-800 hover:underline">
                                吊销
                            </button>
                        </form>
                    </td>
                </tr>
                @empty
                <tr>
                    <td colspan="7" class="px-4 py-8 text-center text-gray-400">
                        暂无 API Token，点击上方按钮创建
                    </td>
                </tr>
                @endforelse
            </tbody>
        </table>
    </div>

    {{-- API 使用说明 --}}
    <div class="mt-6 p-4 bg-gray-50 border rounded-lg">
        <h3 class="font-semibold mb-2">API 使用说明</h3>
        <div class="text-sm text-gray-600 space-y-2">
            <p>Base URL: <code class="bg-white px-2 py-1 rounded border">{{ url('/api/v1') }}</code></p>
            <p>认证方式: 在请求头中添加 <code class="bg-white px-2 py-1 rounded border">Authorization: Bearer &lt;your-token&gt;</code></p>
            <p>频率限制: 每分钟 120 次请求</p>
            <details class="mt-2">
                <summary class="cursor-pointer text-blue-600 hover:underline">查看可用接口</summary>
                <ul class="mt-2 ml-4 space-y-1 list-disc">
                    <li><code>GET /api/v1/instances</code> — 列出所有实例</li>
                    <li><code>GET /api/v1/instances/{id}</code> — 实例详情</li>
                    <li><code>POST /api/v1/instances/{id}/actions</code> — 操作实例（start/stop/reboot/reinstall）</li>
                    <li><code>GET /api/v1/instances/{id}/snapshots</code> — 快照列表</li>
                    <li><code>POST /api/v1/instances/{id}/snapshots</code> — 创建快照</li>
                    <li><code>GET /api/v1/instances/{id}/firewall</code> — 防火墙规则</li>
                    <li><code>PATCH /api/v1/instances/{id}/firewall</code> — 更新防火墙</li>
                    <li><code>GET /api/v1/instances/{id}/metrics</code> — 资源监控</li>
                    <li><code>GET /api/v1/account/balance</code> — 账户余额</li>
                    <li><code>GET /api/v1/account/invoices</code> — 账单列表</li>
                </ul>
            </details>
        </div>
    </div>
</div>

{{-- 创建 Token 模态框 --}}
<div id="create-modal" class="hidden fixed inset-0 z-50 flex items-center justify-center bg-black/50">
    <div class="bg-white rounded-lg shadow-xl w-full max-w-md mx-4">
        <form method="POST" action="{{ route('incus.api-tokens.store') }}">
            @csrf
            <div class="px-6 py-4 border-b">
                <h2 class="text-lg font-semibold">创建 API Token</h2>
            </div>
            <div class="px-6 py-4 space-y-4">
                {{-- 名称 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">Token 名称</label>
                    <input type="text" name="name" required maxlength="64" placeholder="例如：监控脚本"
                           class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500">
                </div>

                {{-- 权限 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">权限级别</label>
                    <select name="permission" id="permission-select" onchange="toggleCustomScopes()"
                            class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500">
                        <option value="read-only">只读 — 仅查看实例、快照、监控</option>
                        <option value="full-access">完全访问 — 所有操作</option>
                        <option value="custom">自定义 — 选择具体权限</option>
                    </select>
                </div>

                {{-- 有效期 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">有效期</label>
                    <select name="expiry_days"
                            class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500">
                        <option value="30">30 天</option>
                        <option value="90" selected>90 天（默认）</option>
                        <option value="180">180 天</option>
                        <option value="365">365 天</option>
                        <option value="0">永不过期</option>
                    </select>
                </div>

                {{-- 自定义权限（custom 时显示） --}}
                <div id="custom-scopes" class="hidden">
                    <label class="block text-sm font-medium text-gray-700 mb-2">选择权限</label>
                    <div class="space-y-2 max-h-48 overflow-y-auto">
                        @foreach([
                            'instances.list'   => '查看实例列表',
                            'instances.read'   => '查看实例详情',
                            'instances.actions' => '操作实例（启动/停止/重启/重装）',
                            'snapshots.list'   => '查看快照',
                            'snapshots.create' => '创建快照',
                            'firewall.read'    => '查看防火墙规则',
                            'firewall.write'   => '修改防火墙规则',
                            'metrics.read'     => '查看资源监控',
                            'account.read'     => '查看账户信息',
                        ] as $scope => $label)
                        <label class="flex items-center gap-2 text-sm">
                            <input type="checkbox" name="custom_permissions[]" value="{{ $scope }}"
                                   class="rounded border-gray-300">
                            <span>{{ $label }}</span>
                            <code class="text-xs text-gray-400 ml-auto">{{ $scope }}</code>
                        </label>
                        @endforeach
                    </div>
                </div>
            </div>
            <div class="px-6 py-4 border-t bg-gray-50 flex justify-end gap-3 rounded-b-lg">
                <button type="button"
                        onclick="document.getElementById('create-modal').classList.add('hidden')"
                        class="px-4 py-2 text-gray-600 hover:text-gray-800">
                    取消
                </button>
                <button type="submit" class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700">
                    创建
                </button>
            </div>
        </form>
    </div>
</div>

@push('scripts')
<script>
function toggleCustomScopes() {
    const select = document.getElementById('permission-select');
    const scopes = document.getElementById('custom-scopes');
    scopes.classList.toggle('hidden', select.value !== 'custom');
}

function copyToken() {
    const tokenEl = document.getElementById('new-token-value');
    navigator.clipboard.writeText(tokenEl.textContent.trim()).then(() => {
        const btn = tokenEl.nextElementSibling;
        btn.textContent = '已复制';
        setTimeout(() => btn.textContent = '复制', 2000);
    });
}

// 点击模态框外部关闭
document.getElementById('create-modal').addEventListener('click', function(e) {
    if (e.target === this) this.classList.add('hidden');
});
</script>
@endpush

@endsection
