<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('incus_object_storage_users', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('user_id')->unique();
            $table->string('rgw_uid', 64)->unique()->comment('RGW 用户 UID（incus-user-{id}）');
            $table->string('access_key', 128)->comment('S3 Access Key');
            $table->text('secret_key')->comment('S3 Secret Key（加密存储）');
            $table->unsignedBigInteger('quota_bytes')->default(53687091200)->comment('用户配额（字节），默认 50GB');
            $table->unsignedInteger('max_buckets')->default(10)->comment('最大桶数量');
            $table->boolean('suspended')->default(false)->comment('是否已暂停');
            $table->timestamps();

            $table->foreign('user_id')->references('id')->on('users')->cascadeOnDelete();
            $table->index('access_key');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('incus_object_storage_users');
    }
};
