<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

/**
 * 创建团队表
 *
 * 存储团队基本信息，每个团队有一个所有者。
 */
return new class extends Migration
{
    public function up(): void
    {
        Schema::create('teams', function (Blueprint $table) {
            $table->id();
            $table->string('name', 100)->comment('团队名称');
            $table->string('slug', 120)->unique()->comment('团队 URL 标识');
            $table->unsignedBigInteger('owner_id')->comment('团队所有者用户 ID');
            $table->text('description')->nullable()->comment('团队描述');
            $table->string('avatar')->nullable()->comment('团队头像 URL');
            $table->json('settings')->nullable()->comment('团队设置（JSON）');
            $table->timestamps();
            $table->softDeletes();

            $table->foreign('owner_id')
                ->references('id')
                ->on('users')
                ->onDelete('cascade');

            $table->index('owner_id');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('teams');
    }
};
