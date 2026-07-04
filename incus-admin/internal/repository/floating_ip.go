package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/incuscloud/incus-admin/internal/model"
)

type FloatingIPRepo struct {
	db *sql.DB
}

func NewFloatingIPRepo(db *sql.DB) *FloatingIPRepo {
	return &FloatingIPRepo{db: db}
}

// Status transitions enforced at the SQL level via CHECK constraint. These
// constants are the authoritative values — the CHECK will reject anything
// else so a typo here is a fast-fail.
const (
	FloatingIPAvailable = "available"
	FloatingIPAttached  = "attached"
)

const floatingIPColumns = `id, cluster_id, host(ip)::text, bound_vm_id, status, description, allocated_at, attached_at, detached_at`

func scanFloatingIP(row interface{ Scan(dest ...any) error }, f *model.FloatingIP) error {
	return row.Scan(&f.ID, &f.ClusterID, &f.IP, &f.BoundVMID, &f.Status, &f.Description,
		&f.AllocatedAt, &f.AttachedAt, &f.DetachedAt)
}

func (r *FloatingIPRepo) List(ctx context.Context) ([]model.FloatingIP, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+floatingIPColumns+` FROM floating_ips ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.FloatingIP, 0)
	for rows.Next() {
		var f model.FloatingIP
		if err := scanFloatingIP(rows, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *FloatingIPRepo) GetByID(ctx context.Context, id int64) (*model.FloatingIP, error) {
	var f model.FloatingIP
	err := scanFloatingIP(
		r.db.QueryRowContext(ctx, `SELECT `+floatingIPColumns+` FROM floating_ips WHERE id = $1`, id),
		&f,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// Allocate reserves a floating IP in the pool. Returns ErrIPAlreadyAllocated
// if the IP is taken (unique index). cluster_id + ip are the only required
// fields; status defaults to 'available' via column default.
var ErrIPAlreadyAllocated = errors.New("floating IP already allocated")

func (r *FloatingIPRepo) Allocate(ctx context.Context, clusterID int64, ip, description string) (*model.FloatingIP, error) {
	var out model.FloatingIP
	err := scanFloatingIP(
		r.db.QueryRowContext(ctx,
			`INSERT INTO floating_ips (cluster_id, ip, description)
			 VALUES ($1, $2::inet, $3)
			 RETURNING `+floatingIPColumns,
			clusterID, ip, description,
		),
		&out,
	)
	if err != nil {
		// 23505 is unique_violation — surface a typed error so the handler
		// can return 409 instead of a generic 500.
		if errIsUniqueViolation(err) {
			return nil, ErrIPAlreadyAllocated
		}
		return nil, fmt.Errorf("allocate floating_ip: %w", err)
	}
	return &out, nil
}

// Attach atomically transitions available → attached only if the row is
// currently available. Returns (false, nil) when the row exists but is
// already attached (caller converts to 409). The atomic UPDATE is the
// concurrency guard for two simultaneous attach requests.
func (r *FloatingIPRepo) Attach(ctx context.Context, id, vmID int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE floating_ips SET bound_vm_id = $1, status = 'attached', attached_at = NOW(), detached_at = NULL
		 WHERE id = $2 AND status = 'available'`,
		vmID, id,
	)
	if err != nil {
		return false, fmt.Errorf("attach floating_ip: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Detach atomically transitions attached → available. Returns (false, nil)
// if the row wasn't attached — safe for repeated detach calls.
func (r *FloatingIPRepo) Detach(ctx context.Context, id int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE floating_ips SET bound_vm_id = NULL, status = 'available', detached_at = NOW()
		 WHERE id = $1 AND status = 'attached'`,
		id,
	)
	if err != nil {
		return false, fmt.Errorf("detach floating_ip: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Release removes the row entirely. Callers must detach first (status check
// keeps a simple guard so a rogue DELETE can't leak an active binding).
func (r *FloatingIPRepo) Release(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM floating_ips WHERE id = $1 AND status = 'available'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("release floating_ip: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("floating_ip is attached or missing; detach first")
	}
	return nil
}

// errIsUniqueViolation matches pgx/pq's SQLSTATE 23505 without importing the
// driver — string match on the well-documented error text keeps this repo
// package driver-neutral.
func errIsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// lib/pq error message pattern; pgx's is similar. Both include "unique".
	msg := err.Error()
	return containsAny(msg, "unique", "duplicate key", "23505")
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(s) >= len(n) {
			for i := 0; i+len(n) <= len(s); i++ {
				if s[i:i+len(n)] == n {
					return true
				}
			}
		}
	}
	return false
}
