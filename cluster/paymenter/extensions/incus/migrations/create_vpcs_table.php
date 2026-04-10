<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('vpcs', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('user_id')->comment('所属用户');
            $table->string('name', 64)->comment('VPC 名称');
            $table->string('subnet', 18)->comment('私有网段 CIDR，如 10.1.0.0/16');
            $table->string('incus_network', 64)->comment('Incus managed network 名称');
            $table->timestamps();

            $table->index('user_id');
            $table->unique(['user_id', 'name']);
            $table->unique('subnet');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('vpcs');
    }
};
