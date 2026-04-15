package config

import (
	"fmt"
	"os"
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
	Listen         string        `json:"listen"`
	EmergencyListen string       `json:"emergency_listen"`
	Domain         string        `json:"domain"`
	SessionSecret  string        `json:"session_secret"`
	SessionTTL     time.Duration `json:"session_ttl"`
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
	PrometheusURL string `json:"prometheus_url"`
	GrafanaURL    string `json:"grafana_url"`
}

func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Listen:          envOr("LISTEN", ":8080"),
			EmergencyListen: envOr("EMERGENCY_LISTEN", "127.0.0.1:8081"),
			Domain:          envOr("DOMAIN", "vmc.5ok.co"),
			SessionSecret:   mustEnv("SESSION_SECRET"),
			SessionTTL:      24 * time.Hour,
		},
		Database: DatabaseConfig{
			DSN:             mustEnv("DATABASE_URL"),
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: time.Hour,
		},
		Auth: AuthConfig{
			AdminEmails:    strings.Split(envOr("ADMIN_EMAILS", ""), ","),
			EmergencyToken: mustEnv("EMERGENCY_TOKEN"),
		},
		Billing: BillingConfig{
			Currency: envOr("BILLING_CURRENCY", "USD"),
		},
		Monitor: MonitorConfig{
			PrometheusURL: envOr("PROMETHEUS_URL", ""),
			GrafanaURL:    envOr("GRAFANA_URL", ""),
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

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required env var %s is not set\n", key)
		os.Exit(1)
	}
	return v
}
