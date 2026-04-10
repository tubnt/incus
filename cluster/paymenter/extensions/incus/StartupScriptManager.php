<?php

namespace App\Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

/**
 * 启动脚本管理
 *
 * 管理 cloud-init 脚本的复用，支持 bash 和 cloud-init YAML 两种类型。
 * 创建 VM 时可选择已保存的脚本注入 cloud-init 配置。
 */
class StartupScriptManager
{
    private IncusClient $client;

    /** 脚本类型 */
    public const TYPE_BASH = 'bash';
    public const TYPE_CLOUD_INIT = 'cloud-init';

    /** 脚本最大长度（字节） */
    private const MAX_SCRIPT_SIZE = 65535;

    /** 敏感命令模式（触发警告） */
    private const DANGEROUS_PATTERNS = [
        '/rm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)?\/\s/' => 'rm -rf / 危险：将删除整个文件系统',
        '/rm\s+-[a-zA-Z]*f[a-zA-Z]*\s+\/\*/' => 'rm -rf /* 危险：将删除根目录下所有文件',
        '/mkfs\.[a-z]+\s+\/dev\/[sv]da/' => '格式化系统盘操作：可能破坏系统',
        '/dd\s+.*of=\/dev\/[sv]da/' => 'dd 写入系统盘：可能破坏系统',
        '/curl\s+[^\|]*\|\s*(ba)?sh/' => 'curl | bash 远程执行：存在安全风险',
        '/wget\s+[^\|]*\|\s*(ba)?sh/' => 'wget | bash 远程执行：存在安全风险',
        '/chmod\s+777\s+\//' => 'chmod 777 / 危险：破坏系统权限',
        '/:\(\)\s*\{\s*:\|\:\s*&\s*\}\s*;/' => 'Fork bomb：将耗尽系统资源',
        '/>\s*\/dev\/[sv]da/' => '重定向到系统盘：可能破坏系统',
    ];

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    /**
     * 创建启动脚本
     *
     * @param int $userId 所属用户 ID
     * @param string $name 脚本名称
     * @param string $script 脚本内容
     * @param string $type 类型：bash 或 cloud-init
     * @return int 脚本 ID
     * @throws \InvalidArgumentException
     */
    public function create(int $userId, string $name, string $script, string $type = self::TYPE_BASH): int
    {
        $this->validateName($name);
        $this->validateType($type);
        $this->validateScript($script, $type);

        // 安全检查（返回警告但不阻止）
        $warnings = $this->checkDangerousPatterns($script);

        $id = DB::table('startup_scripts')->insertGetId([
            'user_id' => $userId,
            'name' => $name,
            'script' => $script,
            'type' => $type,
            'warnings' => !empty($warnings) ? json_encode($warnings) : null,
            'created_at' => now(),
            'updated_at' => now(),
        ]);

        if (!empty($warnings)) {
            Log::warning("启动脚本 #{$id} 包含潜在危险命令", [
                'user_id' => $userId,
                'warnings' => $warnings,
            ]);
        }

        return $id;
    }

    /**
     * 更新启动脚本
     */
    public function update(int $scriptId, int $userId, array $data): void
    {
        $script = $this->getScript($scriptId, $userId);
        if (!$script) {
            throw new \RuntimeException("脚本不存在或无权操作");
        }

        $update = ['updated_at' => now()];

        if (isset($data['name'])) {
            $this->validateName($data['name']);
            $update['name'] = $data['name'];
        }

        if (isset($data['script'])) {
            $type = $data['type'] ?? $script->type;
            $this->validateScript($data['script'], $type);

            $warnings = $this->checkDangerousPatterns($data['script']);
            $update['script'] = $data['script'];
            $update['warnings'] = !empty($warnings) ? json_encode($warnings) : null;
        }

        if (isset($data['type'])) {
            $this->validateType($data['type']);
            $update['type'] = $data['type'];
        }

        DB::table('startup_scripts')
            ->where('id', $scriptId)
            ->where('user_id', $userId)
            ->update($update);
    }

