<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('vpc_members', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('vpc_id')->comment('所属 VPC');
            $table->string('vm_name', 64)->comment('VM 名称');
            $table->string('private_ip', 15)->nullable()->comment('VPC 内分配的私有 IP');
            $table->timestamp('joined_at')->useCurrent()->comment('加入时间');

            $table->foreign('vpc_id')->references('id')->on('vpcs')->onDelete('cascade');
            $table->unique(['vpc_id', 'vm_name']);
            $table->index('vm_name');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('vpc_members');
    }
};
