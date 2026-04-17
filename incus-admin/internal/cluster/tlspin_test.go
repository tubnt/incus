package cluster

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu   sync.Mutex
	pins map[string]string
	sets int
}

func newMemStore() *memStore { return &memStore{pins: map[string]string{}} }

func (s *memStore) Get(_ context.Context, cluster string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pins[cluster], nil
}

func (s *memStore) Set(_ context.Context, cluster, fp string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pins[cluster] = fp
	s.sets++
	return nil
}

func makeSelfSignedDER(t *testing.T, cn string) ([]byte, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return der, parsed
}

func TestSPKIFingerprint_NilCert(t *testing.T) {
	if got := SPKIFingerprint(nil); got != "" {
		t.Fatalf("want empty for nil, got %q", got)
	}
}

func TestSPKIFingerprint_Stable(t *testing.T) {
	_, cert := makeSelfSignedDER(t, "stable")
	a := SPKIFingerprint(cert)
	b := SPKIFingerprint(cert)
	if a == "" || a != b {
		t.Fatalf("expected stable non-empty fingerprint, got a=%q b=%q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64 hex chars (sha256), got %d", len(a))
	}
}

func TestPinnedVerifier_TOFULearnsOnce(t *testing.T) {
	der, _ := makeSelfSignedDER(t, "tofu")
	store := newMemStore()
	verify := pinnedVerifier("c1", store)

	// First handshake: empty store, should learn and persist.
	if err := verify([][]byte{der}, nil); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if got := store.pins["c1"]; got == "" {
		t.Fatalf("expected pin written, got empty")
	}
	if store.sets != 1 {
		t.Fatalf("expected single Set call, got %d", store.sets)
	}

	// Second handshake with same cert: store already has pin, must match.
	if err := verify([][]byte{der}, nil); err != nil {
		t.Fatalf("second verify: %v", err)
	}
	if store.sets != 1 {
		t.Fatalf("Set must not be called again after TOFU, got %d", store.sets)
	}
}

func TestPinnedVerifier_MismatchRejects(t *testing.T) {
	derA, _ := makeSelfSignedDER(t, "a")
	derB, _ := makeSelfSignedDER(t, "b")
	store := newMemStore()
	verify := pinnedVerifier("c1", store)

	// Seed pin with cert A's SPKI.
	if err := verify([][]byte{derA}, nil); err != nil {
		t.Fatalf("seed verify: %v", err)
	}
	// Present cert B — mismatch must fail.
	err := verify([][]byte{derB}, nil)
	if err == nil {
		t.Fatalf("expected mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPinnedVerifier_EmptyChain(t *testing.T) {
	verify := pinnedVerifier("c1", newMemStore())
	if err := verify([][]byte{}, nil); err == nil {
		t.Fatalf("expected error on empty cert chain")
	}
}

func TestPinnedVerifier_StoreGetError(t *testing.T) {
	der, _ := makeSelfSignedDER(t, "err")
	store := &errStore{err: errors.New("boom")}
	verify := pinnedVerifier("c1", store)
	err := verify([][]byte{der}, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped store error, got %v", err)
	}
}

type errStore struct{ err error }

func (s *errStore) Get(context.Context, string) (string, error)    { return "", s.err }
func (s *errStore) Set(context.Context, string, string) error      { return s.err }
