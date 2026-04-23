package cluster

import (
	"net/url"
	"strings"
	"testing"
)

// TestInstanceNameFromSource covers every Source-path shape Incus emits for
// instance lifecycle events so the event listener doesn't mis-route events
// to wrong VM rows.
func TestInstanceNameFromSource(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/1.0/instances/vm-abc", "vm-abc"},
		{"/1.0/instances/vm-abc/state", "vm-abc"},
		{"/1.0/instances/vm-abc/snapshots/snap1", "vm-abc"},
		{"/1.0/instances/", ""},
		{"/1.0/networks/foo", ""},
		{"/1.0/cluster/members/node1", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := InstanceNameFromSource(tt.in); got != tt.want {
				t.Fatalf("InstanceNameFromSource(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestClusterMemberNameFromSource mirrors the above for cluster member
// lifecycle events (PLAN-020 Phase D.2).
func TestClusterMemberNameFromSource(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/1.0/cluster/members/node1", "node1"},
		{"/1.0/cluster/members/node1/state", "node1"},
		{"/1.0/cluster", ""},
		{"/1.0/instances/vm-x", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ClusterMemberNameFromSource(tt.in); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildEventsWSURL verifies the scheme switch (https→wss, http→ws) and
// type filter encoding. A regression here silently disables the listener
// (connect to the wrong URL) so the coverage is load-bearing.
func TestBuildEventsWSURL(t *testing.T) {
	tests := []struct {
		name    string
		apiURL  string
		types   []string
		wantErr bool
		// Assertions on the resulting URL; empty means don't check.
		wantScheme string
		wantPath   string
		wantTypes  string // comma-joined after URL decode
	}{
		{
			name:       "https with types",
			apiURL:     "https://10.0.20.1:8443",
			types:      []string{"lifecycle", "cluster"},
			wantScheme: "wss",
			wantPath:   "/1.0/events",
			wantTypes:  "lifecycle,cluster",
		},
		{
			name:       "http fallback",
			apiURL:     "http://localhost:9000",
			types:      []string{"lifecycle"},
			wantScheme: "ws",
			wantPath:   "/1.0/events",
			wantTypes:  "lifecycle",
		},
		{
			name:       "empty types drops the query param",
			apiURL:     "https://host:8443",
			types:      nil,
			wantScheme: "wss",
			wantPath:   "/1.0/events",
			wantTypes:  "",
		},
		{
			name:    "unknown scheme errors",
			apiURL:  "ftp://bad",
			types:   []string{"lifecycle"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildEventsWSURL(tt.apiURL, tt.types)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			u, perr := url.Parse(got)
			if perr != nil {
				t.Fatalf("resulting URL %q unparseable: %v", got, perr)
			}
			if u.Scheme != tt.wantScheme {
				t.Fatalf("scheme: got %q, want %q", u.Scheme, tt.wantScheme)
			}
			if u.Path != tt.wantPath {
				t.Fatalf("path: got %q, want %q", u.Path, tt.wantPath)
			}
			gotTypes := u.Query().Get("type")
			if tt.wantTypes == "" && gotTypes != "" {
				t.Fatalf("type query should be absent, got %q", gotTypes)
			}
			if tt.wantTypes != "" && gotTypes != tt.wantTypes {
				t.Fatalf("type query: got %q, want %q", gotTypes, tt.wantTypes)
			}
		})
	}
}

// TestBuildEventsWSURL_BaseURLWithTrailingSlash confirms the builder
// doesn't duplicate the slash before /1.0/events. Regression guard only —
// url.Parse normalises this but we want deterministic output for logs.
func TestBuildEventsWSURL_BaseURLWithTrailingSlash(t *testing.T) {
	got, err := buildEventsWSURL("https://host:8443/", []string{"lifecycle"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "wss://host:8443/1.0/events") {
		t.Fatalf("unexpected URL: %q", got)
	}
}
