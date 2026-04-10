<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('incus_api_tokens', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('user_id')->index();
            $table->string('name', 64);
            $table->string('token_hash', 64)->unique(); // SHA256 哈希
            $table->string('token_prefix', 14);          // "incus_" + 8 字符，用于标识
            $table->enum('permission', ['read-only', 'full-access', 'custom'])->default('read-only');
            $table->json('custom_permissions')->nullable(); // custom 模式下的细粒度权限
            $table->timestamp('last_used_at')->nullable();
            $table->timestamp('expires_at')->nullable();
            $table->timestamps();

            $table->foreign('user_id')->references('id')->on('users')->cascadeOnDelete();
            $table->index(['user_id', 'created_at']);
        });

        Schema::create('incus_api_rate_limits', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('user_id')->index();
            $table->unsignedInteger('request_count')->default(0);
            $table->timestamp('window_start');

            $table->unique(['user_id', 'window_start']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('incus_api_rate_limits');
        Schema::dropIfExists('incus_api_tokens');
    }
};
