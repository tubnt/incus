# Incus Metrics mTLS 证书

Prometheus 采集 Incus `/1.0/metrics` 需要 mTLS 客户端证书。

## 所需文件

将以下两个文件放入本目录：

- `metrics.crt` — 客户端证书
- `metrics.key` — 客户端私钥

## 生成方法

在 Incus 集群任意节点上执行：

```bash
# 创建 metrics 专用证书
incus config trust add metrics --type=metrics

# 或手动生成并添加
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 \
  -sha384 -keyout metrics.key -nodes -out metrics.crt \
  -days 3650 -subj "/CN=metrics"

incus config trust add-certificate metrics.crt --type=metrics
```

将生成的 `metrics.crt` 和 `metrics.key` 复制到本目录即可。
