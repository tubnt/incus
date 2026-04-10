<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

/**
 * 创建团队成员表
 *
 * 存储团队成员关系及角色信息。
 * 角色：owner（所有者）、admin（管理员）、member（成员）、viewer（查看者）
 */
return new class extends Migration
{
    public function up(): void
    {
        Schema::create('team_members', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('team_id')->comment('团队 ID');
            $table->unsignedBigInteger('user_id')->comment('用户 ID');
            $table->enum('role', ['owner', 'admin', 'member', 'viewer'])
                ->default('member')
                ->comment('成员角色');
            $table->unsignedBigInteger('invited_by')->nullable()->comment('邀请人用户 ID');
            $table->timestamp('joined_at')->nullable()->comment('加入时间');
            $table->timestamps();

            $table->foreign('team_id')
                ->references('id')
                ->on('teams')
                ->onDelete('cascade');

            $table->foreign('user_id')
                ->references('id')
                ->on('users')
                ->onDelete('cascade');

            $table->foreign('invited_by')
                ->references('id')
                ->on('users')
                ->onDelete('set null');

            // 一个用户在一个团队中只能有一条记录
            $table->unique(['team_id', 'user_id']);
            $table->index('user_id');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('team_members');
    }
};
