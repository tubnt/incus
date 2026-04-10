<?php
/**
 * IPv6 前缀管理器
 *
 * 从 /48 前缀池中为每个 VM 分配 /64 子网，
 * 并生成对应的 cloud-init 网络配置。
 */

namespace Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

class Ipv6Manager
{
    /** IPv6 /48 前缀 */
    private string $prefix48;

    /** 前缀长度（固定 48） */
    private int $prefixLen = 48;

    /** 每 VM 分配的前缀长度（固定 64） */
    private int $vmPrefixLen = 64;

    /** IPv6 网关 */
    private string $gateway;

    /** IPv6 DNS */
    private array $dnsServers;

    public function __construct()
    {
        $this->prefix48 = config('incus.ipv6.prefix', '');
        $this->gateway = config('incus.ipv6.gateway', 'fe80::1');
        $this->dnsServers = config('incus.ipv6.dns', ['2606:4700:4700::1111', '2001:4860:4860::8888']);
    }

    // ────────────────────────────────────────────
    //  前缀分配
    // ────────────────────────────────────────────

    /**
     * 为 VM 分配 /64 前缀
     *
     * 从 /48 前缀中按序分配 /64 子网。
     * /48 → /64 可用 65536 个子网（第 49-64 位）。
     *
     * @param string $vmName 虚拟机名称
     * @return array ['success' => bool, 'prefix' => string|null, 'message' => string]
     */
    public function allocatePrefix(string $vmName): array
    {
        if (empty($this->prefix48)) {
            return ['success' => false, 'prefix' => null, 'message' => 'IPv6 前缀未配置'];
        }

        return DB::transaction(function () use ($vmName) {
            // 检查是否已分配（加行锁防止并发重复）
            $existing = DB::table('ipv6_prefixes')
                ->where('vm_name', $vmName)
                ->where('status', 'allocated')
                ->lockForUpdate()
                ->first();

            if ($existing) {
                return [
                    'success' => true,
                    'prefix' => $existing->prefix,
                    'message' => "VM 已分配前缀: {$existing->prefix}",
                ];
            }

            // 查找下一个可用子网索引（事务内带锁）
            $nextIndex = $this->findNextAvailableIndex();
            if ($nextIndex === null) {
                return ['success' => false, 'prefix' => null, 'message' => 'IPv6 /64 前缀已耗尽'];
            }

            // 生成 /64 前缀
            $prefix64 = $this->generatePrefix64($nextIndex);

            // 写入数据库（subnet_index 有唯一索引，并发冲突会抛异常触发事务回滚）
            DB::table('ipv6_prefixes')->insert([
                'vm_name' => $vmName,
                'prefix' => $prefix64,
                'prefix_len' => $this->vmPrefixLen,
                'subnet_index' => $nextIndex,
                'status' => 'allocated',
                'allocated_at' => now(),
                'created_at' => now(),
                'updated_at' => now(),
            ]);

            Log::info("IPv6 前缀已分配: {$vmName} -> {$prefix64}/{$this->vmPrefixLen}");

            return [
                'success' => true,
                'prefix' => $prefix64,
                'message' => "已分配 {$prefix64}/{$this->vmPrefixLen}",
            ];
        });
    }

    /**
     * 查询 VM 的 IPv6 前缀
     *
     * @param string $vmName 虚拟机名称
     * @return array ['success' => bool, 'prefix' => string|null, 'prefix_len' => int, 'message' => string]
     */
    public function getPrefix(string $vmName): array
    {
        $record = DB::table('ipv6_prefixes')
            ->where('vm_name', $vmName)
            ->where('status', 'allocated')
            ->first();

        if (!$record) {
            return [
                'success' => false,
                'prefix' => null,
                'prefix_len' => $this->vmPrefixLen,
                'message' => "VM {$vmName} 未分配 IPv6 前缀",
            ];
        }

        return [
            'success' => true,
            'prefix' => $record->prefix,
            'prefix_len' => $record->prefix_len,
            'message' => "IPv6 前缀: {$record->prefix}/{$record->prefix_len}",
        ];
    }

