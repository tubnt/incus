package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server   ServerConfig    `json:"server"`
	Database DatabaseConfig  `json:"database"`
	Auth     AuthConfig      `json:"auth"`
	Clusters []ClusterConfig `json:"clusters"`
	Billing  BillingConfig   `json:"billing"`
	Monitor  MonitorConfig   `json:"monitor"`
	AI       AIConfig        `json:"ai"`
	Jobs     JobsConfig      `json:"jobs"`
	// OPS-051 / PLAN-052：cloud-init 注入的 apt-cacher-ng proxy + 模板默认登录用户
	// （root），便于 dev 环境留空跳过 proxy。
	Provisioning ProvisioningConfig `json:"provisioning"`
}

// ProvisioningConfig 控制 vm-create 生成 cloud-init user-data 的横向参数。
//
//   - AptProxyURL：apt-cacher-ng / squid 等 HTTP cache 的全地址，例如
//     http://139.162.24.177:3142/。留空 → 不注入 proxy（VM 直连上游）。
//     cloud-init runcmd 有 fallback：proxy 不可达 5 秒内自动剥离。
//   - DefaultLoginUser：Linux VM 强制建立的统一登录账号；默认 root（OPS-051
//     Q7 决策）。镜像自带的 ubuntu/debian/rocky 用户保留但不再当默认。
type ProvisioningConfig struct {
	AptProxyURL      string `json:"apt_proxy_url"`     // env APT_CACHER_URL
	DefaultLoginUser string `json:"default_login_user"` // env VM_DEFAULT_LOGIN_USER，默认 root
}

// JobsConfig 控制 PLAN-025 异步 provisioning runtime 的容量。
//
// PoolSize 是 worker goroutine 池容量；QueueSize 是入队 channel 的 buffer
// 长度。促销 / 批量场景下默认 4 / 64 不够用——并发 64+ 用户下单时
// Enqueue 会阻塞 HTTP（PayWithBalance 已 commit、余额已扣的状态下挂死），
// 生产应通过 env 显式调高。
//
// 建议：
//   - 默认 4 / 64：单机小流量，向后兼容。
//   - 生产 8 / 256：促销 / 批量。
//   - 不建议 > 16：单 Incus host 镜像并发拉取超过 16 会撞网络/磁盘瓶颈。
type JobsConfig struct {
	PoolSize  int `json:"pool_size"`  // env JOBS_POOL_SIZE，默认 4
	QueueSize int `json:"queue_size"` // env JOBS_QUEUE_SIZE，默认 64
}

// AIConfig 控制 PLAN-038 / OPS-041 Tier 2 (LLM 角色推荐) + Tier 3 (LLM 失败诊断)。
//
// Provider 留空或 "disabled" → 整条 LLM 路径短路（Phase A Tier 1 仍工作），
// 部署没买 API key 也能正常跑。
type AIConfig struct {
	Provider          string `json:"provider"`           // "anthropic" | "openai" | "disabled"
	APIKey            string `json:"-"`                  // env-only；不序列化避免日志泄漏
	Model             string `json:"model"`              // 默认 "claude-haiku-4-5"
	BaseURL           string `json:"base_url"`           // 自托管 OpenAI 兼容用
	RequestTimeoutSec int    `json:"request_timeout_sec"`
	MonthlyTokenBudget int64 `json:"monthly_token_budget"` // 0 = 不限
}

type ServerConfig struct {
	Listen          string        `json:"listen"`
	EmergencyListen string        `json:"emergency_listen"`
	Domain          string        `json:"domain"`
	SessionSecret   string        `json:"session_secret"`
	SessionTTL      time.Duration `json:"session_ttl"`
	// Env controls destructive operations that should never fire in
	// production — chaos drill is the canonical example. Defaults to
	// "production" so the safe-by-default path requires an explicit override
	// on staging/dev deploys.
	Env string `json:"env"`
}

type DatabaseConfig struct {
	DSN             string `json:"dsn"`
	MaxOpenConns    int    `json:"max_open_conns"`
	MaxIdleConns    int    `json:"max_idle_conns"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`
}

type AuthConfig struct {
	AdminEmails    []string `json:"admin_emails"`
	EmergencyToken string   `json:"emergency_token"`

	// Step-up OIDC. Optional: when any of the first four is empty the step-up
	// subsystem stays disabled and sensitive-route middleware falls back to a
	// permissive mode (logged warning only). In production all four must be set.
	OIDCIssuer        string        `json:"oidc_issuer"`
	OIDCClientID      string        `json:"oidc_client_id"`
	OIDCClientSecret  string        `json:"oidc_client_secret"`
	StepUpCallbackURL string        `json:"stepup_callback_url"`
	StepUpStateSecret string        `json:"stepup_state_secret"`
	StepUpMaxAge      time.Duration `json:"stepup_max_age"`

	// AuditRetentionDays governs how long audit_logs rows are kept before the
	// cleanup worker deletes them. <= 0 disables cleanup entirely (test envs).
	AuditRetentionDays int `json:"audit_retention_days"`

	// ShadowSessionSecret signs shadow_session cookies (HMAC-SHA256). Falls
	// back to Server.SessionSecret when unset to simplify single-node deploys.
	ShadowSessionSecret string `json:"shadow_session_secret"`

	// PasswordEncryptionKey enables OPS-022 vms.password 字段 AES-256-GCM 加密。
	// 32 字节 base64 编码（生成方式：openssl rand -base64 32）。
	// 空值 → 加密 disabled，passthrough 模式（向后兼容老部署）。
	PasswordEncryptionKey string `json:"password_encryption_key"`
}

