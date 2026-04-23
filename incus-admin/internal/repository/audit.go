package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

type AuditRepo struct {
	db *sql.DB
}

func NewAuditRepo(db *sql.DB) *AuditRepo {
	return &AuditRepo{db: db}
}

func (r *AuditRepo) Log(ctx context.Context, userID *int64, action, targetType string, targetID int64, details any, ip string) {
	detailsJSON, _ := json.Marshal(details)
	// ip_address is INET; passing an empty string fails the cast at Postgres
	// level and silently swallows the audit row. Callers without a caller IP
	// (system-initiated writes like the reconciler worker) pass "" —
	// translate to NULL so those rows land correctly.
	var ipArg any
	if ip != "" {
		ipArg = ip
	}
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO audit_logs (user_id, action, target_type, target_id, details, ip_address) VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, targetType, targetID, string(detailsJSON), ipArg)
}

func (r *AuditRepo) List(ctx context.Context, limit, offset int) ([]model.AuditLog, int, error) {
	var total int
	_ = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs`).Scan(&total)

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, action, target_type, target_id, COALESCE(details::text, '{}'), COALESCE(ip_address::text, ''), created_at
		 FROM audit_logs ORDER BY id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []model.AuditLog
	for rows.Next() {
		var l model.AuditLog
		var uid sql.NullInt64
		if err := rows.Scan(&l.ID, &uid, &l.Action, &l.TargetType, &l.TargetID, &l.Details, &l.IPAddress, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		if uid.Valid {
			l.UserID = &uid.Int64
		}
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

// ExportRange streams audit rows in the [from, to] window to fn. Returning an
// error from fn aborts iteration. Caller is responsible for ordering
// concerns; rows are emitted in descending id (newest first) with a hard cap
// of 100k to keep CSV exports bounded.
func (r *AuditRepo) ExportRange(ctx context.Context, from, to time.Time, actionPrefix string, fn func(model.AuditLog) error) error {
	query := `SELECT id, user_id, action, target_type, target_id,
                     COALESCE(details::text, '{}'), COALESCE(ip_address::text, ''), created_at
              FROM audit_logs
              WHERE created_at BETWEEN $1 AND $2`
	args := []any{from, to}
	if actionPrefix != "" {
		query += ` AND action LIKE $3`
		args = append(args, actionPrefix+"%")
	}
	query += ` ORDER BY id DESC LIMIT 100000`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var l model.AuditLog
		var uid sql.NullInt64
		if err := rows.Scan(&l.ID, &uid, &l.Action, &l.TargetType, &l.TargetID, &l.Details, &l.IPAddress, &l.CreatedAt); err != nil {
			return err
		}
		if uid.Valid {
			l.UserID = &uid.Int64
		}
		if err := fn(l); err != nil {
			return err
		}
	}
	return rows.Err()
}

// DeleteOlderThan deletes rows with created_at < cutoff and returns the count.
// Used by the retention worker to enforce audit_retention_days.
func (r *AuditRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
