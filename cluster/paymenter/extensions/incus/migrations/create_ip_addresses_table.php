<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('ip_addresses', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('pool_id')->comment('所属 IP 池');
            $table->string('ip', 15)->unique()->comment('IP 地址');
            $table->enum('status', ['available', 'allocated', 'cooldown', 'reserved'])
                  ->default('available')
                  ->comment('状态：可用/已分配/冷却中/保留');
            $table->string('vm_name', 64)->nullable()->comment('绑定的 VM 名称');
            $table->unsignedBigInteger('order_id')->nullable()->comment('关联订单 ID');
            $table->timestamp('allocated_at')->nullable()->comment('分配时间');
            $table->timestamp('released_at')->nullable()->comment('释放时间');
            $table->timestamp('cooldown_until')->nullable()->comment('冷却期截止时间');

            $table->foreign('pool_id')->references('id')->on('ip_pools')->onDelete('cascade');
            $table->index(['pool_id', 'status']);
            $table->index('status');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('ip_addresses');
    }
};
