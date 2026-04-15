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
}

func NewManager(clusters []config.ClusterConfig) (*Manager, error) {
	m := &Manager{
		clients: make(map[string]*Client),
		configs: clusters,
	}

	for _, cc := range clusters {
		client, err := newClient(cc)
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
	for _, cc := range m.configs {
		if cc.Name == name {
			return cc, true
		}
	}
	return config.ClusterConfig{}, false
}

func buildTLSConfig(cc config.ClusterConfig) (*tls.Config, error) {
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

func buildHTTPClient(cc config.ClusterConfig) (*http.Client, error) {
	tlsConfig, err := buildTLSConfig(cc)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
	}, nil
}
