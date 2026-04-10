<?php

namespace App\Extensions\Incus;

use Exception;
use Illuminate\Support\Facades\Log;

/**
 * ObjectStorageManager — Ceph RGW S3 对象存储管理器
 *
 * 通过 radosgw-admin 命令行管理用户、Bucket、配额和用量查询。
 */
class ObjectStorageManager
{
    /** @var string radosgw-admin 可执行路径 */
    protected string $radosgwAdmin;

    /** @var string S3 endpoint URL */
    protected string $s3Endpoint;

    public function __construct()
    {
        $this->radosgwAdmin = config('incus.radosgw_admin_path', '/usr/bin/radosgw-admin');
        $this->s3Endpoint   = config('incus.s3_endpoint', 'https://localhost:7443');
    }

    // ----------------------------------------------------------------
    // 用户管理
    // ----------------------------------------------------------------

    /**
     * 创建对象存储用户
     *
     * @param  string $userId 用户唯一标识
     * @return array  包含 access_key / secret_key 的用户信息
     * @throws Exception
     */
    public function createUser(string $userId): array
    {
        $displayName = "user-{$userId}";

        $output = $this->exec(
            'user create',
            [
                '--uid'          => $userId,
                '--display-name' => $displayName,
            ]
        );

        $data = json_decode($output, true);
        if (json_last_error() !== JSON_ERROR_NONE) {
            throw new Exception("创建用户失败：无法解析 radosgw-admin 输出");
        }

        Log::info("对象存储用户已创建", ['uid' => $userId]);

        return $data;
    }

    /**
     * 获取用户 Access Key / Secret Key
     *
     * @param  string $userId
     * @return array{access_key: string, secret_key: string}
     * @throws Exception
     */
    public function getCredentials(string $userId): array
    {
        $output = $this->exec('user info', ['--uid' => $userId]);
        $data   = json_decode($output, true);

        if (json_last_error() !== JSON_ERROR_NONE || empty($data['keys'])) {
            throw new Exception("获取凭据失败：用户 {$userId} 不存在或无密钥");
        }

        return [
            'access_key' => $data['keys'][0]['access_key'],
            'secret_key' => $data['keys'][0]['secret_key'],
        ];
    }

    // ----------------------------------------------------------------
    // Bucket 管理
    // ----------------------------------------------------------------

    /**
     * 创建 Bucket
     *
     * @param  string $userId Bucket 归属用户
     * @param  string $name   Bucket 名称
     * @return array
     * @throws Exception
     */
    public function createBucket(string $userId, string $name): array
    {
        // Bucket 名称校验：仅允许小写字母、数字、连字符、点，长度 3-63
        if (!preg_match('/^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$/', $name)) {
            throw new Exception("Bucket 名称不合法（仅允许小写字母、数字、连字符、点，长度 3-63）: {$name}");
        }

        // 通过 S3 API 创建 bucket（使用用户凭据）
        $credentials = $this->getCredentials($userId);

        // 通过环境变量传递凭据，避免出现在命令行参数中
        $command = sprintf(
            'aws s3 mb s3://%s --endpoint-url=%s --no-verify-ssl 2>&1',
            escapeshellarg($name),
            escapeshellarg($this->s3Endpoint)
        );

        $env = [
            'AWS_ACCESS_KEY_ID'     => $credentials['access_key'],
            'AWS_SECRET_ACCESS_KEY' => $credentials['secret_key'],
        ];

        $descriptors = [
            0 => ['pipe', 'r'],
            1 => ['pipe', 'w'],
            2 => ['pipe', 'w'],
        ];

        $process = proc_open($command, $descriptors, $pipes, null, $env + $_ENV);
        if (!is_resource($process)) {
            throw new Exception("无法启动 aws cli 进程");
        }

        fclose($pipes[0]);
        $output = stream_get_contents($pipes[1]);
        fclose($pipes[1]);
        $stderr = stream_get_contents($pipes[2]);
        fclose($pipes[2]);
        $returnCode = proc_close($process);

        if ($returnCode !== 0) {
            throw new Exception("创建 Bucket 失败: " . trim($output . ' ' . $stderr));
        }

        Log::info("Bucket 已创建", ['uid' => $userId, 'bucket' => $name]);

        return [
            'bucket'   => $name,
            'owner'    => $userId,
            'endpoint' => $this->s3Endpoint,
        ];
    }