type ClusterConfig struct {
	Name           string          `json:"name"`
	DisplayName    string          `json:"display_name"`
	APIURL         string          `json:"api_url"`
	CertFile       string          `json:"cert_file"`
	KeyFile        string          `json:"key_file"`
	CAFile         string          `json:"ca_file"`
	StoragePool    string          `json:"storage_pool"`
	Network        string          `json:"network"`
	DefaultProject string          `json:"default_project"`
	Projects       []ProjectConfig `json:"projects"`
	IPPools        []IPPoolConfig  `json:"ip_pools"`
}

type ProjectConfig struct {
	Name        string `json:"name"`
	Access      string `json:"access"`
	Description string `json:"description"`
}

type IPPoolConfig struct {
	CIDR    string `json:"cidr"`
	Gateway string `json:"gateway"`
	Range   string `json:"range"`
	VLAN    int    `json:"vlan"`
}

type BillingConfig struct {
	StripeKey string `json:"stripe_key"`
	Currency  string `json:"currency"`
	// PLAN-054 / INFRA-013 计费引擎 toggle + 周期参数。
	// Enabled=false 时启动跳过 charger / grace_expire worker（topup hook 也跳）。
	// 默认 true：生产 + 测试都开。
	Enabled bool `json:"enabled"`
	// ChargerInterval 计费 worker 扫描周期。原始设计是 cron 0 0 * * *（每日
	// 00:00 UTC）；catchup-safe 让我们退化为 hourly tick。默认 1h。
	ChargerInterval time.Duration `json:"charger_interval"`
	// GraceInterval suspended grace 过期 trash 周期。默认 1h，比 charger 错开
	// 15min 跑避免同时锁 users.balance。
	GraceInterval time.Duration `json:"grace_interval"`
	// GraceDuration 余额不足 → suspended 后多久 trash。PLAN-054 §2 默认 72h。
	GraceDuration time.Duration `json:"grace_duration"`
}

type MonitorConfig struct {
	PrometheusURL     string `json:"prometheus_url"`
	GrafanaURL        string `json:"grafana_url"`
	CephSSHHost       string `json:"ceph_ssh_host"`
	CephSSHUser       string `json:"ceph_ssh_user"`
	CephSSHKey        string `json:"ceph_ssh_key"`
	SSHKnownHostsFile string `json:"ssh_known_hosts_file"`
}

