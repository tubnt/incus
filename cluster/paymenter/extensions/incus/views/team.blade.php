@extends('layouts.app')

@section('title', '团队管理')

@section('content')
<div class="container mx-auto px-4 py-8">
    {{-- 页面标题 --}}
    <div class="mb-8 flex items-center justify-between">
        <div>
            <h1 class="text-3xl font-bold text-gray-900">团队管理</h1>
            <p class="mt-2 text-gray-600">管理您的团队和成员权限</p>
        </div>
        <button onclick="openCreateTeamModal()"
                class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
            <i class="fas fa-plus mr-1"></i> 创建团队
        </button>
    </div>

    {{-- 团队列表 --}}
    <div class="space-y-6">
        @forelse ($teams as $team)
        <div class="rounded-lg border border-gray-200 bg-white shadow-sm">
            {{-- 团队头部 --}}
            <div class="flex items-center justify-between border-b border-gray-100 px-6 py-4">
                <div class="flex items-center gap-3">
                    <div class="flex h-10 w-10 items-center justify-center rounded-full bg-blue-100 text-blue-600 font-bold">
                        {{ mb_substr($team->name, 0, 1) }}
                    </div>
                    <div>
                        <h2 class="text-lg font-semibold text-gray-900">{{ $team->name }}</h2>
                        <span class="text-xs text-gray-500">
                            @switch($team->pivot->role ?? $team->role)
                                @case('owner')  <span class="text-red-600">所有者</span> @break
                                @case('admin')  <span class="text-orange-600">管理员</span> @break
                                @case('member') <span class="text-blue-600">成员</span> @break
                                @case('viewer') <span class="text-gray-600">查看者</span> @break
                            @endswitch
                        </span>
                    </div>
                </div>
                <div class="flex gap-2">
                    @if (in_array($team->pivot->role ?? $team->role, ['owner', 'admin']))
                    <button onclick="openInviteModal({{ $team->id }}, '{{ $team->name }}')"
                            class="rounded-lg border border-gray-300 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50">
                        <i class="fas fa-user-plus mr-1"></i> 邀请成员
                    </button>
                    @endif
                    @if (($team->pivot->role ?? $team->role) === 'owner')
                    <button onclick="openTeamSettings({{ $team->id }})"
                            class="rounded-lg border border-gray-300 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50">
                        <i class="fas fa-cog mr-1"></i> 设置
                    </button>
                    @endif
                </div>
            </div>

            {{-- 成员列表 --}}
            <div class="px-6 py-4">
                <h3 class="mb-3 text-sm font-medium text-gray-500">团队成员</h3>
                <div class="space-y-2">
                    @foreach ($team->members as $member)
                    <div class="flex items-center justify-between rounded-lg bg-gray-50 px-4 py-3">
                        <div class="flex items-center gap-3">
                            <div class="flex h-8 w-8 items-center justify-center rounded-full bg-gray-200 text-sm font-medium text-gray-600">
                                {{ mb_substr($member->user->name ?? $member->user->email, 0, 1) }}
                            </div>
                            <div>
                                <p class="text-sm font-medium text-gray-900">{{ $member->user->name ?? '未设置昵称' }}</p>
                                <p class="text-xs text-gray-500">{{ $member->user->email }}</p>
                            </div>
                        </div>
                        <div class="flex items-center gap-3">
                            {{-- 角色标签 --}}
                            <span class="rounded-full px-2.5 py-0.5 text-xs font-medium
                                @switch($member->role)
                                    @case('owner')  bg-red-100 text-red-700 @break
                                    @case('admin')  bg-orange-100 text-orange-700 @break
                                    @case('member') bg-blue-100 text-blue-700 @break
                                    @case('viewer') bg-gray-100 text-gray-700 @break
                                @endswitch">
                                @switch($member->role)
                                    @case('owner')  所有者 @break
                                    @case('admin')  管理员 @break
                                    @case('member') 成员 @break
                                    @case('viewer') 查看者 @break
                                @endswitch
                            </span>

                            {{-- 操作按钮（仅 owner/admin 可见，不能操作自己） --}}
                            @if (in_array($team->pivot->role ?? $team->role, ['owner', 'admin']) && $member->user_id !== auth()->id())
                                @if ($member->role !== 'owner')
                                <div class="relative" x-data="{ open: false }">
                                    <button @click="open = !open"
                                            class="rounded p-1 text-gray-400 hover:bg-gray-200 hover:text-gray-600">
                                        <i class="fas fa-ellipsis-v"></i>
                                    </button>
                                    <div x-show="open" @click.away="open = false"
                                         class="absolute right-0 z-10 mt-1 w-36 rounded-lg border bg-white py-1 shadow-lg">
                                        <button onclick="changeRole({{ $team->id }}, {{ $member->user_id }})"
                                                class="block w-full px-4 py-2 text-left text-sm text-gray-700 hover:bg-gray-50">
                                            <i class="fas fa-exchange-alt mr-2"></i>更改角色
                                        </button>
                                        <button onclick="removeMember({{ $team->id }}, {{ $member->user_id }}, '{{ $member->user->name }}')"
                                                class="block w-full px-4 py-2 text-left text-sm text-red-600 hover:bg-red-50">
                                            <i class="fas fa-user-minus mr-2"></i>移除成员
                                        </button>
                                    </div>
                                </div>
                                @endif
                            @endif
                        </div>
                    </div>
                    @endforeach
                </div>
            </div>
        </div>
        @empty
        <div class="py-16 text-center">
            <i class="fas fa-users text-4xl text-gray-300 mb-4"></i>
            <p class="text-gray-500">您还没有加入任何团队</p>
            <button onclick="openCreateTeamModal()"
                    class="mt-4 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
                创建第一个团队
            </button>
        </div>
        @endforelse
    </div>