    /**
     * 删除 Bucket
     *
     * @param  string $userId
     * @param  string $name   Bucket 名称
     * @return bool
     * @throws Exception
     */
    public function deleteBucket(string $userId, string $name): bool
    {
        $this->exec('bucket rm', [
            '--bucket'        => $name,
            '--purge-objects' => null,
        ]);

        Log::info("Bucket 已删除", ['uid' => $userId, 'bucket' => $name]);

        return true;
    }

    /**
     * 列出用户的所有 Bucket
     *
     * @param  string $userId
     * @return array
     * @throws Exception
     */
    public function listBuckets(string $userId): array
    {
        $output = $this->exec('bucket list', ['--uid' => $userId]);
        $data   = json_decode($output, true);

        if (json_last_error() !== JSON_ERROR_NONE) {
            throw new Exception("列出 Bucket 失败：无法解析输出");
        }

        return $data ?? [];
    }

    // ----------------------------------------------------------------
    // 用量与配额
    // ----------------------------------------------------------------

    /**
     * 获取用户存储用量
     *
     * @param  string $userId
     * @return array{size_kb: int, num_objects: int, size_gb: float}
     * @throws Exception
     */
    public function getUsage(string $userId): array
    {
        $output = $this->exec('user stats', ['--uid' => $userId, '--sync-stats' => null]);
        $data   = json_decode($output, true);

        if (json_last_error() !== JSON_ERROR_NONE) {
            throw new Exception("获取用量失败：无法解析输出");
        }

        $stats = $data['stats'] ?? [];

        return [
            'size_kb'     => $stats['size_kb'] ?? 0,
            'num_objects' => $stats['num_objects'] ?? 0,
            'size_gb'     => round(($stats['size_kb'] ?? 0) / 1024 / 1024, 3),
        ];
    }

    /**
     * 设置用户存储配额
     *
     * @param  string $userId
     * @param  int    $maxSizeGb 最大存储容量 (GB)
     * @return bool
     * @throws Exception
     */
    public function setQuota(string $userId, int $maxSizeGb): bool
    {
        // 启用配额
        $this->exec('quota enable', [
            '--quota-scope' => 'user',
            '--uid'         => $userId,
        ]);

        // 设置容量上限
        $maxSizeBytes = $maxSizeGb * 1024 * 1024 * 1024;
        $this->exec('quota set', [
            '--quota-scope' => 'user',
            '--uid'         => $userId,
            '--max-size'    => (string) $maxSizeBytes,
        ]);

        Log::info("用户配额已设置", ['uid' => $userId, 'max_size_gb' => $maxSizeGb]);

        return true;
    }

    // ----------------------------------------------------------------
    // 内部方法
    // ----------------------------------------------------------------

    /** 允许的 radosgw-admin 子命令白名单 */
    private const ALLOWED_SUBCOMMANDS = [
        'user create', 'user info', 'user stats',
        'bucket list', 'bucket rm',
        'quota enable', 'quota set',
    ];

    /**
     * 执行 radosgw-admin 命令
     *
     * @param  string $subCommand 子命令（如 "user create"），必须在白名单内
     * @param  array  $args       参数 key-value
     * @return string 命令输出
     * @throws Exception
     */
    protected function exec(string $subCommand, array $args = []): string
    {
        if (!in_array($subCommand, self::ALLOWED_SUBCOMMANDS, true)) {
            throw new Exception("不允许的 radosgw-admin 子命令: {$subCommand}");
        }

        $cmd = escapeshellarg($this->radosgwAdmin) . ' ' . $subCommand;

        foreach ($args as $key => $value) {
            // 参数名必须以 -- 开头，防止注入
            if (!preg_match('/^--[a-z][a-z0-9-]*$/', $key)) {
                throw new Exception("非法的命令参数名: {$key}");
            }
            if ($value === null) {
                // 无值开关参数
                $cmd .= ' ' . $key;
            } else {
                $cmd .= ' ' . $key . '=' . escapeshellarg($value);
            }
        }

        $cmd .= ' 2>&1';

        $outputLines = [];
        $returnCode  = 0;
        exec($cmd, $outputLines, $returnCode);
        $output = implode("\n", $outputLines);

        if ($returnCode !== 0) {
            Log::error("radosgw-admin 命令执行失败", [
                'command'     => $subCommand,
                'return_code' => $returnCode,
                'output'      => $output,
            ]);
            throw new Exception("radosgw-admin 命令失败 ({$subCommand}): {$output}");
        }

        return $output;
    }
}
