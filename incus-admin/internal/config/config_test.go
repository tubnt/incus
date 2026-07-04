package config

import (
	"testing"
)

func TestLoadIPPools_JSON(t *testing.T) {
	t.Setenv("CLUSTER_IP_POOLS_JSON", `[
		{"cidr":"202.151.179.0/26","gateway":"202.151.179.62","range":"202.151.179.10-202.151.179.61","vlan":376},
		{"cidr":"202.151.179.224/27","gateway":"202.151.179.225","range":"202.151.179.235-202.151.179.254","vlan":376}
	]`)
	// Legacy single-pool env present — JSON must still win.
	t.Setenv("CLUSTER_IP_RANGE", "legacy-should-be-ignored")

	got := loadIPPools()
	if len(got) != 2 {
		t.Fatalf("want 2 pools, got %d: %+v", len(got), got)
	}
	if got[0].CIDR != "202.151.179.0/26" || got[0].VLAN != 376 {
		t.Errorf("first pool mismatch: %+v", got[0])
	}
	if got[1].CIDR != "202.151.179.224/27" || got[1].Range != "202.151.179.235-202.151.179.254" {
		t.Errorf("second pool mismatch: %+v", got[1])
	}
}

func TestLoadIPPools_LegacySinglePool(t *testing.T) {
	t.Setenv("CLUSTER_IP_POOLS_JSON", "")
	t.Setenv("CLUSTER_IP_CIDR", "10.0.0.0/24")
	t.Setenv("CLUSTER_IP_GATEWAY", "10.0.0.1")
	t.Setenv("CLUSTER_IP_RANGE", "10.0.0.10-10.0.0.250")

	got := loadIPPools()
	if len(got) != 1 {
		t.Fatalf("want 1 pool, got %d", len(got))
	}
	if got[0].CIDR != "10.0.0.0/24" || got[0].Gateway != "10.0.0.1" || got[0].VLAN != 376 {
		t.Errorf("legacy pool mismatch: %+v", got[0])
	}
}

func TestLoadIPPools_Empty(t *testing.T) {
	t.Setenv("CLUSTER_IP_POOLS_JSON", "")
	t.Setenv("CLUSTER_IP_RANGE", "")

	if got := loadIPPools(); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestLoadIPPools_BadJSONFallsThrough(t *testing.T) {
	t.Setenv("CLUSTER_IP_POOLS_JSON", "not-valid-json")
	t.Setenv("CLUSTER_IP_CIDR", "10.0.0.0/24")
	t.Setenv("CLUSTER_IP_GATEWAY", "10.0.0.1")
	t.Setenv("CLUSTER_IP_RANGE", "10.0.0.10-10.0.0.250")

	got := loadIPPools()
	if len(got) != 1 {
		t.Fatalf("want legacy pool after bad JSON, got %d pools", len(got))
	}
	if got[0].CIDR != "10.0.0.0/24" {
		t.Errorf("legacy fallback mismatch: %+v", got[0])
	}
}

// TestLoad_ProxySharedSecret 守门决策#6：PROXY_SHARED_SECRET 未设置时默认空
// （关闭代理签名加固，保持现网默认行为）；设置时原样载入。
func TestLoad_ProxySharedSecret(t *testing.T) {
	// Load() 依赖三个 mustEnv；测试里补齐避免 os.Exit。
	t.Setenv("SESSION_SECRET", "test-session-secret")
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("EMERGENCY_TOKEN", "test-emergency-token")

	// 未设置 → 默认空 = 关闭。
	t.Setenv("PROXY_SHARED_SECRET", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.ProxySharedSecret != "" {
		t.Errorf("PROXY_SHARED_SECRET 默认应为空（加固关闭），got %q", cfg.Server.ProxySharedSecret)
	}

	// 设置 → 原样载入。
	t.Setenv("PROXY_SHARED_SECRET", "abc123")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.ProxySharedSecret != "abc123" {
		t.Errorf("PROXY_SHARED_SECRET 应原样载入，got %q", cfg.Server.ProxySharedSecret)
	}
}
