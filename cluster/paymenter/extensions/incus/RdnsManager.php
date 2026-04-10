<?php
/**
 * rDNS 反向 DNS 管理器
 *
 * 支持 Cloudflare / Route53 作为 DNS 后端，管理 IP 反向解析记录。
 * 邮件服务器等场景需要 rDNS 指向正确的 hostname。
 */

namespace Extensions\Incus;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;

class RdnsManager
{
    /** DNS 服务商类型 */
    private string $provider;

    /** API 凭证 */
    private string $apiKey;
    private string $apiSecret;

    /** Cloudflare Zone ID（仅 Cloudflare） */
    private string $zoneId;

    /** Route53 Hosted Zone ID（仅 Route53） */
    private string $hostedZoneId;

    /** API 端点 */
    private string $endpoint;

    public function __construct()
    {
        $this->provider = config('incus.rdns.provider', 'cloudflare');
        $this->apiKey = config('incus.rdns.api_key', '');
        $this->apiSecret = config('incus.rdns.api_secret', '');
        $this->zoneId = config('incus.rdns.cloudflare_zone_id', '');
        $this->hostedZoneId = config('incus.rdns.route53_hosted_zone_id', '');

        $this->endpoint = match ($this->provider) {
            'cloudflare' => 'https://api.cloudflare.com/client/v4',
            'route53' => 'https://route53.amazonaws.com/2013-04-01',
            default => throw new \InvalidArgumentException("不支持的 DNS 服务商: {$this->provider}"),
        };
    }

    // ────────────────────────────────────────────
    //  公共 CRUD 方法
    // ────────────────────────────────────────────

    /**
     * 设置反向 DNS 记录
     *
     * @param string $ip   IP 地址（IPv4 或 IPv6）
     * @param string $hostname 目标主机名（FQDN）
     * @return array ['success' => bool, 'message' => string]
     */
    public function setRdns(string $ip, string $hostname): array
    {
        // 校验 IP 格式
        if (!$this->validateIp($ip)) {
            return ['success' => false, 'message' => '无效的 IP 地址格式'];
        }

        // 校验 hostname 格式
        if (!$this->validateHostname($hostname)) {
            return ['success' => false, 'message' => '无效的主机名格式，必须为合法 FQDN'];
        }

        // 校验 A/AAAA 记录是否指向该 IP
        $forwardCheck = $this->verifyForwardRecord($ip, $hostname);
        if (!$forwardCheck['success']) {
            return $forwardCheck;
        }

        // 确认 IP 属于当前系统管理
        $ipRecord = DB::table('ip_addresses')->where('ip', $ip)->first();
        if (!$ipRecord) {
            return ['success' => false, 'message' => '该 IP 不在系统管理范围内'];
        }

        // 生成 PTR 记录名
        $ptrName = $this->ipToPtrName($ip);

        // 调用 DNS 服务商 API
        $result = $this->createOrUpdatePtrRecord($ptrName, $hostname);
        if (!$result['success']) {
            return $result;
        }

        // 更新数据库
        DB::table('ip_addresses')
            ->where('ip', $ip)
            ->update([
                'rdns_hostname' => $hostname,
                'updated_at' => now(),
            ]);

        Log::info("rDNS 设置成功: {$ip} -> {$hostname}");

        return ['success' => true, 'message' => "rDNS 已设置: {$ip} -> {$hostname}"];
    }

    /**
     * 查询当前 rDNS 记录
     *
     * @param string $ip IP 地址
     * @return array ['success' => bool, 'hostname' => string|null, 'message' => string]
     */
    public function getRdns(string $ip): array
    {
        if (!$this->validateIp($ip)) {
            return ['success' => false, 'hostname' => null, 'message' => '无效的 IP 地址格式'];
        }

        $ipRecord = DB::table('ip_addresses')->where('ip', $ip)->first();
        if (!$ipRecord) {
            return ['success' => false, 'hostname' => null, 'message' => '该 IP 不在系统管理范围内'];
        }

        return [
            'success' => true,
            'hostname' => $ipRecord->rdns_hostname,
            'message' => $ipRecord->rdns_hostname
                ? "当前 rDNS: {$ipRecord->rdns_hostname}"
                : '未设置 rDNS',
        ];
    }

