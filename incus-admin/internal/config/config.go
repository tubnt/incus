package config

import (
	"fmt"
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
			AuditRetentionDays:  parseIntOr("AUDIT_RETENTION_DAYS", 365),
			ShadowSessionSecret: envOr("SHADOW_SESSION_SECRET", ""),
		},
		Billing: BillingConfig{
			Currency: envOr("BILLING_CURRENCY", "USD"),
		},
		Monitor: MonitorConfig{
			PrometheusURL:     envOr("PROMETHEUS_URL", ""),
			GrafanaURL:        envOr("GRAFANA_URL", ""),
			CephSSHHost:       envOr("CEPH_SSH_HOST", ""),
			CephSSHUser:       envOr("CEPH_SSH_USER", "root"),
			CephSSHKey:        envOr("CEPH_SSH_KEY", "/etc/incus-admin/certs/ssh_key"),
			SSHKnownHostsFile: envOr("SSH_KNOWN_HOSTS_FILE", ""),
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
			IPPools: func() []IPPoolConfig {
				if r := envOr("CLUSTER_IP_RANGE", ""); r != "" {
					return []IPPoolConfig{{
						CIDR:    envOr("CLUSTER_IP_CIDR", "202.151.179.224/27"),
						Gateway: envOr("CLUSTER_IP_GATEWAY", "202.151.179.225"),
						Range:   r,
						VLAN:    376,
					}}
				}
				return nil
			}(),
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