</div>

{{-- 创建团队弹窗 --}}
<div id="create-team-modal" class="fixed inset-0 z-50 hidden items-center justify-center bg-black bg-opacity-50">
    <div class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <h2 class="mb-4 text-xl font-bold text-gray-900">创建团队</h2>
        <form method="POST" action="{{ route('teams.store') }}">
            @csrf
            <div class="space-y-4">
                <div>
                    <label class="block text-sm font-medium text-gray-700">团队名称</label>
                    <input type="text" name="name" required
                           placeholder="输入团队名称"
                           class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 focus:border-blue-500 focus:outline-none">
                </div>
                <div>
                    <label class="block text-sm font-medium text-gray-700">团队描述（可选）</label>
                    <textarea name="description" rows="3"
                              placeholder="简要描述团队用途"
                              class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 focus:border-blue-500 focus:outline-none"></textarea>
                </div>
            </div>
            <div class="mt-6 flex justify-end gap-3">
                <button type="button" onclick="closeModal('create-team-modal')"
                        class="rounded-lg border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">
                    取消
                </button>
                <button type="submit"
                        class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
                    创建
                </button>
            </div>
        </form>
    </div>
</div>

{{-- 邀请成员弹窗 --}}
<div id="invite-modal" class="fixed inset-0 z-50 hidden items-center justify-center bg-black bg-opacity-50">
    <div class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <h2 class="mb-4 text-xl font-bold text-gray-900">
            邀请成员到 <span id="invite-team-name"></span>
        </h2>
        <form id="invite-form" method="POST" action="{{ route('teams.invite') }}">
            @csrf
            <input type="hidden" name="team_id" id="invite-team-id">
            <div class="space-y-4">
                <div>
                    <label class="block text-sm font-medium text-gray-700">邮箱地址</label>
                    <input type="email" name="email" required
                           placeholder="输入成员邮箱"
                           class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 focus:border-blue-500 focus:outline-none">
                </div>
                <div>
                    <label class="block text-sm font-medium text-gray-700">角色</label>
                    <select name="role" class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2">
                        <option value="member">成员 - 可创建和管理自己的实例</option>
                        <option value="admin">管理员 - 可管理成员和所有实例</option>
                        <option value="viewer">查看者 - 仅可查看实例和账单</option>
                    </select>
                </div>
            </div>
            <div class="mt-6 flex justify-end gap-3">
                <button type="button" onclick="closeModal('invite-modal')"
                        class="rounded-lg border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">
                    取消
                </button>
                <button type="submit"
                        class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
                    发送邀请
                </button>
            </div>
        </form>
    </div>
</div>

@push('scripts')
<script>
    // 通用弹窗控制
    function openModal(id) {
        document.getElementById(id).classList.remove('hidden');
        document.getElementById(id).classList.add('flex');
    }

    function closeModal(id) {
        document.getElementById(id).classList.add('hidden');
        document.getElementById(id).classList.remove('flex');
    }

    // 创建团队
    function openCreateTeamModal() {
        openModal('create-team-modal');
    }

    // 邀请成员
    function openInviteModal(teamId, teamName) {
        document.getElementById('invite-team-id').value = teamId;
        document.getElementById('invite-team-name').textContent = teamName;
        openModal('invite-modal');
    }

    // 团队设置
    function openTeamSettings(teamId) {
        window.location.href = `/teams/${teamId}/settings`;
    }

    // 更改角色
    function changeRole(teamId, userId) {
        const newRole = prompt('请输入新角色（admin / member / viewer）：');
        if (newRole && ['admin', 'member', 'viewer'].includes(newRole)) {
            fetch(`/api/teams/${teamId}/members/${userId}/role`, {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-TOKEN': document.querySelector('meta[name="csrf-token"]').content,
                },
                body: JSON.stringify({ role: newRole }),
            }).then(res => {
                if (res.ok) location.reload();
                else alert('更改角色失败');
            });
        }
    }

    // 移除成员
    function removeMember(teamId, userId, userName) {
        if (!confirm(`确定要将 ${userName} 从团队中移除吗？`)) return;

        fetch(`/api/teams/${teamId}/members/${userId}`, {
            method: 'DELETE',
            headers: {
                'X-CSRF-TOKEN': document.querySelector('meta[name="csrf-token"]').content,
            },
        }).then(res => {
            if (res.ok) location.reload();
            else alert('移除成员失败');
        });
    }

    // 点击遮罩关闭弹窗
    document.querySelectorAll('.fixed').forEach(modal => {
        modal.addEventListener('click', function (e) {
            if (e.target === this) {
                this.classList.add('hidden');
                this.classList.remove('flex');
            }
        });
    });
</script>
@endpush
@endsection
