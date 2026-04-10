{{-- 启动脚本管理视图 --}}
@extends('layouts.app')

@section('title', '启动脚本')

@section('content')
<div class="container mx-auto px-4 py-6">
    {{-- 脚本列表 --}}
    <div class="bg-white rounded-lg shadow p-6 mb-6">
        <div class="flex items-center justify-between mb-4">
            <h2 class="text-xl font-semibold">启动脚本管理</h2>
            <button type="button" onclick="openCreateModal()"
                    class="bg-blue-600 hover:bg-blue-700 text-white font-medium py-2 px-4 rounded transition">
                新建脚本
            </button>
        </div>

        <p class="text-gray-500 text-sm mb-4">
            管理 cloud-init 启动脚本，可在创建 VM 时自动执行。支持 Bash 脚本和 cloud-init YAML 配置。
        </p>

        @if(empty($scripts))
            <div class="text-center py-12 text-gray-400">
                <svg class="mx-auto w-12 h-12 mb-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
                          d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/>
                </svg>
                <p>暂无启动脚本，点击"新建脚本"创建</p>
            </div>
        @else
            <div class="overflow-x-auto">
                <table class="w-full text-sm">
                    <thead>
                        <tr class="text-left text-gray-500 border-b">
                            <th class="py-3 px-2">名称</th>
                            <th class="py-3 px-2">类型</th>
                            <th class="py-3 px-2">安全状态</th>
                            <th class="py-3 px-2">更新时间</th>
                            <th class="py-3 px-2 text-right">操作</th>
                        </tr>
                    </thead>
                    <tbody>
                        @foreach($scripts as $script)
                        <tr class="border-b hover:bg-gray-50">
                            <td class="py-3 px-2 font-medium">{{ $script->name }}</td>
                            <td class="py-3 px-2">
                                <span class="inline-block px-2 py-0.5 text-xs rounded
                                    {{ $script->type === 'bash' ? 'bg-green-100 text-green-700' : 'bg-purple-100 text-purple-700' }}">
                                    {{ $script->type }}
                                </span>
                            </td>
                            <td class="py-3 px-2">
                                @if(!empty($script->warnings))
                                    <span class="inline-flex items-center text-yellow-600" title="{{ implode('；', $script->warnings) }}">
                                        <svg class="w-4 h-4 mr-1" fill="currentColor" viewBox="0 0 20 20">
                                            <path fill-rule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/>
                                        </svg>
                                        {{ count($script->warnings) }} 项警告
                                    </span>
                                @else
                                    <span class="text-green-600">✓ 安全</span>
                                @endif
                            </td>
                            <td class="py-3 px-2 text-gray-500">{{ $script->updated_at }}</td>
                            <td class="py-3 px-2 text-right space-x-2">
                                <button type="button"
                                        onclick="openEditModal({{ json_encode($script) }})"
                                        class="text-blue-600 hover:text-blue-800 text-sm">
                                    编辑
                                </button>
                                <form method="POST"
                                      action="{{ route('incus.startup-scripts.delete', $script->id) }}"
                                      class="inline"
                                      onsubmit="return confirm('确定删除脚本「{{ $script->name }}」？')">
                                    @csrf
                                    @method('DELETE')
                                    <button type="submit" class="text-red-600 hover:text-red-800 text-sm">
                                        删除
                                    </button>
                                </form>
                            </td>
                        </tr>
                        @endforeach
                    </tbody>
                </table>
            </div>
        @endif
    </div>
</div>

