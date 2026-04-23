// Package auth wraps the Logto OIDC client used by the step-up authentication
// flow. The main Bearer / oauth2-proxy header auth stays in internal/middleware.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCClient carries a verified OIDC provider handle plus the oauth2 config used
// to construct step-up authorization URLs and exchange authorization codes.
type OIDCClient struct {
	Provider *oidc.Provider
	Config   oauth2.Config
	Verifier *oidc.IDTokenVerifier
}

// StepUpClaims is the minimal subset of ID token claims the step-up flow needs.
type StepUpClaims struct {
	Sub      string `json:"sub"`
	Email    string `json:"email"`
	AuthTime int64  `json:"auth_time"`
}

// NewOIDCClient discovers the provider and returns a ready-to-use client.
// The caller owns the ctx lifetime (only used for discovery).
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*OIDCClient, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %q: %w", issuer, err)
	}
	return &OIDCClient{
		Provider: provider,
		Config: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		Verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
	}, nil
}

// StepUpAuthURL returns the Logto authorization URL with prompt=login and
// max_age=0 so that Logto forces the user through a full re-auth (including MFA
// if enabled in Logto), regardless of current Logto session freshness.
func (c *OIDCClient) StepUpAuthURL(state string) string {
	return c.Config.AuthCodeURL(state,
		oauth2.SetAuthURLParam("prompt", "login"),
		oauth2.SetAuthURLParam("max_age", "0"))
}

// VerifyCode exchanges the authorization code for tokens, verifies the
// id_token signature/issuer/audience, and returns the minimal claims needed to
// match the user and record the step-up timestamp.
func (c *OIDCClient) VerifyCode(ctx context.Context, code string) (*StepUpClaims, error) {
	tok, err := c.Config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, fmt.Errorf("oidc provider returned no id_token")
	}
	idToken, err := c.Verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	var claims StepUpClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("parse id_token claims: %w", err)
	}
	if claims.AuthTime == 0 {
		// Logto always issues auth_time when max_age is set; zero value would
		// break the freshness check downstream.
		return nil, fmt.Errorf("id_token missing auth_time claim")
	}
	return &claims, nil
}

// SignState packs `rd` (return URL) and a short expiry into an HMAC-signed
// opaque string carried through the OIDC round-trip via the `state` parameter.
// Format: base64url(expiresUnix:rd) + "." + base64url(hmac-sha256)
func SignState(secret []byte, rd string, ttl time.Duration) (string, error) {
	if len(secret) < 16 {
		return "", fmt.Errorf("state secret too short")
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	expires := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%d:%s:%s", expires, base64.RawURLEncoding.EncodeToString(nonce[:]), rd)
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + sig, nil
}

// VerifyState checks signature + expiry and returns the original `rd`.
// Returns an error (not a bool) so the caller can distinguish "malformed",
// "bad signature", and "expired" without exposing these details to the client.
func VerifyState(secret []byte, state string) (rd string, err error) {
	if len(secret) < 16 {
		return "", fmt.Errorf("state secret too short")
	}
	parts := strings.SplitN(state, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed state")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return "", fmt.Errorf("state signature mismatch")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("state payload decode: %w", err)
	}
	// payload = "<expiresUnix>:<nonceB64>:<rd>"
	sepIdx := strings.Index(string(raw), ":")
	if sepIdx < 0 {
		return "", fmt.Errorf("malformed state payload")
	}
	s := string(raw)
	secondIx := strings.Index(s[sepIdx+1:], ":")
	if secondIx < 0 {
		return "", fmt.Errorf("malformed state payload")
	}
	expiresStr := s[:sepIdx]
	rd = s[sepIdx+1+secondIx+1:]
	var expires int64
	if _, err := fmt.Sscanf(expiresStr, "%d", &expires); err != nil {
		return "", fmt.Errorf("state expires parse: %w", err)
	}
	if time.Now().Unix() > expires {
		return "", fmt.Errorf("state expired")
	}
	return rd, nil
}
