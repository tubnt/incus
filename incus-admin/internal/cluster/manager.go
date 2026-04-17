package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/incuscloud/incus-admin/internal/config"
)

type Manager struct {
	mu       sync.RWMutex
	clients  map[string]*Client
	configs  []config.ClusterConfig
	idByName map[string]int64
	nameByID map[int64]string
	store    FingerprintStore
}

// NewManager builds the cluster manager. When store is non-nil it enforces
// SPKI-pinning for every HTTP/WebSocket client; first-connect learns the pin
// (TOFU). A nil store falls back to InsecureSkipVerify — only acceptable in
// tests or when no CA is configured.
func NewManager(clusters []config.ClusterConfig, store FingerprintStore) (*Manager, error) {
	m := &Manager{
		clients:  make(map[string]*Client),
		configs:  clusters,
		idByName: make(map[string]int64),
		nameByID: make(map[int64]string),
		store:    store,
	}

	for _, cc := range clusters {
		client, err := newClient(cc, store)
		if err != nil {
			slog.Error("failed to connect cluster", "name", cc.Name, "error", err)
			continue
		}
		m.clients[cc.Name] = client
		slog.Info("cluster connected", "name", cc.Name, "url", cc.APIURL)
	}

	if len(m.clients) == 0 {
		return nil, fmt.Errorf("no clusters connected")
	}

	return m, nil
}

// NewTestManager builds a manager without instantiating HTTP clients; used from
// unit tests that need cluster metadata (names, configs) without real TLS.
func NewTestManager(clusters []config.ClusterConfig) *Manager {
	return &Manager{
		clients:  make(map[string]*Client),
		configs:  clusters,
		idByName: make(map[string]int64),
		nameByID: make(map[int64]string),
	}
}

// SetID associates the DB-side cluster row ID with the config cluster name.
// Call once after repository-level upsert at startup.
func (m *Manager) SetID(name string, id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idByName[name] = id
	m.nameByID[id] = name
}

// IDByName returns the DB cluster id for a configured cluster name, 0 if unknown.
func (m *Manager) IDByName(name string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.idByName[name]
}

// NameByID returns the configured cluster name for a DB id, empty if unknown.
func (m *Manager) NameByID(id int64) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nameByID[id]
}

// DisplayNameByName looks up display_name from the in-memory config map.
func (m *Manager) DisplayNameByName(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, cc := range m.configs {
		if cc.Name == name {
			if cc.DisplayName != "" {
				return cc.DisplayName
			}
			return cc.Name
		}
	}
	return name
}

func (m *Manager) Get(name string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clients[name]
	return c, ok
}

func (m *Manager) List() []*Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		result = append(result, c)
	}
	return result
}

func (m *Manager) ConfigByName(name string) (config.ClusterConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, cc := range m.configs {
		if cc.Name == name {
			return cc, true
		}
	}
	return config.ClusterConfig{}, false
}

func (m *Manager) UpdateConfig(name string, cc config.ClusterConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, c := range m.configs {
		if c.Name == name {
			m.configs[i] = cc
			return
		}
	}
}

func (m *Manager) AddCluster(cc config.ClusterConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.clients[cc.Name]; exists {
		return fmt.Errorf("cluster %q already exists", cc.Name)
	}
	client, err := newClient(cc, m.store)
	if err != nil {
		return fmt.Errorf("connect cluster: %w", err)
	}
	m.clients[cc.Name] = client
	m.configs = append(m.configs, cc)
	slog.Info("cluster added dynamically", "name", cc.Name, "url", cc.APIURL)
	return nil
}

func (m *Manager) RemoveCluster(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.clients[name]; !exists {
		return fmt.Errorf("cluster %q not found", name)
	}
	delete(m.clients, name)
	for i, cc := range m.configs {
		if cc.Name == name {
			m.configs = append(m.configs[:i], m.configs[i+1:]...)
			break
		}
	}
	slog.Info("cluster removed dynamically", "name", name)
	return nil
}

// BuildTLSConfig assembles the mTLS client config. When no CA is configured,
// it sets InsecureSkipVerify=true so the SPKI pin callback (layered on later
// via BuildPinnedTLSConfig) is the sole trust anchor. Callers that can supply
// a store should prefer BuildTLSConfigWithPin.
func BuildTLSConfig(cc config.ClusterConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cc.CertFile, cc.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if cc.CAFile != "" {
		caCert, err := os.ReadFile(cc.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = pool
	} else {
		tlsConfig.InsecureSkipVerify = true
	}

	return tlsConfig, nil
}

// BuildTLSConfigWithPin returns a TLS config layered with SPKI pinning when a
// store is supplied. Without a store the base config is returned unchanged —
// only acceptable in tests or explicit opt-out paths.
func BuildTLSConfigWithPin(cc config.ClusterConfig, store FingerprintStore) (*tls.Config, error) {
	base, err := BuildTLSConfig(cc)
	if err != nil {
		return nil, err
	}
	if store == nil {
		slog.Warn("TLS pin store not configured — peer verification relies on CA only", "cluster", cc.Name)
		return base, nil
	}
	return BuildPinnedTLSConfig(base, cc.Name, store), nil
}

// TLSConfigForCluster is the go-to helper for WebSocket dialers. It reuses the
// manager's fingerprint store so console/events connections share the same pin
// with the REST client.
func (m *Manager) TLSConfigForCluster(name string) (*tls.Config, error) {
	cc, ok := m.ConfigByName(name)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", name)
	}
	return BuildTLSConfigWithPin(cc, m.store)
}

func buildHTTPClient(cc config.ClusterConfig, store FingerprintStore) (*http.Client, error) {
	tlsConfig, err := BuildTLSConfigWithPin(cc, store)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     tlsConfig,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}, nil
}
