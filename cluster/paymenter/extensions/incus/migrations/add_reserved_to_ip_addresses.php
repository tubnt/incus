<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::table('ip_addresses', function (Blueprint $table) {
            $table->unsignedBigInteger('reserved_by_user')->nullable()
                  ->after('cooldown_until')
                  ->comment('保留该 IP 的用户 ID');
            $table->timestamp('reserved_at')->nullable()
                  ->after('reserved_by_user')
                  ->comment('保留时间');

            $table->index('reserved_by_user');
        });
    }

    public function down(): void
    {
        Schema::table('ip_addresses', function (Blueprint $table) {
            $table->dropIndex(['reserved_by_user']);
            $table->dropColumn(['reserved_by_user', 'reserved_at']);
        });
    }
};