{{-- 新建/编辑脚本模态框 --}}
<div id="script-modal" class="fixed inset-0 bg-black bg-opacity-50 hidden z-50 flex items-center justify-center">
    <div class="bg-white rounded-lg shadow-xl w-full max-w-2xl mx-4 max-h-[90vh] overflow-y-auto">
        <form id="script-form" method="POST">
            @csrf
            <input type="hidden" id="form-method" name="_method" value="POST">

            <div class="p-6 border-b">
                <h3 id="modal-title" class="text-lg font-semibold">新建启动脚本</h3>
            </div>

            <div class="p-6 space-y-4">
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">脚本名称</label>
                    <input type="text" name="name" id="script-name" required
                           maxlength="128" placeholder="例如：LAMP 环境初始化"
                           class="w-full border rounded px-3 py-2 focus:ring-2 focus:ring-blue-500 focus:border-blue-500">
                </div>

                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">脚本类型</label>
                    <select name="type" id="script-type"
                            class="w-full border rounded px-3 py-2 focus:ring-2 focus:ring-blue-500"
                            onchange="updatePlaceholder()">
                        <option value="bash">Bash 脚本</option>
                        <option value="cloud-init">cloud-init YAML</option>
                    </select>
                </div>

                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">脚本内容</label>
                    <textarea name="script" id="script-content" required rows="15"
                              class="w-full border rounded px-3 py-2 font-mono text-sm focus:ring-2 focus:ring-blue-500"
                              placeholder="#!/bin/bash&#10;apt-get update&#10;apt-get install -y nginx"></textarea>
                    <p class="text-xs text-gray-400 mt-1">最大 64KB。包含危险命令时会显示安全警告。</p>
                </div>

                {{-- 安全警告提示区 --}}
                <div id="warning-area" class="hidden bg-yellow-50 border border-yellow-200 rounded p-3">
                    <p class="text-sm font-medium text-yellow-800 mb-1">安全提示</p>
                    <p class="text-xs text-yellow-700">
                        脚本将在 VM 首次启动时以 root 权限执行。请避免包含：
                        <code>rm -rf /</code>、<code>curl | bash</code> 等危险命令。
                        系统会对危险命令进行标记，但不会阻止保存。
                    </p>
                </div>
            </div>

            <div class="p-6 border-t bg-gray-50 flex justify-end space-x-3">
                <button type="button" onclick="closeModal()"
                        class="px-4 py-2 border rounded text-gray-700 hover:bg-gray-100 transition">
                    取消
                </button>
                <button type="submit"
                        class="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 transition">
                    保存
                </button>
            </div>
        </form>
    </div>
</div>

@push('scripts')
<script>
    function openCreateModal() {
        document.getElementById('modal-title').textContent = '新建启动脚本';
        document.getElementById('script-form').action = '{{ route("incus.startup-scripts.store") }}';
        document.getElementById('form-method').value = 'POST';
        document.getElementById('script-name').value = '';
        document.getElementById('script-type').value = 'bash';
        document.getElementById('script-content').value = '';
        document.getElementById('script-modal').classList.remove('hidden');
    }

    function openEditModal(script) {
        document.getElementById('modal-title').textContent = '编辑启动脚本';
        document.getElementById('script-form').action =
            '{{ route("incus.startup-scripts.update", ":id") }}'.replace(':id', script.id);
        document.getElementById('form-method').value = 'PUT';
        document.getElementById('script-name').value = script.name;
        document.getElementById('script-type').value = script.type;
        document.getElementById('script-content').value = script.script;
        document.getElementById('script-modal').classList.remove('hidden');
        updatePlaceholder();
    }

    function closeModal() {
        document.getElementById('script-modal').classList.add('hidden');
    }

    function updatePlaceholder() {
        const type = document.getElementById('script-type').value;
        const textarea = document.getElementById('script-content');
        if (type === 'cloud-init') {
            textarea.placeholder = '#cloud-config\npackages:\n  - nginx\n  - mysql-server\nruncmd:\n  - systemctl enable nginx';
        } else {
            textarea.placeholder = '#!/bin/bash\napt-get update\napt-get install -y nginx';
        }
    }

    // ESC 关闭模态框
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') closeModal();
    });

    // 点击遮罩关闭
    document.getElementById('script-modal').addEventListener('click', function(e) {
        if (e.target === this) closeModal();
    });
</script>
@endpush
@endsection
