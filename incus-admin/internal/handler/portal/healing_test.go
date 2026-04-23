package portal

import (
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/repository"
)

// TestSerialiseHealing covers the JSON shape the HA history UI depends on.
// Keep columns stable: renaming a field here cascades into /admin/ha table
// bindings + drawer field labels (Phase F).
func TestSerialiseHealing(t *testing.T) {
	start := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	completed := start.Add(5 * time.Minute)
	actorID := int64(23)
	errMsg := "evacuate: cert restricted"

	tests := []struct {
		name    string
		event   repository.HealingEvent
		cluster string
		checks  func(t *testing.T, row map[string]any)
	}{
		{
			name: "in_progress no completion no error",
			event: repository.HealingEvent{
				ID: 1, ClusterID: 10, NodeName: "node1",
				Trigger: "auto", StartedAt: start, Status: "in_progress",
			},
			cluster: "cn-sz-01",
			checks: func(t *testing.T, row map[string]any) {
				if row["id"] != int64(1) {
					t.Fatalf("id: got %v", row["id"])
				}
				if row["cluster_name"] != "cn-sz-01" {
					t.Fatalf("cluster_name: got %v", row["cluster_name"])
				}
				if _, present := row["completed_at"]; present {
					t.Fatalf("completed_at should be omitted when nil")
				}
				if _, present := row["error"]; present {
					t.Fatalf("error should be omitted when nil")
				}
				if _, present := row["duration_seconds"]; present {
					t.Fatalf("duration_seconds should be omitted without completed_at")
				}
			},
		},
		{
			name: "completed with duration + actor",
			event: repository.HealingEvent{
				ID: 2, ClusterID: 10, NodeName: "node2",
				Trigger: "manual", ActorID: &actorID,
				StartedAt: start, CompletedAt: &completed, Status: "completed",
			},
			cluster: "cn-sz-01",
			checks: func(t *testing.T, row map[string]any) {
				if row["duration_seconds"] != int64(300) {
					t.Fatalf("duration_seconds: got %v", row["duration_seconds"])
				}
				if row["actor_id"] == nil {
					t.Fatalf("actor_id should be present for manual")
				}
			},
		},
		{
			name: "failed with error message",
			event: repository.HealingEvent{
				ID: 3, ClusterID: 10, NodeName: "node3",
				Trigger: "chaos", ActorID: &actorID,
				StartedAt: start, CompletedAt: &completed, Status: "failed",
				Error: &errMsg,
			},
			cluster: "cn-sz-01",
			checks: func(t *testing.T, row map[string]any) {
				if row["error"] != errMsg {
					t.Fatalf("error: got %v", row["error"])
				}
				if row["trigger"] != "chaos" {
					t.Fatalf("trigger: got %v", row["trigger"])
				}
			},
		},
		{
			name: "unknown cluster id falls back to empty cluster_name",
			event: repository.HealingEvent{
				ID: 4, ClusterID: 99, NodeName: "node4",
				Trigger: "auto", StartedAt: start, Status: "partial",
			},
			cluster: "",
			checks: func(t *testing.T, row map[string]any) {
				if row["cluster_name"] != "" {
					t.Fatalf("cluster_name: expected empty, got %v", row["cluster_name"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := serialiseHealing(tt.event, tt.cluster)
			tt.checks(t, row)
		})
	}
}

// TestParseHealingTime covers the two formats the List endpoint accepts as
// from/to: RFC3339 and YYYY-MM-DD. Invalid input returns zero so the handler
// treats it as "no filter" rather than surfacing a 400.
func TestParseHealingTime(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantZero bool
	}{
		{"RFC3339", "2026-04-19T10:00:00Z", false},
		{"date only", "2026-04-19", false},
		{"empty", "", true},
		{"garbage", "yesterday please", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHealingTime(tt.in)
			if tt.wantZero && !got.IsZero() {
				t.Fatalf("expected zero time, got %v", got)
			}
			if !tt.wantZero && got.IsZero() {
				t.Fatalf("expected non-zero, got zero for %q", tt.in)
			}
		})
	}
}
