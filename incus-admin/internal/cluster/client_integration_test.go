package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newFakeIncusClient wires a bare *Client directly at a fake Incus server.
// Bypasses newClient() so we skip the mTLS pin / health-check round-trip —
// the test server speaks plain HTTP, which is all these tests need.
func newFakeIncusClient(t *testing.T, apiURL string) *Client {
	t.Helper()
	return &Client{
		Name:       "fake",
		APIURL:     strings.TrimRight(apiURL, "/"),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// TestClient_GetInstances_Success confirms the client parses the Incus
// `recursion=1` instance listing into a slice of raw JSON objects and that
// consumers (event listener / reconciler adapter) can extract the `name`
// field by re-parsing each element.
func TestClient_GetInstances_Success(t *testing.T) {
	want := []map[string]any{
		{"name": "vm-alpha", "location": "node1", "status": "Running", "type": "virtual-machine"},
		{"name": "vm-beta", "location": "node2", "status": "Stopped", "type": "virtual-machine"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Incus shape: /1.0/instances?recursion=1&project=...
		if !strings.HasPrefix(r.URL.Path, "/1.0/instances") {
			http.NotFound(w, r)
			return
		}
		// Incus GetInstances uses recursion=2 (full state). Accept either
		// value so future tuning of recursion depth doesn't wedge the test.
		if rec := r.URL.Query().Get("recursion"); rec != "1" && rec != "2" {
			http.Error(w, "missing recursion param", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":        "sync",
			"status":      "Success",
			"status_code": 200,
			"metadata":    want,
		})
	}))
	defer ts.Close()

	c := newFakeIncusClient(t, ts.URL)
	raws, err := c.GetInstances(context.Background(), "customers")
	if err != nil {
		t.Fatalf("GetInstances: %v", err)
	}
	if len(raws) != len(want) {
		t.Fatalf("got %d instances, want %d", len(raws), len(want))
	}
	for i, raw := range raws {
		var got struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("row %d unparseable: %v", i, err)
		}
		if got.Name != want[i]["name"] {
			t.Fatalf("row %d name: got %q, want %q", i, got.Name, want[i]["name"])
		}
	}
}

// TestClient_GetInstances_IncusError confirms the client surfaces
// Incus-side errors as Go errors. The event listener relies on this to
// skip a misconfigured cluster without starving the others.
func TestClient_GetInstances_IncusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":       "error",
			"error_code": 403,
			"error":      "Certificate is restricted",
		})
	}))
	defer ts.Close()

	c := newFakeIncusClient(t, ts.URL)
	_, err := c.GetInstances(context.Background(), "customers")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Certificate is restricted") {
		t.Fatalf("error missing Incus message: %v", err)
	}
}

// TestClient_GetClusterMembers exercises the /1.0/cluster/members path so
// the HA status page has fault coverage on the same HTTP plumbing.
func TestClient_GetClusterMembers(t *testing.T) {
	members := []map[string]any{
		{"server_name": "node1", "status": "Online", "message": "Fully operational"},
		{"server_name": "node2", "status": "Online", "message": "Fully operational"},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.0/cluster/members" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":        "sync",
			"status":      "Success",
			"status_code": 200,
			"metadata":    members,
		})
	}))
	defer ts.Close()

	c := newFakeIncusClient(t, ts.URL)
	got, err := c.GetClusterMembers(context.Background())
	if err != nil {
		t.Fatalf("GetClusterMembers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d members, want 2", len(got))
	}
}

// TestClient_WaitForOperation polls /1.0/operations/<id>/wait until 200 so
// async API calls (evacuate, snapshot create, restore) can complete.
func TestClient_WaitForOperation(t *testing.T) {
	opID := "op-xyz"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := fmt.Sprintf("/1.0/operations/%s/wait", opID)
		if r.URL.Path != wantPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":        "sync",
			"status":      "Success",
			"status_code": 200,
			"metadata":    map[string]any{"id": opID, "status": "Success"},
		})
	}))
	defer ts.Close()

	c := newFakeIncusClient(t, ts.URL)
	if err := c.WaitForOperation(context.Background(), opID); err != nil {
		t.Fatalf("WaitForOperation: %v", err)
	}
}
