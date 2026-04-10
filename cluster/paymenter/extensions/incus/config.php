<?php

return [
    // Incus API 端点（通过 WireGuard 隧道连接）
    'endpoint' => env('INCUS_API_ENDPOINT', 'https://10.0.10.1:8443'),

    // mTLS 证书路径（受限证书，仅 customers project 权限）
    'cert_file' => env('INCUS_CERT_FILE', '/var/www/html/certs/client.crt'),
    'key_file' => env('INCUS_KEY_FILE', '/var/www/html/certs/client.key'),

    // Incus Project 名称
    'project' => env('INCUS_PROJECT', 'customers'),

    // 请求超时（秒）
    'timeout' => (int) env('INCUS_TIMEOUT', 30),

    // 失败重试次数
    'max_retries' => (int) env('INCUS_MAX_RETRIES', 3),

    // 默认带宽限速
    'default_bandwidth' => [
        'ingress' => env('INCUS_DEFAULT_BW_INGRESS', '200Mbit'),
        'egress' => env('INCUS_DEFAULT_BW_EGRESS', '200Mbit'),
    ],

    // 超额限速（流量超额后降速）
    'throttle_bandwidth' => [
        'ingress' => env('INCUS_THROTTLE_BW_INGRESS', '10Mbit'),
        'egress' => env('INCUS_THROTTLE_BW_EGRESS', '10Mbit'),
    ],

    // 默认 ACL 规则（创建 VM 时自动应用）
    'default_acl' => [
        'ingress' => [
            [
                'action' => 'allow',
                'protocol' => 'tcp',
                'destination_port' => '22',
                'source' => '0.0.0.0/0',
                'description' => 'SSH',
            ],
        ],
        'egress' => [],
        'default_ingress_action' => 'drop',
        'default_egress_action' => 'allow',
    ],

    // IP 冷却期（小时）
    'ip_cooldown_hours' => (int) env('INCUS_IP_COOLDOWN_HOURS', 24),

    // 快照限制
    'max_snapshots_per_vm' => (int) env('INCUS_MAX_SNAPSHOTS', 5),

    // 存储池名称
    'storage_pool' => env('INCUS_STORAGE_POOL', 'ceph-pool'),
];