    /**
     * 删除启动脚本
     */
    public function delete(int $scriptId, int $userId): void
    {
        $deleted = DB::table('startup_scripts')
            ->where('id', $scriptId)
            ->where('user_id', $userId)
            ->delete();

        if ($deleted === 0) {
            throw new \RuntimeException("脚本不存在或无权操作");
        }
    }

    /**
     * 获取用户的所有启动脚本
     *
     * @return array
     */
    public function list(int $userId): array
    {
        return DB::table('startup_scripts')
            ->where('user_id', $userId)
            ->orderByDesc('updated_at')
            ->get()
            ->map(function ($row) {
                $row->warnings = $row->warnings ? json_decode($row->warnings, true) : [];
                return $row;
            })
            ->toArray();
    }

    /**
     * 获取单个脚本详情
     */
    public function getScript(int $scriptId, int $userId): ?object
    {
        $script = DB::table('startup_scripts')
            ->where('id', $scriptId)
            ->where('user_id', $userId)
            ->first();

        if ($script && $script->warnings) {
            $script->warnings = json_decode($script->warnings, true);
        }

        return $script;
    }

    /**
     * 将脚本应用到 VM（通过 cloud-init 配置注入）
     *
     * @param int $scriptId 脚本 ID
     * @param string $vmName VM 实例名称
     * @param int $userId 操作用户 ID
     * @throws \RuntimeException
     */
    public function applyToVm(int $scriptId, string $vmName, int $userId): void
    {
        $script = $this->getScript($scriptId, $userId);
        if (!$script) {
            throw new \RuntimeException("脚本不存在或无权操作");
        }

        $cloudInitConfig = $this->buildCloudInitConfig($script->script, $script->type);

        // 获取当前 VM 配置
        $instance = $this->client->request('GET', "/1.0/instances/{$vmName}");
        $config = $instance['metadata']['config'] ?? [];

        // 注入 cloud-init 用户数据
        $config['user.user-data'] = $cloudInitConfig;
        $config['user.startup_script_id'] = (string) $scriptId;

        $this->client->request('PATCH', "/1.0/instances/{$vmName}", [
            'config' => $config,
        ]);

        Log::info("启动脚本 #{$scriptId} 已应用到 VM {$vmName}");
    }

    /**
     * 构建 cloud-init 配置
     */
    private function buildCloudInitConfig(string $script, string $type): string
    {
        if ($type === self::TYPE_CLOUD_INIT) {
            // cloud-init YAML 直接使用
            return $script;
        }

        // bash 脚本包装为 cloud-init 格式
        return "#cloud-config\nruncmd:\n  - |\n" .
            implode("\n", array_map(
                fn($line) => "    {$line}",
                explode("\n", $script)
            ));
    }

    /**
     * 检查脚本中的危险命令
     *
     * @return string[] 警告信息列表
     */
    private function checkDangerousPatterns(string $script): array
    {
        $warnings = [];

        foreach (self::DANGEROUS_PATTERNS as $pattern => $message) {
            if (preg_match($pattern, $script)) {
                $warnings[] = $message;
            }
        }

        return $warnings;
    }

    private function validateName(string $name): void
    {
        if ($name === '' || mb_strlen($name) > 128) {
            throw new \InvalidArgumentException('脚本名称长度需在 1-128 字符之间');
        }
    }

    private function validateType(string $type): void
    {
        if (!in_array($type, [self::TYPE_BASH, self::TYPE_CLOUD_INIT], true)) {
            throw new \InvalidArgumentException("不支持的脚本类型：{$type}，仅支持 bash 和 cloud-init");
        }
    }

    private function validateScript(string $script, string $type): void
    {
        if ($script === '') {
            throw new \InvalidArgumentException('脚本内容不能为空');
        }

        if (strlen($script) > self::MAX_SCRIPT_SIZE) {
            throw new \InvalidArgumentException(
                '脚本内容超出最大长度限制（' . self::MAX_SCRIPT_SIZE . ' 字节）'
            );
        }

        // cloud-init YAML 基本格式校验
        if ($type === self::TYPE_CLOUD_INIT) {
            if (!str_starts_with(trim($script), '#cloud-config')) {
                throw new \InvalidArgumentException(
                    'cloud-init 脚本必须以 #cloud-config 开头'
                );
            }
        }
    }
}
