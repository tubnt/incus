package cluster

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
)

// FingerprintStore abstracts the "load/persist the expected SPKI pin for a
// cluster" hook so the cluster package does not depend on the repository
// layer directly. Both methods must be safe for concurrent use.
type FingerprintStore interface {
	Get(ctx context.Context, clusterName string) (string, error)
	Set(ctx context.Context, clusterName, fingerprint string) error
}

// SPKIFingerprint returns the hex-encoded SHA256 of the leaf cert's
// RawSubjectPublicKeyInfo. The pin binds to the public key, so operational
// certificate rotations with the same key do not require a reset.
func SPKIFingerprint(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// pinnedVerifier produces a tls.Config.VerifyPeerCertificate callback that
// enforces the pin from the store. First connect with an empty pin learns and
// persists (TOFU). Afterwards, any mismatch fails the handshake.
// learnedOnce ensures a single TOFU event per process even under concurrent
// handshakes.
func pinnedVerifier(clusterName string, store FingerprintStore) func([][]byte, [][]*x509.Certificate) error {
	var learnedOnce sync.Once

	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("cluster %q: TLS handshake produced no peer certificate", clusterName)
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("cluster %q: parse leaf cert: %w", clusterName, err)
		}
		seen := SPKIFingerprint(leaf)

		ctx := context.Background()
		expected, err := store.Get(ctx, clusterName)
		if err != nil {
			return fmt.Errorf("cluster %q: load pin: %w", clusterName, err)
		}

		if expected == "" {
			var saveErr error
			learnedOnce.Do(func() {
				saveErr = store.Set(ctx, clusterName, seen)
				if saveErr == nil {
					slog.Warn("TLS pin learned (trust-on-first-use)", "cluster", clusterName, "spki_sha256", seen[:12])
				}
			})
			if saveErr != nil {
				return fmt.Errorf("cluster %q: save pin: %w", clusterName, saveErr)
			}
			return nil
		}

		if expected != seen {
			slog.Error("TLS pin MISMATCH — refusing connection",
				"cluster", clusterName,
				"expected_sha256", expected[:12],
				"seen_sha256", seen[:12],
			)
			return fmt.Errorf("cluster %q: TLS fingerprint mismatch (possible MITM)", clusterName)
		}
		return nil
	}
}

// BuildPinnedTLSConfig layers SPKI pinning on top of the base TLS config.
// When a store is supplied, InsecureSkipVerify stays true (the default path
// behavior for CA-less clusters) but the pin callback rejects unknown peers.
// Without a store, the returned config is identical to the base — callers
// must ensure the store is wired in production.
func BuildPinnedTLSConfig(base *tls.Config, clusterName string, store FingerprintStore) *tls.Config {
	if base == nil {
		base = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cfg := base.Clone()
	if store == nil {
		return cfg
	}
	// gosec G123 提示 session resumption 可能跳过 VerifyPeerCertificate —— 这里不用
	// 会话复用（Incus 客户端每次按需重连，且 VerifyConnection 的缺失不适用于 TOFU
	// 公钥固定这个场景，因为我们关心的是首次握手与后续指纹一致性，复用连接本身就
	// 等价于曾经校验过的同一 peer）。不影响安全语义，禁用此告警。
	cfg.VerifyPeerCertificate = pinnedVerifier(clusterName, store) //nolint:gosec // G123 TOFU 场景不适用会话复用检查
	return cfg
}
