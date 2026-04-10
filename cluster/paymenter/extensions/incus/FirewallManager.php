<?php

namespace App\Extensions\Incus;

/**
 * 用户防火墙管理器 — 基于 Incus Network ACL
 *
 * ACL 命名规范：acl-order-{order_id}
 * 默认策略：ingress drop + egress allow + 放行 SSH 22
 * 规则上限：50 条/VM
 * 安全限制：拒绝 RFC1918 源地址（首期无内网功能）
 */
class FirewallManager
{
    /** 每个 VM 最大规则数 */
    private const MAX_RULES_PER_VM = 50;

    /** 允许的协议列表 */
    private const ALLOWED_PROTOCOLS = ['tcp', 'udp', 'icmp'];

    /** RFC1918 私有地址段 — 禁止作为源地址 */
    private const RFC1918_RANGES = [
        ['start' => '10.0.0.0',     'end' => '10.255.255.255',   'cidr' => 8],
        ['start' => '172.16.0.0',   'end' => '172.31.255.255',   'cidr' => 12],
        ['start' => '192.168.0.0',  'end' => '192.168.255.255',  'cidr' => 16],
    ];

    private IncusClient $client;

    public function __construct(IncusClient $client)
    {
        $this->client = $client;
    }

    /**
     * 获取 ACL 名称
     */
    private function getAclName(int $orderId): string
    {
        return 'acl-order-' . $orderId;
    }

    /**
     * 创建默认 ACL（ingress drop + 放行 SSH 22）并绑定到 VM
     */
    public function createDefaultAcl(int $orderId, string $vmName): array
    {
        $aclName = $this->getAclName($orderId);

        // 创建 ACL：默认放行 SSH
        $result = $this->client->request('POST', '/1.0/network-acls?project=customers', [
            'name'    => $aclName,
            'ingress' => [
                [
                    'action'           => 'allow',
                    'protocol'         => 'tcp',
                    'destination_port' => '22',
                    'source'           => '0.0.0.0/0',
                    'description'      => 'Allow SSH',
                ],
            ],
            'egress' => [],
        ]);

        // 绑定 ACL 到 VM 网卡
        $this->bindAclToVm($vmName, $aclName);

        return $result;
    }

    /**
     * 将 ACL 绑定到 VM 的 eth0 设备
     */
    private function bindAclToVm(string $vmName, string $aclName): array
    {
        return $this->client->request('PATCH', '/1.0/instances/' . $vmName . '?project=customers', [
            'devices' => [
                'eth0' => [
                    'security.acls'                        => $aclName,
                    'security.acls.default.ingress.action' => 'drop',
                    'security.acls.default.egress.action'  => 'allow',
                ],
            ],
        ]);
    }

    /**
     * 获取防火墙规则列表
     */
    public function getFirewallRules(string $vmName, int $orderId): array
    {
        $aclName = $this->getAclName($orderId);

        $response = $this->client->request('GET', '/1.0/network-acls/' . $aclName . '?project=customers');

        return $response['metadata']['ingress'] ?? [];
    }

    /**
     * 添加防火墙规则
     *
     * @param array $rule ['protocol' => 'tcp', 'destination_port' => '80', 'source' => '0.0.0.0/0', 'description' => '']
     * @throws \InvalidArgumentException 规则校验失败
     * @throws \OverflowException 超出规则数量限制
     */
    public function addFirewallRule(string $vmName, int $orderId, array $rule): array
    {
        // 校验规则
        $this->validateRule($rule);

        $aclName = $this->getAclName($orderId);

        // 获取当前 ACL
        $response = $this->client->request('GET', '/1.0/network-acls/' . $aclName . '?project=customers');
        $acl = $response['metadata'];
        $currentRules = $acl['ingress'] ?? [];

        // 检查规则数量上限
        if (count($currentRules) >= self::MAX_RULES_PER_VM) {
            throw new \OverflowException(
                '防火墙规则数量已达上限（' . self::MAX_RULES_PER_VM . ' 条），请删除不需要的规则后再添加'
            );
        }

        // 构造 Incus ACL 规则格式
        $newRule = [
            'action'           => 'allow',
            'protocol'         => strtolower($rule['protocol']),
            'destination_port' => (string) $rule['destination_port'],
            'source'           => $rule['source'],
            'description'      => $rule['description'] ?? '',
        ];

        // ICMP 不需要端口
        if ($newRule['protocol'] === 'icmp') {
            unset($newRule['destination_port']);
        }

        $currentRules[] = $newRule;

        // PATCH 更新 ACL
        return $this->client->request('PATCH', '/1.0/network-acls/' . $aclName . '?project=customers', [
            'ingress' => $currentRules,
        ]);
    }

