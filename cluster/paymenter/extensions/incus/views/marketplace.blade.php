@extends('layouts.app')

@section('title', '应用市场')

@section('content')
<div class="container mx-auto px-4 py-8">
    {{-- 页面标题 --}}
    <div class="mb-8">
        <h1 class="text-3xl font-bold text-gray-900">应用市场</h1>
        <p class="mt-2 text-gray-600">一键部署常用应用，快速搭建您的云端服务</p>
    </div>

    {{-- 搜索和筛选 --}}
    <div class="mb-6 flex items-center gap-4">
        <div class="flex-1">
            <input type="text"
                   id="app-search"
                   placeholder="搜索应用..."
                   class="w-full rounded-lg border border-gray-300 px-4 py-2 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-200">
        </div>
    </div>

    {{-- 应用列表 --}}
    <div id="app-grid" class="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        @forelse ($apps as $app)
        <div class="app-card rounded-lg border border-gray-200 bg-white p-6 shadow-sm transition hover:shadow-md"
             data-name="{{ strtolower($app['name']) }}"
             data-description="{{ strtolower($app['description']) }}">
            {{-- 应用图标和名称 --}}
            <div class="mb-4 flex items-center gap-3">
                <div class="flex h-12 w-12 items-center justify-center rounded-lg bg-blue-50">
                    <i class="{{ $app['icon'] ?? 'fas fa-cube' }} text-2xl text-blue-600"></i>
                </div>
                <div>
                    <h3 class="text-lg font-semibold text-gray-900">{{ $app['name'] }}</h3>
                </div>
            </div>

            {{-- 应用描述 --}}
            <p class="mb-4 text-sm text-gray-600 line-clamp-2">{{ $app['description'] }}</p>

            {{-- 最低配置 --}}
            <div class="mb-4 flex flex-wrap gap-2 text-xs text-gray-500">
                <span class="rounded bg-gray-100 px-2 py-1">
                    <i class="fas fa-microchip mr-1"></i>{{ $app['min_cpu'] }} vCPU
                </span>
                <span class="rounded bg-gray-100 px-2 py-1">
                    <i class="fas fa-memory mr-1"></i>{{ $app['min_memory'] }}
                </span>
                <span class="rounded bg-gray-100 px-2 py-1">
                    <i class="fas fa-hdd mr-1"></i>{{ $app['min_disk'] }}
                </span>
            </div>

            {{-- 开放端口 --}}
            @if (!empty($app['ports']))
            <div class="mb-4 text-xs text-gray-500">
                <span class="font-medium">端口：</span>
                @foreach ($app['ports'] as $port)
                    <span class="inline-block rounded bg-green-50 px-1.5 py-0.5 text-green-700">{{ $port }}</span>
                @endforeach
            </div>
            @endif

            {{-- 部署按钮 --}}
            <button onclick="openDeployModal({{ Js::from($app['id']) }}, {{ Js::from($app['name']) }})"
                    class="w-full rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-blue-300">
                <i class="fas fa-rocket mr-1"></i> 一键部署
            </button>
        </div>
        @empty
        <div class="col-span-full py-12 text-center text-gray-500">
            <i class="fas fa-box-open text-4xl mb-4"></i>
            <p>暂无可用应用</p>
        </div>
        @endforelse
    </div>
</div>

{{-- 部署确认弹窗 --}}
<div id="deploy-modal" class="fixed inset-0 z-50 hidden items-center justify-center bg-black bg-opacity-50">
    <div class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <h2 class="mb-4 text-xl font-bold text-gray-900">
            部署 <span id="modal-app-name"></span>
        </h2>

        <form id="deploy-form" method="POST" action="{{ route('marketplace.deploy') }}">
            @csrf
            <input type="hidden" name="app_id" id="modal-app-id">

            <div class="space-y-4">
                {{-- 实例名称 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700">实例名称（可选）</label>
                    <input type="text" name="instance_name"
                           placeholder="留空则自动生成"
                           class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 focus:border-blue-500 focus:outline-none">
                </div>

                {{-- CPU --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700">CPU 核数</label>
                    <select name="cpu" class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2">
                        <option value="1">1 vCPU</option>
                        <option value="2" selected>2 vCPU</option>
                        <option value="4">4 vCPU</option>
                        <option value="8">8 vCPU</option>
                    </select>
                </div>

                {{-- 内存 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700">内存</label>
                    <select name="memory" class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2">
                        <option value="512MiB">512 MiB</option>
                        <option value="1024MiB">1 GiB</option>
                        <option value="2048MiB" selected>2 GiB</option>
                        <option value="4096MiB">4 GiB</option>
                        <option value="8192MiB">8 GiB</option>
                    </select>
                </div>

                {{-- 磁盘 --}}
                <div>
                    <label class="block text-sm font-medium text-gray-700">磁盘</label>
                    <select name="disk" class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2">
                        <option value="10GiB">10 GiB</option>
                        <option value="20GiB" selected>20 GiB</option>
                        <option value="50GiB">50 GiB</option>
                        <option value="100GiB">100 GiB</option>
                    </select>
                </div>
            </div>

            <div class="mt-6 flex justify-end gap-3">
                <button type="button" onclick="closeDeployModal()"
                        class="rounded-lg border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">
                    取消
                </button>
                <button type="submit"
                        class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
                    确认部署
                </button>
            </div>
        </form>
    </div>
</div>

@push('scripts')
<script>
    // 搜索过滤
    document.getElementById('app-search').addEventListener('input', function () {
        const query = this.value.toLowerCase();
        document.querySelectorAll('.app-card').forEach(card => {
            const name = card.dataset.name;
            const desc = card.dataset.description;
            card.style.display = (name.includes(query) || desc.includes(query)) ? '' : 'none';
        });
    });

    // 打开部署弹窗
    function openDeployModal(appId, appName) {
        document.getElementById('modal-app-id').value = appId;
        document.getElementById('modal-app-name').textContent = appName;
        document.getElementById('deploy-modal').classList.remove('hidden');
        document.getElementById('deploy-modal').classList.add('flex');
    }

    // 关闭部署弹窗
    function closeDeployModal() {
        document.getElementById('deploy-modal').classList.add('hidden');
        document.getElementById('deploy-modal').classList.remove('flex');
    }

    // 点击遮罩关闭
    document.getElementById('deploy-modal').addEventListener('click', function (e) {
        if (e.target === this) closeDeployModal();
    });
</script>
@endpush
@endsection