    /**
     * 删除 rDNS 记录
     *
     * @param string $ip IP 地址
     * @return array ['success' => bool, 'message' => string]
     */
    public function deleteRdns(string $ip): array
    {
        if (!$this->validateIp($ip)) {
            return ['success' => false, 'message' => '无效的 IP 地址格式'];
        }

        $ipRecord = DB::table('ip_addresses')->where('ip', $ip)->first();
        if (!$ipRecord) {
            return ['success' => false, 'message' => '该 IP 不在系统管理范围内'];
        }

        if (empty($ipRecord->rdns_hostname)) {
            return ['success' => false, 'message' => '该 IP 未设置 rDNS'];
        }

        $ptrName = $this->ipToPtrName($ip);

        $result = $this->deletePtrRecord($ptrName);
        if (!$result['success']) {
            return $result;
        }

        DB::table('ip_addresses')
            ->where('ip', $ip)
            ->update([
                'rdns_hostname' => null,
                'updated_at' => now(),
            ]);

        Log::info("rDNS 已删除: {$ip}");

        return ['success' => true, 'message' => "rDNS 已删除: {$ip}"];
    }

    // ────────────────────────────────────────────
    //  校验方法
    // ────────────────────────────────────────────

    /**
     * 校验 IP 地址格式（支持 IPv4 和 IPv6）
     */
    private function validateIp(string $ip): bool
    {
        return filter_var($ip, FILTER_VALIDATE_IP) !== false;
    }

    /**
     * 校验主机名格式（FQDN）
     * 必须为合法域名，至少包含一个 dot，每段 label 合法
     */
    private function validateHostname(string $hostname): bool
    {
        // 移除末尾的点（允许 FQDN 格式 "host.example.com."）
        $hostname = rtrim($hostname, '.');

        if (strlen($hostname) > 253) {
            return false;
        }

        // 必须至少有一个 dot（FQDN 要求）
        if (strpos($hostname, '.') === false) {
            return false;
        }

        // RFC 1123: 每段 label 由字母数字和连字符组成
        $labels = explode('.', $hostname);
        foreach ($labels as $label) {
            if (empty($label) || strlen($label) > 63) {
                return false;
            }
            if (!preg_match('/^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$/', $label)) {
                return false;
            }
        }

        return true;
    }

    /**
     * 验证正向 DNS 记录是否指向该 IP
     * A 记录（IPv4）或 AAAA 记录（IPv6）必须解析到目标 IP
     */
    private function verifyForwardRecord(string $ip, string $hostname): array
    {
        $hostname = rtrim($hostname, '.');

        $isIpv6 = filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6);
        $recordType = $isIpv6 ? DNS_AAAA : DNS_A;
        $typeName = $isIpv6 ? 'AAAA' : 'A';

        $records = @dns_get_record($hostname, $recordType);

        if ($records === false || empty($records)) {
            return [
                'success' => false,
                'message' => "{$hostname} 没有 {$typeName} 记录，请先添加指向 {$ip} 的 {$typeName} 记录",
            ];
        }

        $field = $isIpv6 ? 'ipv6' : 'ip';
        $resolvedIps = array_column($records, $field);

        // IPv6 地址比较需要标准化
        if ($isIpv6) {
            $normalizedTarget = inet_ntop(inet_pton($ip));
            $found = false;
            foreach ($resolvedIps as $resolved) {
                if (inet_ntop(inet_pton($resolved)) === $normalizedTarget) {
                    $found = true;
                    break;
                }
            }
        } else {
            $found = in_array($ip, $resolvedIps, true);
        }

        if (!$found) {
            return [
                'success' => false,
                'message' => "{$hostname} 的 {$typeName} 记录未指向 {$ip}，当前解析到: " . implode(', ', $resolvedIps),
            ];
        }

