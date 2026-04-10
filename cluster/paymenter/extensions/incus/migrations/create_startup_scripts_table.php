<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('startup_scripts', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('user_id')->index();
            $table->string('name', 128);
            $table->text('script');
            $table->enum('type', ['bash', 'cloud-init'])->default('bash');
            $table->json('warnings')->nullable();
            $table->timestamps();

            $table->foreign('user_id')
                ->references('id')
                ->on('users')
                ->cascadeOnDelete();
        });

        // 救援模式会话表（RescueMode 依赖）
        Schema::create('vm_rescue_sessions', function (Blueprint $table) {
            $table->id();
            $table->string('vm_name', 64)->index();
            $table->string('original_boot_image', 255)->default('');
            $table->json('original_root_device');
            $table->timestamp('expires_at');
            $table->timestamp('exited_at')->nullable();
            $table->timestamp('created_at')->useCurrent();
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('vm_rescue_sessions');
        Schema::dropIfExists('startup_scripts');
    }
};
