<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('user_alerts', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('user_id');
            $table->string('vm_name', 64);
            $table->enum('metric', [
                'cpu_percent',
                'memory_percent',
                'bandwidth_in',
                'bandwidth_out',
                'disk_percent',
            ]);
            $table->decimal('threshold', 20, 4);
            $table->enum('direction', ['above', 'below']);
            $table->enum('channel', ['email', 'webhook']);
            $table->string('webhook_url', 512)->nullable();
            $table->boolean('enabled')->default(true);
            $table->timestamp('last_notified_at')->nullable();
            $table->timestamps();

            $table->foreign('user_id')
                  ->references('id')
                  ->on('users')
                  ->onDelete('cascade');

            $table->index(['user_id', 'vm_name'], 'idx_user_vm');
            $table->index(['enabled', 'metric'], 'idx_enabled_metric');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('user_alerts');
    }
};