    /**
     * 释放 VM 的 IPv6 前缀
     *
     * @param string $vmName 虚拟机名称
     * @return array ['success' => bool, 'message' => string]
     */
    public function releasePrefix(string $vmName): array
    {
        $affected = DB::table('ipv6_prefixes')
            ->where('vm_name', $vmName)
            ->where('status', 'allocated')
            ->update([
                'status' => 'released',
                'vm_name' => null,
                'released_at' => now(),
                'updated_at' => now(),
            ]);

        if ($affected === 0) {
            return ['success' => false, 'message' => "VM {$vmName} 无已分配的 IPv6 前缀"];
        }

        Log::info("IPv6 前缀已释放: {$vmName}");
        return ['success' => true, 'message' => "VM {$vmName} 的 IPv6 前缀已释放"];
    }

    // ────────────────────────────────────────────
    //  cloud-init 网络配置生成
    // ────────────────────────────────────────────

    /**
     * 生成 VM 的 cloud-init IPv6 网络配置（netplan 格式）
     *
     * 用于追加到现有的 cloud-init network-config 中。
     *
     * @param string $vmName 虚拟机名称
     * @param string|null $ipv4Config 现有 IPv4 配置（可选，用于合并）
     * @return array ['success' => bool, 'config' => string|null, 'message' => string]
     */
    public function generateCloudInitConfig(string $vmName, ?string $ipv4Config = null): array
    {
        $prefixInfo = $this->getPrefix($vmName);
        if (!$prefixInfo['success']) {
            return ['success' => false, 'config' => null, 'message' => $prefixInfo['message']];
        }

        $prefix = $prefixInfo['prefix'];
        $prefixLen = $prefixInfo['prefix_len'];

        // VM 使用前缀的 ::1 作为地址
        $vmIpv6 = $this->prefixToVmAddress($prefix);
        $dnsStr = implode(', ', $this->dnsServers);

        // 生成 netplan 格式的 IPv6 配置
        // 该配置需要与 IPv4 配置合并到同一个 ethernets 段
        $config = <<<YAML
      # IPv6 配置（追加到 ethernets.all-en 段）
      addresses:
        - {$vmIpv6}/{$prefixLen}
      routes:
        - to: "::/0"
          via: "{$this->gateway}"
      nameservers:
        addresses: [{$dnsStr}]
YAML;

        return [
            'success' => true,
            'config' => $config,
            'ipv6_address' => $vmIpv6,
            'prefix' => $prefix,
            'prefix_len' => $prefixLen,
            'message' => "已生成 IPv6 配置: {$vmIpv6}/{$prefixLen}",
        ];
    }

    /**
     * 生成完整的双栈 cloud-init network-config
     *
     * @param string $vmName   VM 名称
     * @param string $ipv4Addr IPv4 地址
     * @param string $ipv4Mask IPv4 子网掩码（CIDR 如 /26）
     * @param string $ipv4Gw   IPv4 网关
     * @param string $ipv4Dns  IPv4 DNS（逗号分隔）
     * @return array ['success' => bool, 'config' => string|null, 'message' => string]
     */
    public function generateDualStackConfig(
        string $vmName,
        string $ipv4Addr,
        string $ipv4Mask,
        string $ipv4Gw,
        string $ipv4Dns = '8.8.8.8,1.1.1.1'
    ): array {
        $prefixInfo = $this->getPrefix($vmName);
        if (!$prefixInfo['success']) {
            // 未分配 IPv6 — 返回纯 IPv4 配置
            return $this->generateIpv4OnlyConfig($ipv4Addr, $ipv4Mask, $ipv4Gw, $ipv4Dns);
        }

        $vmIpv6 = $this->prefixToVmAddress($prefixInfo['prefix']);
        $v6PrefixLen = $prefixInfo['prefix_len'];
        $v6DnsStr = implode(', ', $this->dnsServers);

        $config = <<<YAML
network:
  version: 2
  ethernets:
    all-en:
      match:
        name: "en*"
      dhcp4: false
      dhcp6: false
      addresses:
        - {$ipv4Addr}{$ipv4Mask}
        - {$vmIpv6}/{$v6PrefixLen}
      routes:
        - to: default
          via: {$ipv4Gw}
        - to: "::/0"
          via: "{$this->gateway}"
      nameservers:
        addresses: [{$ipv4Dns}, {$v6DnsStr}]
YAML;

        return [
            'success' => true,
            'config' => $config,
            'message' => "已生成双栈配置: {$ipv4Addr} + {$vmIpv6}/{$v6PrefixLen}",
        ];
    }

