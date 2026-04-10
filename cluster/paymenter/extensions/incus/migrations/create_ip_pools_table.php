<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('ip_pools', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('server_id')->comment('关联 Paymenter Server');
            $table->string('name', 64)->comment('池名称，如 tokyo-pool-1');
            $table->string('subnet', 18)->comment('子网 CIDR，如 202.151.179.224/27');
            $table->string('gateway', 15)->comment('网关地址');
            $table->string('netmask', 15)->comment('子网掩码');
            $table->timestamps();

            $table->index('server_id');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('ip_pools');
    }
};
