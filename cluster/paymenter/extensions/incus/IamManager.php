<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\Str;

/**
 * 团队 IAM（身份与访问管理）管理器
 *
 * 提供多用户团队协作功能，包括团队创建、成员管理和权限检查。
 * 角色层级：owner > admin > member > viewer
 */
class IamManager
{
    /**
     * 可用角色列表
     */
    const ROLE_OWNER  = 'owner';
    const ROLE_ADMIN  = 'admin';
    const ROLE_MEMBER = 'member';
    const ROLE_VIEWER = 'viewer';

    /**
     * 权限矩阵
     *
     * 定义每个角色可执行的操作列表
     */
    const PERMISSIONS = [
        self::ROLE_OWNER => [
            'team.update',
            'team.delete',
            'team.transfer',
            'member.invite',
            'member.remove',
            'member.update_role',
            'instance.create',
            'instance.delete',
            'instance.start',
            'instance.stop',
            'instance.restart',
            'instance.resize',
            'instance.snapshot',
            'instance.backup',
            'instance.restore',
            'instance.console',
            'instance.view',
            'billing.view',
            'billing.manage',
            'marketplace.deploy',
        ],
        self::ROLE_ADMIN => [
            'member.invite',
            'member.remove',
            'instance.create',
            'instance.delete',
            'instance.start',
            'instance.stop',
            'instance.restart',
            'instance.resize',
            'instance.snapshot',
            'instance.backup',
            'instance.restore',
            'instance.console',
            'instance.view',
            'billing.view',
            'marketplace.deploy',
        ],
        self::ROLE_MEMBER => [
            'instance.create',
            'instance.start',
            'instance.stop',
            'instance.restart',
            'instance.snapshot',
            'instance.console',
            'instance.view',
            'marketplace.deploy',
        ],
        self::ROLE_VIEWER => [
            'instance.view',
            'billing.view',
        ],
    ];

    /**
     * 创建团队
     *
     * @param int    $ownerId 创建者用户 ID
     * @param string $name    团队名称
     * @return array 创建的团队信息
     */
    public function createTeam(int $ownerId, string $name): array
    {
        $teamId = DB::table('teams')->insertGetId([
            'name'       => $name,
            'owner_id'   => $ownerId,
            'slug'       => Str::slug($name) . '-' . Str::random(6),
            'created_at' => now(),
            'updated_at' => now(),
        ]);

        // 将创建者添加为 owner 成员
        DB::table('team_members')->insert([
            'team_id'    => $teamId,
            'user_id'    => $ownerId,
            'role'       => self::ROLE_OWNER,
            'invited_by' => $ownerId,
            'joined_at'  => now(),
            'created_at' => now(),
            'updated_at' => now(),
        ]);

        Log::info("团队创建成功", ['team_id' => $teamId, 'owner_id' => $ownerId]);

        return [
            'id'       => $teamId,
            'name'     => $name,
            'owner_id' => $ownerId,
        ];
    }

    /**
     * 邀请成员加入团队
     *
     * @param int    $callerId 操作者用户 ID
     * @param int    $teamId   团队 ID
     * @param string $email    被邀请人邮箱
     * @param string $role     角色：admin/member/viewer（不允许直接邀请为 owner）
     * @return array 邀请信息
     *
     * @throws \InvalidArgumentException 角色无效时抛出
     * @throws \RuntimeException 权限不足或成员已存在时抛出
     */
    public function inviteMember(int $callerId, int $teamId, string $email, string $role = self::ROLE_MEMBER): array
    {
        // 校验调用者权限
        if (!$this->checkPermission($callerId, $teamId, 'member.invite')) {
            throw new \RuntimeException('权限不足：无邀请成员权限');
        }

        // 不允许通过邀请直接授予 owner 角色
        if ($role === self::ROLE_OWNER) {
            throw new \InvalidArgumentException('不能直接邀请为 owner，请使用所有权转移功能');
        }

        // 校验角色
        if (!in_array($role, [self::ROLE_ADMIN, self::ROLE_MEMBER, self::ROLE_VIEWER])) {
            throw new \InvalidArgumentException("无效的角色: {$role}");
        }

        // admin 不能邀请 admin（仅 owner 可以）
        $callerMember = DB::table('team_members')
            ->where('team_id', $teamId)
            ->where('user_id', $callerId)
            ->first();
        if ($callerMember && $callerMember->role !== self::ROLE_OWNER && $role === self::ROLE_ADMIN) {
            throw new \RuntimeException('权限不足：仅 owner 可邀请 admin');
        }

        // 查找用户
        $user = DB::table('users')->where('email', $email)->first();
        if (!$user) {
            throw new \RuntimeException("用户不存在: {$email}");
        }

        // 检查是否已是成员
        $existing = DB::table('team_members')
            ->where('team_id', $teamId)
            ->where('user_id', $user->id)
            ->first();

        if ($existing) {
            throw new \RuntimeException("用户已是团队成员: {$email}");
        }

        // 添加成员
        $memberId = DB::table('team_members')->insertGetId([
            'team_id'    => $teamId,
            'user_id'    => $user->id,
            'role'       => $role,
            'invited_by' => $callerId,
            'joined_at'  => now(),
            'created_at' => now(),
            'updated_at' => now(),
        ]);

        Log::info("团队成员添加成功", [
            'team_id' => $teamId,
            'user_id' => $user->id,
            'role'    => $role,
        ]);

        return [
            'id'      => $memberId,
            'team_id' => $teamId,
            'user_id' => $user->id,
            'email'   => $email,
            'role'    => $role,
        ];
    }

