<?php

namespace Extensions\Incus\Api;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Routing\Controller;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

class ApiController extends Controller
{
    private ApiTokenManager $tokenManager;

    public function __construct(ApiTokenManager $tokenManager)
    {
        $this->tokenManager = $tokenManager;
    }

    // ============================
    // 实例管理
    // ============================

    /**
     * GET /api/v1/instances — 列出用户所有 VM
     */
    public function listInstances(Request $request): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        $instances = DB::table('orders')
            ->join('order_products', 'orders.id', '=', 'order_products.order_id')
            ->leftJoin('ip_addresses', 'orders.id', '=', 'ip_addresses.order_id')
            ->where('orders.user_id', $userId)
            ->where('order_products.server_type', 'incus')
            ->select([
                'orders.id',
                'order_products.vm_name',
                'order_products.status',
                'order_products.cpu',
                'order_products.memory',
                'order_products.disk',
                'ip_addresses.ip',
                'orders.due_date',
                'orders.created_at',
            ])
            ->get();

        return response()->json([
            'data'  => $instances,
            'total' => $instances->count(),
        ]);
    }

    /**
     * GET /api/v1/instances/{id} — VM 详情
     */
    public function getInstance(Request $request, int $id): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        $instance = DB::table('orders')
            ->join('order_products', 'orders.id', '=', 'order_products.order_id')
            ->leftJoin('ip_addresses', 'orders.id', '=', 'ip_addresses.order_id')
            ->where('orders.id', $id)
            ->where('orders.user_id', $userId)
            ->where('order_products.server_type', 'incus')
            ->select([
                'orders.id',
                'order_products.vm_name',
                'order_products.status',
                'order_products.cpu',
                'order_products.memory',
                'order_products.disk',
                'order_products.bandwidth_limit',
                'order_products.bandwidth_used',
                'order_products.os_template',
                'ip_addresses.ip',
                'orders.due_date',
                'orders.created_at',
            ])
            ->first();

        if (!$instance) {
            return response()->json(['error' => 'not_found', 'message' => '实例不存在'], 404);
        }

        return response()->json(['data' => $instance]);
    }

    /**
     * POST /api/v1/instances/{id}/actions — 操作（start/stop/reboot/reinstall）
     */
    public function instanceAction(Request $request, int $id): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');
        $action = $request->input('action');

        $allowedActions = ['start', 'stop', 'reboot', 'reinstall'];
        if (!in_array($action, $allowedActions, true)) {
            return response()->json([
                'error'   => 'invalid_action',
                'message' => '无效操作，允许: ' . implode(', ', $allowedActions),
            ], 422);
        }

        $instance = $this->findUserInstance($userId, $id);
        if (!$instance) {
            return response()->json(['error' => 'not_found', 'message' => '实例不存在'], 404);
        }

        // 并发锁：同一 VM 同一时间只能执行一个操作
        $lockKey = "vm_action:{$instance->vm_name}";
        $lock = cache()->lock($lockKey, 120);

        if (!$lock->get()) {
            return response()->json([
                'error'   => 'conflict',
                'message' => '该实例正在执行其他操作，请稍后重试',
            ], 409);
        }

        try {
            $incusClient = $this->getIncusClient();
            $vmName = $instance->vm_name;

            $result = match ($action) {
                'start'   => $incusClient->request('PUT', "/1.0/instances/{$vmName}/state", [
                    'action' => 'start', 'timeout' => 30,
                ]),
                'stop'    => $incusClient->request('PUT', "/1.0/instances/{$vmName}/state", [
                    'action' => 'stop', 'timeout' => 30, 'force' => false,
                ]),
                'reboot'  => $incusClient->request('PUT', "/1.0/instances/{$vmName}/state", [
                    'action' => 'restart', 'timeout' => 30,
                ]),
                'reinstall' => $this->handleReinstall($instance, $request),
            };

            Log::info("API 操作: user={$userId} vm={$vmName} action={$action}");

            return response()->json([
                'message' => "操作 {$action} 已提交",
                'status'  => 'accepted',
            ], 202);
        } catch (\InvalidArgumentException $e) {
            return response()->json([
                'error'   => 'validation_error',
                'message' => $e->getMessage(),
            ], 422);
        } finally {
            $lock->release();
        }
    }

    // ============================
    // 快照管理
    // ============================

    /**
     * GET /api/v1/instances/{id}/snapshots — 快照列表
     */
    public function listSnapshots(Request $request, int $id): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        $instance = $this->findUserInstance($userId, $id);
        if (!$instance) {
            return response()->json(['error' => 'not_found', 'message' => '实例不存在'], 404);
        }

        $incusClient = $this->getIncusClient();
        $vmName = $instance->vm_name;
        $response = $incusClient->request('GET', "/1.0/instances/{$vmName}/snapshots?recursion=1");

        $snapshots = array_map(function ($snap) {
            return [
                'name'       => $snap['name'] ?? basename($snap),
                'created_at' => $snap['created_at'] ?? null,
                'stateful'   => $snap['stateful'] ?? false,
            ];
        }, $response['metadata'] ?? []);

        return response()->json(['data' => $snapshots]);
    }

    /**
     * POST /api/v1/instances/{id}/snapshots — 创建快照
     */
    public function createSnapshot(Request $request, int $id): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        $instance = $this->findUserInstance($userId, $id);
        if (!$instance) {
            return response()->json(['error' => 'not_found', 'message' => '实例不存在'], 404);
        }

        // 快照数量限制
        $incusClient = $this->getIncusClient();
        $vmName = $instance->vm_name;
        $existing = $incusClient->request('GET', "/1.0/instances/{$vmName}/snapshots");
        if (count($existing['metadata'] ?? []) >= 5) {
            return response()->json([
                'error'   => 'limit_exceeded',
                'message' => '快照数量已达上限（5 个），请删除旧快照后重试',
            ], 422);
        }

        $snapshotName = $request->input('name', 'snap-' . date('Ymd-His'));
        $snapshotName = preg_replace('/[^a-zA-Z0-9\-_]/', '', $snapshotName);

        $incusClient->request('POST', "/1.0/instances/{$vmName}/snapshots", [
            'name'     => $snapshotName,
            'stateful' => false,
        ]);

        Log::info("API 创建快照: user={$userId} vm={$vmName} snapshot={$snapshotName}");

        return response()->json([
            'message' => '快照创建已提交',
            'name'    => $snapshotName,
        ], 201);
    }

    // ============================
    // 防火墙
    // ============================

    /**
     * GET /api/v1/instances/{id}/firewall — 防火墙规则
     */
    public function getFirewall(Request $request, int $id): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        $instance = $this->findUserInstance($userId, $id);
        if (!$instance) {
            return response()->json(['error' => 'not_found', 'message' => '实例不存在'], 404);
        }

        $incusClient = $this->getIncusClient();
        $aclName = "acl-order-{$id}";
        $response = $incusClient->request('GET', "/1.0/network-acls/{$aclName}?project=customers");

        return response()->json([
            'data' => [
                'ingress' => $response['metadata']['ingress'] ?? [],
                'egress'  => $response['metadata']['egress'] ?? [],
            ],
        ]);
    }

    /**
     * PATCH /api/v1/instances/{id}/firewall — 更新防火墙
     */
    public function updateFirewall(Request $request, int $id): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        $instance = $this->findUserInstance($userId, $id);
        if (!$instance) {
            return response()->json(['error' => 'not_found', 'message' => '实例不存在'], 404);
        }

        $rules = $request->input('ingress', []);

        if (!is_array($rules)) {
            return response()->json([
                'error'   => 'validation_error',
                'message' => 'ingress 必须是规则数组',
            ], 422);
        }

        // 规则数量限制
        if (count($rules) > 50) {
            return response()->json([
                'error'   => 'limit_exceeded',
                'message' => '防火墙规则数量上限为 50 条',
            ], 422);
        }

        // 校验每条规则的结构和取值
        $allowedActions = ['allow', 'drop', 'reject'];
        $allowedProtocols = ['tcp', 'udp', 'icmp4', 'icmp6'];
        $allowedFields = ['action', 'source', 'destination', 'protocol', 'source_port', 'destination_port', 'description', 'state'];

        foreach ($rules as $i => $rule) {
            if (!is_array($rule)) {
                return response()->json([
                    'error'   => 'invalid_rule',
                    'message' => "规则 #{$i}: 必须是对象",
                ], 422);
            }

            // 拒绝未知字段
            $unknownFields = array_diff(array_keys($rule), $allowedFields);
            if (!empty($unknownFields)) {
                return response()->json([
                    'error'   => 'invalid_rule',
                    'message' => "规则 #{$i}: 包含未知字段: " . implode(', ', $unknownFields),
                ], 422);
            }

            // action 必填且合法
            if (!isset($rule['action']) || !in_array($rule['action'], $allowedActions, true)) {
                return response()->json([
                    'error'   => 'invalid_rule',
                    'message' => "规则 #{$i}: action 必须是 " . implode('/', $allowedActions),
                ], 422);
            }

            // protocol 校验
            if (isset($rule['protocol']) && !in_array($rule['protocol'], $allowedProtocols, true)) {
                return response()->json([
                    'error'   => 'invalid_rule',
                    'message' => "规则 #{$i}: protocol 必须是 " . implode('/', $allowedProtocols),
                ], 422);
            }

            // 拒绝 RFC1918 源地址
            if (isset($rule['source']) && $this->isRfc1918($rule['source'])) {
                return response()->json([
                    'error'   => 'invalid_rule',
                    'message' => "规则 #{$i}: 不允许使用 RFC1918 私有地址作为源地址: " . $rule['source'],
                ], 422);
            }

            // 端口格式校验（数字或范围如 80,443 或 8000-9000）
            foreach (['source_port', 'destination_port'] as $portField) {
                if (isset($rule[$portField]) && !preg_match('/^[0-9,\-]+$/', (string) $rule[$portField])) {
                    return response()->json([
                        'error'   => 'invalid_rule',
                        'message' => "规则 #{$i}: {$portField} 格式无效",
                    ], 422);
                }
            }
        }

        $incusClient = $this->getIncusClient();
        $aclName = "acl-order-{$id}";
        $incusClient->request('PATCH', "/1.0/network-acls/{$aclName}?project=customers", [
            'ingress' => $rules,
        ]);

        Log::info("API 更新防火墙: user={$userId} order={$id} rules=" . count($rules));

        return response()->json(['message' => '防火墙规则已更新']);
    }

    // ============================
    // 监控
    // ============================

    /**
     * GET /api/v1/instances/{id}/metrics — 资源监控
     */
    public function getMetrics(Request $request, int $id): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        $instance = $this->findUserInstance($userId, $id);
        if (!$instance) {
            return response()->json(['error' => 'not_found', 'message' => '实例不存在'], 404);
        }

        $incusClient = $this->getIncusClient();
        $vmName = $instance->vm_name;
        $response = $incusClient->request('GET', "/1.0/instances/{$vmName}/state");

        $state = $response['metadata'] ?? [];

        return response()->json([
            'data' => [
                'status'  => $state['status'] ?? 'Unknown',
                'cpu'     => [
                    'usage_ns' => $state['cpu']['usage'] ?? 0,
                ],
                'memory'  => [
                    'usage_bytes' => $state['memory']['usage'] ?? 0,
                    'peak_bytes'  => $state['memory']['usage_peak'] ?? 0,
                ],
                'disk'    => array_map(function ($disk) {
                    return [
                        'usage_bytes' => $disk['usage'] ?? 0,
                        'total_bytes' => $disk['total'] ?? 0,
                    ];
                }, $state['disk'] ?? []),
                'network' => array_map(function ($net) {
                    return [
                        'rx_bytes'   => $net['counters']['bytes_received'] ?? 0,
                        'tx_bytes'   => $net['counters']['bytes_sent'] ?? 0,
                        'rx_packets' => $net['counters']['packets_received'] ?? 0,
                        'tx_packets' => $net['counters']['packets_sent'] ?? 0,
                    ];
                }, $state['network'] ?? []),
            ],
        ]);
    }

    // ============================
    // 账户
    // ============================

    /**
     * GET /api/v1/account/balance — 账户余额
     */
    public function getBalance(Request $request): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        $balance = DB::table('users')
            ->where('id', $userId)
            ->value('balance') ?? 0;

        return response()->json([
            'data' => [
                'balance'  => (float) $balance,
                'currency' => 'USD',
            ],
        ]);
    }

    /**
     * GET /api/v1/account/invoices — 账单列表
     */
    public function listInvoices(Request $request): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');
        $page = max(1, (int) $request->input('page', 1));
        $perPage = min(50, max(1, (int) $request->input('per_page', 20)));

        $query = DB::table('invoices')
            ->where('user_id', $userId)
            ->orderByDesc('created_at');

        $total = $query->count();

        $invoices = $query
            ->offset(($page - 1) * $perPage)
            ->limit($perPage)
            ->select(['id', 'status', 'amount', 'currency', 'due_date', 'paid_at', 'created_at'])
            ->get();

        return response()->json([
            'data' => $invoices,
            'meta' => [
                'total'    => $total,
                'page'     => $page,
                'per_page' => $perPage,
            ],
        ]);
    }

    // ============================
    // Token 管理（用户自助）
    // ============================

    /**
     * GET /api/v1/tokens — 列出用户 Token
     */
    public function listTokens(Request $request): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');
        $tokens = $this->tokenManager->listTokens($userId);

        return response()->json(['data' => $tokens]);
    }

    /**
     * POST /api/v1/tokens — 创建 Token
     */
    public function createToken(Request $request): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');
        $name = $request->input('name');
        $permission = $request->input('permission', 'read-only');
        $customPermissions = $request->input('custom_permissions');
        $expiryDays = $request->has('expiry_days') ? (int) $request->input('expiry_days') : null;

        if (empty($name) || strlen($name) > 64) {
            return response()->json([
                'error'   => 'validation_error',
                'message' => 'Token 名称不能为空且不超过 64 字符',
            ], 422);
        }

        // 过滤 Token 名称中的控制字符
        $name = preg_replace('/[\x00-\x1F\x7F]/u', '', $name);

        try {
            $result = $this->tokenManager->createToken($userId, $name, $permission, $customPermissions, $expiryDays);
        } catch (\InvalidArgumentException $e) {
            return response()->json(['error' => 'validation_error', 'message' => $e->getMessage()], 422);
        } catch (\RuntimeException $e) {
            return response()->json(['error' => 'limit_exceeded', 'message' => $e->getMessage()], 422);
        }

        return response()->json([
            'message' => 'Token 已创建，请立即保存，此 Token 仅显示一次',
            'data'    => [
                'id'    => $result['id'],
                'token' => $result['token'],
            ],
        ], 201);
    }

    /**
     * DELETE /api/v1/tokens/{tokenId} — 吊销 Token
     */
    public function revokeToken(Request $request, int $tokenId): JsonResponse
    {
        $userId = $request->attributes->get('api_user_id');

        if (!$this->tokenManager->revokeToken($tokenId, $userId)) {
            return response()->json(['error' => 'not_found', 'message' => 'Token 不存在'], 404);
        }

        return response()->json(['message' => 'Token 已吊销']);
    }

    // ============================
    // 辅助方法
    // ============================

    private function findUserInstance(int $userId, int $orderId): ?object
    {
        return DB::table('orders')
            ->join('order_products', 'orders.id', '=', 'order_products.order_id')
            ->where('orders.id', $orderId)
            ->where('orders.user_id', $userId)
            ->where('order_products.server_type', 'incus')
            ->select(['orders.id', 'order_products.vm_name', 'order_products.status', 'order_products.os_template'])
            ->first();
    }

    /**
     * 检查 IP/CIDR 是否属于 RFC1918 私有地址空间
     */
    private function isRfc1918(string $cidr): bool
    {
        $ip = explode('/', $cidr)[0];
        $long = ip2long($ip);
        if ($long === false) {
            return false;
        }

        // 10.0.0.0/8
        if (($long & 0xFF000000) === 0x0A000000) return true;
        // 172.16.0.0/12
        if (($long & 0xFFF00000) === 0xAC100000) return true;
        // 192.168.0.0/16
        if (($long & 0xFFFF0000) === 0xC0A80000) return true;

        return false;
    }

    private function getIncusClient(): object
    {
        // 复用 Extension 内的 IncusClient（mTLS 连接）
        return app(\Extensions\Incus\IncusClient::class);
    }

    /** 允许重装的 OS 模板白名单 */
    private const ALLOWED_OS_TEMPLATES = [
        'ubuntu-22.04',
        'ubuntu-24.04',
        'debian-11',
        'debian-12',
        'rocky-8',
        'rocky-9',
        'alma-8',
        'alma-9',
        'centos-stream-9',
        'fedora-40',
        'arch',
        'opensuse-15.6',
        'windows-2022',
        'windows-2025',
    ];

    private function handleReinstall(object $instance, Request $request): array
    {
        $osTemplate = $request->input('os_template', $instance->os_template ?? 'ubuntu-24.04');

        if (!in_array($osTemplate, self::ALLOWED_OS_TEMPLATES, true)) {
            throw new \InvalidArgumentException(
                '不支持的操作系统模板: ' . $osTemplate . '，允许: ' . implode(', ', self::ALLOWED_OS_TEMPLATES)
            );
        }

        $incusClient = $this->getIncusClient();
        $vmName = $instance->vm_name;

        // 重装前自动创建安全快照
        $incusClient->request('POST', "/1.0/instances/{$vmName}/snapshots", [
            'name'     => 'pre-reinstall-' . date('Ymd-His'),
            'stateful' => false,
        ]);

        // 停止 → 重建（保留 IP）
        $incusClient->request('PUT', "/1.0/instances/{$vmName}/state", [
            'action' => 'stop', 'timeout' => 30, 'force' => true,
        ]);

        $incusClient->request('POST', "/1.0/instances/{$vmName}/rebuild", [
            'source' => ['type' => 'image', 'alias' => $osTemplate],
        ]);

        return ['status' => 'reinstalling'];
    }
}
