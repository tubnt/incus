package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// HealingEvent mirrors the healing_events row. evacuated_vms is kept as a
// JSONB slice of minimal VM movement records so the history UI can render
// from a single table scan without joining vms.
type HealingEvent struct {
	ID            int64
	ClusterID     int64
	NodeName      string
	Trigger       string         // 'manual' | 'auto' | 'chaos'
	ActorID       *int64         // null for trigger='auto'
	EvacuatedVMs  []EvacuatedVM  // parsed from JSONB
	StartedAt     time.Time
	CompletedAt   *time.Time
	Status        string         // 'in_progress' | 'completed' | 'failed' | 'partial'
	Error         *string
}

// EvacuatedVM is the per-VM payload inside evacuated_vms. Populated by the
// event listener as instance-updated events stream in during evacuate.
type EvacuatedVM struct {
	VMID     int64  `json:"vm_id"`
	Name     string `json:"name"`
	FromNode string `json:"from_node"`
	ToNode   string `json:"to_node"`
}

type HealingEventRepo struct {
	db *sql.DB
}

func NewHealingEventRepo(db *sql.DB) *HealingEventRepo {
	return &HealingEventRepo{db: db}
}

// Create starts a new healing event row in status='in_progress'. Returns
// the new id so callers can reference it when the event listener updates
// evacuated_vms / completes / fails.
func (r *HealingEventRepo) Create(ctx context.Context, clusterID int64, nodeName, trigger string, actorID *int64) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO healing_events (cluster_id, node_name, trigger, actor_id, status)
		 VALUES ($1, $2, $3, $4, 'in_progress')
		 RETURNING id`,
		clusterID, nodeName, trigger, actorID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert healing event: %w", err)
	}
	return id, nil
}

// AppendEvacuatedVM adds one VM movement to the event's evacuated_vms JSONB
// array. Atomic via `||` so concurrent event arrivals don't trample each
// other. If the event has already been completed/failed the append is a
// no-op (WHERE status filter).
func (r *HealingEventRepo) AppendEvacuatedVM(ctx context.Context, eventID int64, vm EvacuatedVM) error {
	vmJSON, err := json.Marshal(vm)
	if err != nil {
		return fmt.Errorf("marshal evacuated vm: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`UPDATE healing_events
		 SET evacuated_vms = COALESCE(evacuated_vms, '[]'::jsonb) || $1::jsonb
		 WHERE id = $2 AND status = 'in_progress'`,
		string(vmJSON), eventID,
	)
	if err != nil {
		return fmt.Errorf("append evacuated vm: %w", err)
	}
	return nil
}

// Complete flips status to 'completed' and stamps completed_at.
func (r *HealingEventRepo) Complete(ctx context.Context, eventID int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE healing_events
		 SET status = 'completed', completed_at = NOW()
		 WHERE id = $1 AND status = 'in_progress'`,
		eventID,
	)
	if err != nil {
		return fmt.Errorf("complete healing event: %w", err)
	}
	return nil
}