func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Listen:          envOr("LISTEN", ":8080"),
			EmergencyListen: envOr("EMERGENCY_LISTEN", "127.0.0.1:8081"),
			Domain:          envOr("DOMAIN", "vmc.5ok.co"),
			SessionSecret:   mustEnv("SESSION_SECRET"),
			SessionTTL:      24 * time.Hour,
			Env:             envOr("INCUS_ADMIN_ENV", "production"),
		},
		Database: DatabaseConfig{
			DSN:             mustEnv("DATABASE_URL"),
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: time.Hour,
		},
		Auth: AuthConfig{
			AdminEmails:       strings.Split(envOr("ADMIN_EMAILS", ""), ","),
			EmergencyToken:    mustEnv("EMERGENCY_TOKEN"),
			OIDCIssuer:        envOr("OIDC_ISSUER", ""),
			OIDCClientID:      envOr("OIDC_CLIENT_ID", ""),
			OIDCClientSecret:  envOr("OIDC_CLIENT_SECRET", ""),
			StepUpCallbackURL: envOr("STEPUP_CALLBACK_URL", ""),
			StepUpStateSecret: envOr("STEPUP_STATE_SECRET", ""),
			StepUpMaxAge:       parseDurationOr("STEPUP_MAX_AGE", 5*time.Minute),
			AuditRetentionDays:    parseIntOr("AUDIT_RETENTION_DAYS", 365),
			ShadowSessionSecret:   envOr("SHADOW_SESSION_SECRET", ""),
			PasswordEncryptionKey: envOr("PASSWORD_ENCRYPTION_KEY", ""),
		},
		Billing: BillingConfig{
			Currency:        envOr("BILLING_CURRENCY", "USD"),
			Enabled:         parseBoolOr("INCUS_ADMIN_BILLING_ENABLED", true),
			ChargerInterval: parseDurationOr("INCUS_ADMIN_BILLING_CHARGER_INTERVAL", time.Hour),
			GraceInterval:   parseDurationOr("INCUS_ADMIN_BILLING_GRACE_INTERVAL", time.Hour),
			GraceDuration:   parseDurationOr("INCUS_ADMIN_BILLING_GRACE_DURATION", 72*time.Hour),
		},
		Monitor: MonitorConfig{
			PrometheusURL:     envOr("PROMETHEUS_URL", ""),
			GrafanaURL:        envOr("GRAFANA_URL", ""),
			CephSSHHost:       envOr("CEPH_SSH_HOST", ""),
			CephSSHUser:       envOr("CEPH_SSH_USER", "root"),
			CephSSHKey:        envOr("CEPH_SSH_KEY", "/etc/incus-admin/certs/ssh_key"),
			SSHKnownHostsFile: envOr("SSH_KNOWN_HOSTS_FILE", ""),
		},
		AI: AIConfig{
			Provider:           strings.ToLower(envOr("AI_PROVIDER", "disabled")),
			APIKey:             os.Getenv("AI_API_KEY"),
			Model:              envOr("AI_MODEL", "claude-haiku-4-5"),
			BaseURL:            envOr("AI_BASE_URL", ""),
			RequestTimeoutSec:  parseIntOr("AI_REQUEST_TIMEOUT_SEC", 30),
			MonthlyTokenBudget: int64(parseIntOr("AI_MONTHLY_TOKEN_BUDGET", 0)),
		},
		Jobs: JobsConfig{
			PoolSize:  parseIntOr("JOBS_POOL_SIZE", 4),
			QueueSize: parseIntOr("JOBS_QUEUE_SIZE", 64),
		},
		Provisioning: ProvisioningConfig{
			AptProxyURL:      envOr("APT_CACHER_URL", ""),
			DefaultLoginUser: envOr("VM_DEFAULT_LOGIN_USER", "root"),
		},
	}

	if cfg.Auth.AdminEmails[0] == "" {
		cfg.Auth.AdminEmails = nil
	}

	if clusterURL := envOr("CLUSTER_API_URL", ""); clusterURL != "" {
		cfg.Clusters = append(cfg.Clusters, ClusterConfig{
			Name:           envOr("CLUSTER_NAME", "default"),
			DisplayName:    envOr("CLUSTER_DISPLAY_NAME", "Default Cluster"),
			APIURL:         clusterURL,
			CertFile:       envOr("CLUSTER_CERT_FILE", "/etc/incus-admin/certs/client.crt"),
			KeyFile:        envOr("CLUSTER_KEY_FILE", "/etc/incus-admin/certs/client.key"),
			CAFile:         envOr("CLUSTER_CA_FILE", ""),
			StoragePool:    envOr("CLUSTER_STORAGE_POOL", "ceph-pool"),
			Network:        envOr("CLUSTER_NETWORK", "br-pub"),
			DefaultProject: envOr("CLUSTER_DEFAULT_PROJECT", "customers"),
			Projects: []ProjectConfig{
				{Name: "default", Access: "internal", Description: "Default project"},
				{Name: "customers", Access: "public", Description: "Customer VMs"},
			},
			IPPools: loadIPPools(),
		})
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid int for %s=%q: %v; using default %d\n", key, v, err, fallback)
		return fallback
	}
	return n
}

// parseBoolOr 解析布尔型 env。空 → fallback。识别 1/true/yes/on（不分大小写）
// 为 true，0/false/no/off 为 false；其它 → fallback + stderr warn。
func parseBoolOr(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		fmt.Fprintf(os.Stderr, "invalid bool for %s=%q; using default %v\n", key, v, fallback)
		return fallback
	}
}

func parseDurationOr(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid duration for %s=%q: %v; using default %s\n", key, v, err, fallback)
		return fallback
	}
	return d
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required env var %s is not set\n", key)
		os.Exit(1)
	}
	return v
}

// loadIPPools supports two shapes:
//
//  1. CLUSTER_IP_POOLS_JSON — JSON array of {cidr,gateway,range,vlan};
//     preferred for multi-segment deploys (PLAN-021 Phase F).
//  2. Legacy single-pool CLUSTER_IP_CIDR/GATEWAY/RANGE env vars — kept so
//     existing single-segment deploys keep working without env-file churn.
//
// Returns nil when nothing is configured so tests/dev builds can boot without
// a public pool.
func loadIPPools() []IPPoolConfig {
	if raw := envOr("CLUSTER_IP_POOLS_JSON", ""); raw != "" {
		var pools []IPPoolConfig
		if err := json.Unmarshal([]byte(raw), &pools); err != nil {
			slog.Warn("CLUSTER_IP_POOLS_JSON parse failed, ignoring", "error", err)
		} else if len(pools) > 0 {
			return pools
		}
	}
	if r := envOr("CLUSTER_IP_RANGE", ""); r != "" {
		return []IPPoolConfig{{
			CIDR:    envOr("CLUSTER_IP_CIDR", "202.151.179.224/27"),
			Gateway: envOr("CLUSTER_IP_GATEWAY", "202.151.179.225"),
			Range:   r,
			VLAN:    376,
		}}
	}
	return nil
}
