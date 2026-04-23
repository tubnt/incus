package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSignStateVerifyRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	rd := "/admin/vms?cluster=cn-sz-01"

	state, err := SignState(secret, rd, 5*time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(state, ".") {
		t.Fatalf("expected payload.sig shape, got %q", state)
	}

	got, err := VerifyState(secret, state)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != rd {
		t.Fatalf("rd mismatch: got %q want %q", got, rd)
	}
}

func TestSignStateRejectsShortSecret(t *testing.T) {
	if _, err := SignState([]byte("short"), "/", time.Minute); err == nil {
		t.Fatalf("expected short-secret sign to fail")
	}
	if _, err := VerifyState([]byte("short"), "anything.sig"); err == nil {
		t.Fatalf("expected short-secret verify to fail")
	}
}

func TestVerifyStateRejectsMalformed(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	cases := []string{
		"",
		"no-dot-here",
		"onlyleft.",
		".onlyright",
	}
	for _, c := range cases {
		if _, err := VerifyState(secret, c); err == nil {
			t.Errorf("expected %q to fail", c)
		}
	}
}

func TestVerifyStateRejectsBadSignature(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	s, err := SignState(secret, "/admin", time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	parts := strings.SplitN(s, ".", 2)
	tampered := parts[0] + "." + "AAAA"
	if _, err := VerifyState(secret, tampered); err == nil {
		t.Fatalf("expected signature tamper to fail")
	}
	wrong := []byte("bbbbbbbbbbbbbbbbcccccccccccccccc")
	if _, err := VerifyState(wrong, s); err == nil {
		t.Fatalf("expected wrong-secret to fail")
	}
}

func TestVerifyStateRejectsExpired(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	// Negative TTL → immediate expiry.
	s, err := SignState(secret, "/rd", -1*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = VerifyState(secret, s)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got: %v", err)
	}
}