        return ['success' => true, 'message' => '正向记录验证通过'];
    }

    // ────────────────────────────────────────────
    //  PTR 记录操作
    // ────────────────────────────────────────────

    /**
     * 将 IP 转为 PTR 记录名（in-addr.arpa / ip6.arpa 格式）
     */
    private function ipToPtrName(string $ip): string
    {
        if (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6)) {
            // IPv6: 展开为完整 32 hex nibbles，反转后加 ip6.arpa
            $expanded = bin2hex(inet_pton($ip));
            $nibbles = str_split($expanded);
            return implode('.', array_reverse($nibbles)) . '.ip6.arpa';
        }

        // IPv4: 反转四段加 in-addr.arpa
        $octets = explode('.', $ip);
        return implode('.', array_reverse($octets)) . '.in-addr.arpa';
    }

    /**
     * 创建或更新 PTR 记录
     */
    private function createOrUpdatePtrRecord(string $ptrName, string $hostname): array
    {
        return match ($this->provider) {
            'cloudflare' => $this->cloudflareUpsertPtr($ptrName, $hostname),
            'route53' => $this->route53UpsertPtr($ptrName, $hostname),
            default => ['success' => false, 'message' => "不支持的服务商: {$this->provider}"],
        };
    }

    /**
     * 删除 PTR 记录
     */
    private function deletePtrRecord(string $ptrName): array
    {
        return match ($this->provider) {
            'cloudflare' => $this->cloudflareDeletePtr($ptrName),
            'route53' => $this->route53DeletePtr($ptrName),
            default => ['success' => false, 'message' => "不支持的服务商: {$this->provider}"],
        };
    }

    // ────────────────────────────────────────────
    //  Cloudflare 实现
    // ────────────────────────────────────────────

    private function cloudflareUpsertPtr(string $ptrName, string $hostname): array
    {
        // 查找已有 PTR 记录
        $existing = $this->cloudflareRequest('GET', "/zones/{$this->zoneId}/dns_records", [
            'type' => 'PTR',
            'name' => $ptrName,
        ]);

        if (!$existing['success']) {
            return $existing;
        }

        $data = [
            'type' => 'PTR',
            'name' => $ptrName,
            'content' => rtrim($hostname, '.') . '.',
            'ttl' => 3600,
        ];

        if (!empty($existing['result'])) {
            // 更新已有记录
            $recordId = $existing['result'][0]['id'];
            return $this->cloudflareRequest('PUT', "/zones/{$this->zoneId}/dns_records/{$recordId}", $data);
        }

        // 创建新记录
        return $this->cloudflareRequest('POST', "/zones/{$this->zoneId}/dns_records", $data);
    }

    private function cloudflareDeletePtr(string $ptrName): array
    {
        $existing = $this->cloudflareRequest('GET', "/zones/{$this->zoneId}/dns_records", [
            'type' => 'PTR',
            'name' => $ptrName,
        ]);

        if (!$existing['success']) {
            return $existing;
        }

        if (empty($existing['result'])) {
            return ['success' => true, 'message' => 'PTR 记录不存在，无需删除'];
        }

        $recordId = $existing['result'][0]['id'];
        return $this->cloudflareRequest('DELETE', "/zones/{$this->zoneId}/dns_records/{$recordId}");
    }

    private function cloudflareRequest(string $method, string $path, array $data = []): array
    {
        $url = $this->endpoint . $path;

        $ch = curl_init();
        curl_setopt_array($ch, [
            CURLOPT_URL => $method === 'GET' && !empty($data) ? $url . '?' . http_build_query($data) : $url,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => [
                'Authorization: Bearer ' . $this->apiKey,
                'Content-Type: application/json',
            ],
            CURLOPT_TIMEOUT => 30,
        ]);

        if ($method !== 'GET' && !empty($data)) {
            curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($data));
        }

        $response = curl_exec($ch);
        $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $error = curl_error($ch);
        curl_close($ch);

        if ($error) {
            Log::error("Cloudflare API 请求失败: {$error}");
            return ['success' => false, 'message' => "DNS API 请求失败: {$error}"];
        }

        $result = json_decode($response, true);

        if ($httpCode >= 400 || (isset($result['success']) && !$result['success'])) {
            $errorMsg = $result['errors'][0]['message'] ?? "HTTP {$httpCode}";
            Log::error("Cloudflare API 错误: {$errorMsg}");
            return ['success' => false, 'message' => "DNS API 错误: {$errorMsg}"];
        }

        return ['success' => true, 'result' => $result['result'] ?? [], 'message' => '操作成功'];
    }

    // ────────────────────────────────────────────
    //  Route53 实现
    // ────────────────────────────────────────────

    private function route53UpsertPtr(string $ptrName, string $hostname): array
    {
        $xml = $this->route53ChangeBatchXml('UPSERT', $ptrName, rtrim($hostname, '.') . '.', 3600);
        return $this->route53Request($xml);
    }

    private function route53DeletePtr(string $ptrName): array
    {
        // 先查询当前值
        $current = $this->route53QueryPtr($ptrName);
        if (!$current['success'] || empty($current['value'])) {
            return ['success' => true, 'message' => 'PTR 记录不存在，无需删除'];
        }

        $xml = $this->route53ChangeBatchXml('DELETE', $ptrName, $current['value'], $current['ttl']);
        return $this->route53Request($xml);
    }

    private function route53QueryPtr(string $ptrName): array
    {
        $url = "{$this->endpoint}/hostedzone/{$this->hostedZoneId}/rrset"
            . '?' . http_build_query(['name' => $ptrName, 'type' => 'PTR', 'maxitems' => '1']);

        $ch = curl_init();
        curl_setopt_array($ch, [
            CURLOPT_URL => $url,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_HTTPHEADER => $this->route53Headers('GET', $url),
            CURLOPT_TIMEOUT => 30,
        ]);

        $response = curl_exec($ch);
        $error = curl_error($ch);
        curl_close($ch);

        if ($error) {
            return ['success' => false, 'value' => null, 'ttl' => 3600];
        }

        $xml = @simplexml_load_string($response);
        if ($xml === false || !isset($xml->ResourceRecordSets->ResourceRecordSet)) {
            return ['success' => true, 'value' => null, 'ttl' => 3600];
        }

        $rrset = $xml->ResourceRecordSets->ResourceRecordSet;
        if ((string) $rrset->Name !== $ptrName . '.' && (string) $rrset->Name !== $ptrName) {
            return ['success' => true, 'value' => null, 'ttl' => 3600];
        }

        return [
            'success' => true,
            'value' => (string) $rrset->ResourceRecords->ResourceRecord->Value,
            'ttl' => (int) $rrset->TTL,
        ];
    }

    private function route53ChangeBatchXml(string $action, string $name, string $value, int $ttl): string
    {
        // 白名单校验 action，防御性编程防止 XML 注入
        if (!in_array($action, ['CREATE', 'DELETE', 'UPSERT'], true)) {
            throw new \InvalidArgumentException("非法的 Route53 action: {$action}");
        }

        $name = htmlspecialchars($name, ENT_XML1, 'UTF-8');
        $value = htmlspecialchars($value, ENT_XML1, 'UTF-8');

        return <<<XML
<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeBatch>
    <Changes>
      <Change>
        <Action>{$action}</Action>
        <ResourceRecordSet>
          <Name>{$name}</Name>
          <Type>PTR</Type>
          <TTL>{$ttl}</TTL>
          <ResourceRecords>
            <ResourceRecord>
              <Value>{$value}</Value>
            </ResourceRecord>
          </ResourceRecords>
        </ResourceRecordSet>
      </Change>
    </Changes>
  </ChangeBatch>
</ChangeResourceRecordSetsRequest>
XML;
    }

    private function route53Request(string $xmlBody): array
    {
        $url = "{$this->endpoint}/hostedzone/{$this->hostedZoneId}/rrset";

        $ch = curl_init();
        curl_setopt_array($ch, [
            CURLOPT_URL => $url,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CUSTOMREQUEST => 'POST',
            CURLOPT_POSTFIELDS => $xmlBody,
            CURLOPT_HTTPHEADER => array_merge(
                $this->route53Headers('POST', $url, $xmlBody),
                ['Content-Type: text/xml']
            ),
            CURLOPT_TIMEOUT => 30,
        ]);

        $response = curl_exec($ch);
        $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $error = curl_error($ch);
        curl_close($ch);

        if ($error) {
            Log::error("Route53 API 请求失败: {$error}");
            return ['success' => false, 'message' => "DNS API 请求失败: {$error}"];
        }

        if ($httpCode >= 400) {
            Log::error("Route53 API 错误: HTTP {$httpCode}, {$response}");
            return ['success' => false, 'message' => "DNS API 错误: HTTP {$httpCode}"];
        }

        return ['success' => true, 'message' => '操作成功'];
    }

    /**
     * Route53 AWS Signature V4 请求头
     * 简化实现 — 生产环境建议使用 AWS SDK
     *
     * @param string $body 请求体（POST 时传入 XML，GET 时为空）
     */
    private function route53Headers(string $method, string $url, string $body = ''): array
    {
        $date = gmdate('Ymd\THis\Z');
        $shortDate = gmdate('Ymd');
        $region = config('incus.rdns.route53_region', 'us-east-1');
        $service = 'route53';

        // AWS Signature V4 签名
        $host = parse_url($url, PHP_URL_HOST);
        $path = parse_url($url, PHP_URL_PATH) ?: '/';
        $queryString = parse_url($url, PHP_URL_QUERY) ?: '';

        // 规范化 query string：按参数名排序（SigV4 要求）
        $canonicalQueryString = '';
        if (!empty($queryString)) {
            parse_str($queryString, $params);
            ksort($params);
            $canonicalQueryString = http_build_query($params, '', '&', PHP_QUERY_RFC3986);
        }

        $canonicalRequest = implode("\n", [
            $method, $path, $canonicalQueryString,
            "host:{$host}", "x-amz-date:{$date}", '',
            'host;x-amz-date', hash('sha256', $body),
        ]);

        $credentialScope = "{$shortDate}/{$region}/{$service}/aws4_request";
        $stringToSign = implode("\n", [
            'AWS4-HMAC-SHA256', $date, $credentialScope,
            hash('sha256', $canonicalRequest),
        ]);

        $signingKey = hash_hmac('sha256', 'aws4_request',
            hash_hmac('sha256', $service,
                hash_hmac('sha256', $region,
                    hash_hmac('sha256', $shortDate, 'AWS4' . $this->apiSecret, true),
                    true),
                true),
            true);

        $signature = hash_hmac('sha256', $stringToSign, $signingKey);
        $authHeader = "AWS4-HMAC-SHA256 Credential={$this->apiKey}/{$credentialScope}, "
            . "SignedHeaders=host;x-amz-date, Signature={$signature}";

        return [
            "Host: {$host}",
            "X-Amz-Date: {$date}",
            "Authorization: {$authHeader}",
        ];
    }
}