    // ────────────────────────────────────────────
    //  内部方法
    // ────────────────────────────────────────────

    /**
     * 查找下一个可用的子网索引
     *
     * 优先复用已释放的索引，否则取最大索引 + 1。
     * /48 → /64 共 65536 个子网（索引 0-65535）。
     * 索引 0 保留给宿主机网桥。
     */
    private function findNextAvailableIndex(): ?int
    {
        // 优先复用已释放的最小索引（带行锁，确保并发安全）
        $released = DB::table('ipv6_prefixes')
            ->where('status', 'released')
            ->orderBy('subnet_index')
            ->lockForUpdate()
            ->first();

        if ($released) {
            DB::table('ipv6_prefixes')->where('id', $released->id)->delete();
            return $released->subnet_index;
        }

        // 取最大已分配索引 + 1（事务内读取，配合唯一索引防重复）
        $maxIndex = DB::table('ipv6_prefixes')->lockForUpdate()->max('subnet_index');
        $nextIndex = ($maxIndex === null) ? 1 : $maxIndex + 1; // 0 保留给网桥

        // 检查是否耗尽（/48 → /64 = 65536 个子网）
        if ($nextIndex > 65535) {
            return null;
        }

        return $nextIndex;
    }

    /**
     * 根据子网索引生成 /64 前缀
     *
     * /48 前缀格式: 2001:db8:abcd::
     * /64 前缀: 2001:db8:abcd:XXXX:: 其中 XXXX 是子网索引（十六进制）
     */
    private function generatePrefix64(int $subnetIndex): string
    {
        // 将 /48 前缀转为二进制
        $prefix = rtrim($this->prefix48, ':');

        // 确保前缀有 3 组（/48 = 3 × 16 bits）
        $groups = explode(':', $prefix);
        // 过滤空字符串
        $groups = array_filter($groups, fn($g) => $g !== '');
        $groups = array_values($groups);

        // 补足到 3 组
        while (count($groups) < 3) {
            $groups[] = '0';
        }

        // 第 4 组为子网索引
        $groups[] = dechex($subnetIndex);

        return implode(':', $groups) . '::';
    }

    /**
     * 从 /64 前缀生成 VM 地址（使用 ::1）
     * 通过 inet_pton/inet_ntop 规范化，避免 rtrim 过度删除冒号
     */
    private function prefixToVmAddress(string $prefix): string
    {
        // 将前缀规范化为完整地址（补 0），然后设置最后 2 字节为 ::1
        $prefixAddr = rtrim($prefix, ':');
        // 补齐为合法 IPv6 地址用于解析
        $packed = @inet_pton($prefixAddr . '::1');
        if ($packed === false) {
            // 回退：直接拼接
            $packed = @inet_pton($prefixAddr . ':0:0:0:1');
        }
        if ($packed === false) {
            throw new \RuntimeException("无法解析 IPv6 前缀: {$prefix}");
        }
        return inet_ntop($packed);
    }

    /**
     * 生成纯 IPv4 的 cloud-init 配置（无 IPv6 时使用）
     */
    private function generateIpv4OnlyConfig(
        string $ipv4Addr,
        string $ipv4Mask,
        string $ipv4Gw,
        string $ipv4Dns
    ): array {
        $config = <<<YAML
network:
  version: 2
  ethernets:
    all-en:
      match:
        name: "en*"
      dhcp4: false
      dhcp6: false
      addresses:
        - {$ipv4Addr}{$ipv4Mask}
      routes:
        - to: default
          via: {$ipv4Gw}
      nameservers:
        addresses: [{$ipv4Dns}]
YAML;

        return ['success' => true, 'config' => $config, 'message' => '已生成纯 IPv4 配置'];
    }
}