    /**
     * 删除防火墙规则（按索引）
     *
     * @param int $ruleIndex 规则索引（从 0 开始）
     * @throws \OutOfRangeException 索引越界
     */
    public function removeFirewallRule(string $vmName, int $orderId, int $ruleIndex): array
    {
        $aclName = $this->getAclName($orderId);

        // 获取当前 ACL
        $response = $this->client->request('GET', '/1.0/network-acls/' . $aclName . '?project=customers');
        $acl = $response['metadata'];
        $currentRules = $acl['ingress'] ?? [];

        if ($ruleIndex < 0 || $ruleIndex >= count($currentRules)) {
            throw new \OutOfRangeException('规则索引越界：' . $ruleIndex);
        }

        // 移除指定索引的规则
        array_splice($currentRules, $ruleIndex, 1);

        // PATCH 更新 ACL
        return $this->client->request('PATCH', '/1.0/network-acls/' . $aclName . '?project=customers', [
            'ingress' => $currentRules,
        ]);
    }

    /**
     * 删除整个 ACL（VM 销毁时调用）
     */
    public function deleteAcl(int $orderId): array
    {
        $aclName = $this->getAclName($orderId);

        return $this->client->request('DELETE', '/1.0/network-acls/' . $aclName . '?project=customers');
    }

    /**
     * 校验防火墙规则
     *
     * @throws \InvalidArgumentException
     */
    private function validateRule(array $rule): void
    {
        // 协议校验
        if (empty($rule['protocol'])) {
            throw new \InvalidArgumentException('协议不能为空');
        }
        $protocol = strtolower($rule['protocol']);
        if (!in_array($protocol, self::ALLOWED_PROTOCOLS, true)) {
            throw new \InvalidArgumentException(
                '不支持的协议：' . $rule['protocol'] . '，仅支持 ' . implode('/', self::ALLOWED_PROTOCOLS)
            );
        }

        // TCP/UDP 必须指定端口
        if ($protocol !== 'icmp') {
            if (empty($rule['destination_port'])) {
                throw new \InvalidArgumentException('TCP/UDP 规则必须指定目标端口');
            }
            $this->validatePort($rule['destination_port']);
        }

        // 源地址校验
        if (empty($rule['source'])) {
            throw new \InvalidArgumentException('源地址不能为空');
        }
        $this->validateSource($rule['source']);
    }

    /**
     * 校验端口格式（支持单端口、逗号分隔、范围）
     *
     * @throws \InvalidArgumentException
     */
    private function validatePort(string $port): void
    {
        // 支持格式：80, 80,443, 8000-9000
        $segments = explode(',', $port);
        foreach ($segments as $segment) {
            $segment = trim($segment);
            if (str_contains($segment, '-')) {
                $parts = explode('-', $segment);
                if (count($parts) !== 2) {
                    throw new \InvalidArgumentException('端口范围格式错误：' . $segment);
                }
                $start = (int) $parts[0];
                $end   = (int) $parts[1];
                if ($start < 1 || $start > 65535 || $end < 1 || $end > 65535 || $start > $end) {
                    throw new \InvalidArgumentException('端口范围无效：' . $segment);
                }
            } else {
                $p = (int) $segment;
                if ($p < 1 || $p > 65535) {
                    throw new \InvalidArgumentException('端口号无效：' . $segment . '（有效范围 1-65535）');
                }
            }
        }
    }

    /**
     * 校验源地址 — 拒绝 RFC1918 私有地址段
     *
     * @throws \InvalidArgumentException
     */
    private function validateSource(string $source): void
    {
        // 允许 0.0.0.0/0（全部）
        if ($source === '0.0.0.0/0') {
            return;
        }

        // 解析 CIDR
        $parts = explode('/', $source);
        $ip = $parts[0];

        if (!filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4)) {
            throw new \InvalidArgumentException('无效的 IPv4 地址：' . $source);
        }

        // 检查是否落入 RFC1918 范围
        if ($this->isRfc1918($ip)) {
            throw new \InvalidArgumentException(
                '禁止使用 RFC1918 私有地址作为源地址：' . $source .
                '（10.0.0.0/8、172.16.0.0/12、192.168.0.0/16 均不允许）'
            );
        }

        // 校验 CIDR 掩码
        if (isset($parts[1])) {
            $mask = (int) $parts[1];
            if ($mask < 0 || $mask > 32) {
                throw new \InvalidArgumentException('无效的 CIDR 掩码：/' . $parts[1]);
            }
        }
    }

    /**
     * 检查 IP 是否在 RFC1918 范围内
     */
    private function isRfc1918(string $ip): bool
    {
        $ipLong = ip2long($ip);
        if ($ipLong === false) {
            return false;
        }

        foreach (self::RFC1918_RANGES as $range) {
            $startLong = ip2long($range['start']);
            $endLong   = ip2long($range['end']);
            if ($ipLong >= $startLong && $ipLong <= $endLong) {
                return true;
            }
        }

        return false;
    }
}