    /**
     * 移除团队成员
     *
     * @param int $callerId 操作者用户 ID
     * @param int $teamId   团队 ID
     * @param int $userId   要移除的用户 ID
     * @return bool 是否成功
     *
     * @throws \RuntimeException 权限不足或尝试移除 owner 时抛出
     */
    public function removeMember(int $callerId, int $teamId, int $userId): bool
    {
        // 校验调用者权限
        if (!$this->checkPermission($callerId, $teamId, 'member.remove')) {
            throw new \RuntimeException('权限不足：无移除成员权限');
        }

        // 不允许移除团队 owner
        $team = DB::table('teams')->where('id', $teamId)->first();
        if ($team && $team->owner_id === $userId) {
            throw new \RuntimeException('不能移除团队所有者，请先转移所有权');
        }

        // admin 不能移除其他 admin（仅 owner 可以）
        $callerMember = DB::table('team_members')
            ->where('team_id', $teamId)->where('user_id', $callerId)->first();
        $targetMember = DB::table('team_members')
            ->where('team_id', $teamId)->where('user_id', $userId)->first();
        if ($callerMember && $targetMember
            && $callerMember->role !== self::ROLE_OWNER
            && $targetMember->role === self::ROLE_ADMIN) {
            throw new \RuntimeException('权限不足：仅 owner 可移除 admin');
        }

        $deleted = DB::table('team_members')
            ->where('team_id', $teamId)
            ->where('user_id', $userId)
            ->delete();

        if ($deleted) {
            Log::info("团队成员已移除", ['team_id' => $teamId, 'user_id' => $userId]);
        }

        return $deleted > 0;
    }

    /**
     * 获取用户所属的所有团队
     *
     * @param int $userId 用户 ID
     * @return array 团队列表（包含角色信息）
     */
    public function listTeams(int $userId): array
    {
        return DB::table('team_members')
            ->join('teams', 'teams.id', '=', 'team_members.team_id')
            ->where('team_members.user_id', $userId)
            ->select([
                'teams.id',
                'teams.name',
                'teams.slug',
                'teams.owner_id',
                'team_members.role',
                'team_members.joined_at',
            ])
            ->orderBy('teams.name')
            ->get()
            ->toArray();
    }

    /**
     * 检查用户在团队中是否拥有指定权限
     *
     * @param int    $userId 用户 ID
     * @param int    $teamId 团队 ID
     * @param string $action 操作名称（如 instance.create）
     * @return bool 是否有权限
     */
    public function checkPermission(int $userId, int $teamId, string $action): bool
    {
        // 获取成员角色
        $member = DB::table('team_members')
            ->where('team_id', $teamId)
            ->where('user_id', $userId)
            ->first();

        if (!$member) {
            return false;
        }

        $role = $member->role;

        // 查询权限矩阵
        $allowedActions = self::PERMISSIONS[$role] ?? [];

        return in_array($action, $allowedActions, true);
    }

    /**
     * 获取指定角色的所有权限
     *
     * @param string $role 角色名称
     * @return array 权限列表
     */
    public function getPermissionsForRole(string $role): array
    {
        return self::PERMISSIONS[$role] ?? [];
    }

    /**
     * 更新成员角色
     *
     * @param int    $callerId 操作者用户 ID
     * @param int    $teamId   团队 ID
     * @param int    $userId   目标用户 ID
     * @param string $newRole  新角色：admin/member/viewer（不允许设为 owner）
     * @return bool 是否成功
     *
     * @throws \RuntimeException 权限不足时抛出
     * @throws \InvalidArgumentException 角色无效时抛出
     */
    public function updateMemberRole(int $callerId, int $teamId, int $userId, string $newRole): bool
    {
        // 校验调用者权限
        if (!$this->checkPermission($callerId, $teamId, 'member.update_role')) {
            throw new \RuntimeException('权限不足：无更改角色权限');
        }

        // 不允许通过此方法设置 owner（使用专用的所有权转移功能）
        if ($newRole === self::ROLE_OWNER) {
            throw new \InvalidArgumentException('不能通过角色更新设为 owner，请使用所有权转移功能');
        }

        if (!in_array($newRole, [self::ROLE_ADMIN, self::ROLE_MEMBER, self::ROLE_VIEWER])) {
            throw new \InvalidArgumentException("无效的角色: {$newRole}");
        }

        // 不允许修改 owner 的角色
        $targetMember = DB::table('team_members')
            ->where('team_id', $teamId)->where('user_id', $userId)->first();
        if ($targetMember && $targetMember->role === self::ROLE_OWNER) {
            throw new \RuntimeException('不能修改 owner 的角色，请使用所有权转移功能');
        }

        // admin 不能修改其他 admin 的角色
        $callerMember = DB::table('team_members')
            ->where('team_id', $teamId)->where('user_id', $callerId)->first();
        if ($callerMember && $callerMember->role !== self::ROLE_OWNER
            && $targetMember && $targetMember->role === self::ROLE_ADMIN) {
            throw new \RuntimeException('权限不足：仅 owner 可修改 admin 角色');
        }

        $updated = DB::table('team_members')
            ->where('team_id', $teamId)
            ->where('user_id', $userId)
            ->update([
                'role'       => $newRole,
                'updated_at' => now(),
            ]);

        return $updated > 0;
    }
}