// Fail marks the event as failed with the given error message.
func (r *HealingEventRepo) Fail(ctx context.Context, eventID int64, reason string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE healing_events
		 SET status = 'failed', completed_at = NOW(), error = $2
		 WHERE id = $1 AND status = 'in_progress'`,
		eventID, reason,
	)
	if err != nil {
		return fmt.Errorf("fail healing event: %w", err)
	}
	return nil
}

// FindInProgressByNode returns the id of the newest in_progress healing
// event for (cluster, node), optionally filtered by trigger (empty string
// matches any trigger). Returns 0 when none exists. Used by the event
// listener to coalesce multiple offline/member-updated signals into one row.
func (r *HealingEventRepo) FindInProgressByNode(ctx context.Context, clusterID int64, nodeName, trigger string) (int64, error) {
	var id int64
	var err error
	if trigger == "" {
		err = r.db.QueryRowContext(ctx,
			`SELECT id FROM healing_events
			 WHERE cluster_id = $1 AND node_name = $2 AND status = 'in_progress'
			 ORDER BY started_at DESC LIMIT 1`,
			clusterID, nodeName,
		).Scan(&id)
	} else {
		err = r.db.QueryRowContext(ctx,
			`SELECT id FROM healing_events
			 WHERE cluster_id = $1 AND node_name = $2 AND trigger = $3 AND status = 'in_progress'
			 ORDER BY started_at DESC LIMIT 1`,
			clusterID, nodeName, trigger,
		).Scan(&id)
	}
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find in-progress healing: %w", err)
	}
	return id, nil
}

// CompleteByNode flips auto-tracked in_progress rows for (cluster, node) to
// 'completed' when the node is observed online again. Explicitly scoped to
// trigger='auto' so a manual evacuate or chaos drill still in progress isn't
// prematurely closed by an online lifecycle event — those flows Complete
// themselves from their own handler/goroutine.
func (r *HealingEventRepo) CompleteByNode(ctx context.Context, clusterID int64, nodeName string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE healing_events
		 SET status = 'completed', completed_at = NOW()
		 WHERE cluster_id = $1 AND node_name = $2
		   AND status = 'in_progress' AND trigger = 'auto'`,
		clusterID, nodeName,
	)
	if err != nil {
		return 0, fmt.Errorf("complete healing by node: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// GetByID returns a single healing event by primary key, or (nil, nil) if no
// such row exists. Used by the admin HA history drawer when the user clicks
// a row — cheaper than scanning ListFiltered output, and unbounded (the 500-row
// list cap used to hide events beyond that window).
func (r *HealingEventRepo) GetByID(ctx context.Context, id int64) (*HealingEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, cluster_id, node_name, trigger, actor_id,
		        COALESCE(evacuated_vms, '[]'::jsonb)::text,
		        started_at, completed_at, status, error
		 FROM healing_events WHERE id = $1`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("get healing event: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	e, err := scanHealingRow(rows)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ExpireStale marks in-progress events older than `cutoff` as 'partial' —
// used by a background sweeper so events that never received their
// completion signal don't linger forever. Returns affected rows.
func (r *HealingEventRepo) ExpireStale(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE healing_events
		 SET status = 'partial', completed_at = NOW(),
		     error = COALESCE(error, 'timed out waiting for completion signal')
		 WHERE status = 'in_progress' AND started_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("expire stale healing events: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// HealingListFilter narrows down the history list. Zero values skip the
// corresponding predicate. Used by the admin HA history page to support
// trigger / status / time-range / node / cluster filters.
type HealingListFilter struct {
	ClusterID int64
	NodeName  string
	Trigger   string // 'manual' | 'auto' | 'chaos'
	Status    string // 'in_progress' | 'completed' | 'failed' | 'partial'
	FromTime  *time.Time
	ToTime    *time.Time
}

// ListFiltered returns a filtered + paginated slice and the total matching
// row count (for pagination UI). Ordered newest-first by started_at.
func (r *HealingEventRepo) ListFiltered(ctx context.Context, f HealingListFilter, limit, offset int) ([]HealingEvent, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	conds := make([]string, 0, 6)
	args := make([]any, 0, 8)
	argN := 1
	if f.ClusterID > 0 {
		conds = append(conds, fmt.Sprintf("cluster_id = $%d", argN))
		args = append(args, f.ClusterID)
		argN++
	}
	if f.NodeName != "" {
		conds = append(conds, fmt.Sprintf("node_name = $%d", argN))
		args = append(args, f.NodeName)
		argN++
	}
	if f.Trigger != "" {
		conds = append(conds, fmt.Sprintf("trigger = $%d", argN))
		args = append(args, f.Trigger)
		argN++
	}
	if f.Status != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", argN))
		args = append(args, f.Status)
		argN++
	}
	if f.FromTime != nil {
		conds = append(conds, fmt.Sprintf("started_at >= $%d", argN))
		args = append(args, *f.FromTime)
		argN++
	}
	if f.ToTime != nil {
		conds = append(conds, fmt.Sprintf("started_at <= $%d", argN))
		args = append(args, *f.ToTime)
		argN++
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	var total int64
	if err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM healing_events "+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count healing events: %w", err)
	}

	query := `SELECT id, cluster_id, node_name, trigger, actor_id,
	        COALESCE(evacuated_vms, '[]'::jsonb)::text,
	        started_at, completed_at, status, error
	 FROM healing_events ` + where + fmt.Sprintf(`
	 ORDER BY started_at DESC
	 LIMIT $%d OFFSET $%d`, argN, argN+1)
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list filtered healing events: %w", err)
	}
	defer rows.Close()

	events := make([]HealingEvent, 0)
	for rows.Next() {
		e, err := scanHealingRow(rows)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, e)
	}
	return events, total, rows.Err()
}

// scanHealingRow keeps the scan+JSON dance in one place so List and
// ListFiltered stay consistent on NULL handling.
func scanHealingRow(rows *sql.Rows) (HealingEvent, error) {
	var e HealingEvent
	var actorID sql.NullInt64
	var vmsJSON string
	var completedAt sql.NullTime
	var errMsg sql.NullString
	if err := rows.Scan(&e.ID, &e.ClusterID, &e.NodeName, &e.Trigger, &actorID,
		&vmsJSON, &e.StartedAt, &completedAt, &e.Status, &errMsg); err != nil {
		return HealingEvent{}, fmt.Errorf("scan healing event: %w", err)
	}
	if actorID.Valid {
		e.ActorID = &actorID.Int64
	}
	if completedAt.Valid {
		e.CompletedAt = &completedAt.Time
	}
	if errMsg.Valid {
		e.Error = &errMsg.String
	}
	if err := json.Unmarshal([]byte(vmsJSON), &e.EvacuatedVMs); err != nil {
		e.EvacuatedVMs = nil
	}
	return e, nil
}

// List returns the most recent events, newest first, capped at limit. Used
// by the admin HA history page.
func (r *HealingEventRepo) List(ctx context.Context, limit int) ([]HealingEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, cluster_id, node_name, trigger, actor_id,
		        COALESCE(evacuated_vms, '[]'::jsonb)::text,
		        started_at, completed_at, status, error
		 FROM healing_events
		 ORDER BY started_at DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list healing events: %w", err)
	}
	defer rows.Close()

	events := make([]HealingEvent, 0)
	for rows.Next() {
		e, err := scanHealingRow(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
