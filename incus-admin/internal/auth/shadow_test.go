package auth

import (
	"strings"
	"testing"
	"time"
)

func makeClaims() ShadowClaims {
	now := time.Now()
	return ShadowClaims{
		ActorID:     42,
		ActorEmail:  "admin@example.com",
		TargetID:    7,
		TargetEmail: "user@example.com",
		Reason:      "ticket #101 investigation",
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(ShadowTTL).Unix(),
	}
}

func TestShadowSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef") // >= 16 bytes
	c := makeClaims()

	tok, err := SignShadow(secret, c)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(tok, ".") {
		t.Fatalf("expected 'payload.sig' shape, got %q", tok)
	}

	got, err := VerifyShadow(secret, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.ActorID != c.ActorID || got.TargetID != c.TargetID || got.Reason != c.Reason {
		t.Fatalf("claim mismatch: %+v vs %+v", got, c)
	}
}

func TestShadowVerifyRejectsShortSecret(t *testing.T) {
	if _, err := SignShadow([]byte("short"), makeClaims()); err == nil {
		t.Fatalf("expected sign with short secret to fail")
	}
	if _, err := VerifyShadow([]byte("short"), "anything.sig"); err == nil {
		t.Fatalf("expected verify with short secret to fail")
	}
}

func TestShadowVerifyRejectsMalformed(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	cases := []string{
		"",
		"no-dot-here",
		"onlyleft.",
		".onlyright",
		"not_base64_payload.not_base64_sig",
	}
	for _, c := range cases {
		if _, err := VerifyShadow(secret, c); err == nil {
			t.Errorf("expected malformed token %q to fail", c)
		}
	}
}

func TestShadowVerifyRejectsBadSignature(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok, err := SignShadow(secret, makeClaims())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Flip a signature byte — any single-char tamper invalidates HMAC.
	parts := strings.SplitN(tok, ".", 2)
	tampered := parts[0] + "." + flipLast(parts[1])
	if _, err := VerifyShadow(secret, tampered); err == nil {
		t.Fatalf("expected signature-tamper to fail")
	}
	// Wrong secret should also fail.
	wrong := []byte("aaaaaaaaaaaaaaaabbbbbbbbbbbbbbbb")
	if _, err := VerifyShadow(wrong, tok); err == nil {
		t.Fatalf("expected wrong-secret to fail")
	}
}

func TestShadowVerifyRejectsExpired(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	c := makeClaims()
	c.ExpiresAt = time.Now().Add(-1 * time.Minute).Unix()
	tok, err := SignShadow(secret, c)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = VerifyShadow(secret, tok)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got: %v", err)
	}
}

// flipLast toggles the last char so the signature compare fails without
// breaking the base64 alphabet shape.
func flipLast(s string) string {
	if s == "" {
		return "X"
	}
	b := []byte(s)
	if b[len(b)-1] == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	return string(b)
}
