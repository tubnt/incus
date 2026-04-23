package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ShadowSessionCookie is the HTTP cookie name used to carry an admin's
// "acting as another user" session. Keeping the name here so middleware and
// handler agree without hardcoding it twice.
const ShadowSessionCookie = "shadow_session"

// ShadowTTL bounds how long a single shadow-login session can live. PLAN-019
// fixes this at 30 minutes — long enough for a ticket investigation, short
// enough to fail closed if the admin forgets to exit.
const ShadowTTL = 30 * time.Minute

// ShadowClaims is the payload serialized into the signed shadow cookie.
// The struct is exported so handlers and middleware share one definition
// (no drift between sign and verify sites).
type ShadowClaims struct {
	ActorID     int64  `json:"actor_id"`
	ActorEmail  string `json:"actor_email"`
	TargetID    int64  `json:"target_user_id"`
	TargetEmail string `json:"target_email"`
	Reason      string `json:"reason"`
	ExpiresAt   int64  `json:"exp"`
	IssuedAt    int64  `json:"iat"`
}

// SignShadow packs claims into a compact HMAC-signed token:
//   base64url(payload_json) + "." + base64url(hmac_sha256(payload))
// secret must be at least 16 bytes; enforced to catch misconfig early.
func SignShadow(secret []byte, c ShadowClaims) (string, error) {
	if len(secret) < 16 {
		return "", fmt.Errorf("shadow secret too short")
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + sig, nil
}

// VerifyShadow returns the claims if the signature verifies and the token
// hasn't expired. Returns distinct errors so the caller can distinguish
// "malformed" (probably tampered) from "expired" (benign — tell user to
// re-shadow).
func VerifyShadow(secret []byte, token string) (*ShadowClaims, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("shadow secret too short")
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed shadow token")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return nil, fmt.Errorf("shadow signature mismatch")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("shadow payload decode: %w", err)
	}
	var c ShadowClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("shadow payload parse: %w", err)
	}
	if time.Now().Unix() > c.ExpiresAt {
		return nil, fmt.Errorf("shadow session expired")
	}
	return &c, nil
}
