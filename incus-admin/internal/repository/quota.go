package repository

import (
	"context"
	"database/sql"

	"github.com/incuscloud/incus-admin/internal/model"
)

type QuotaRepo struct {
	db *sql.DB
}

func NewQuotaRepo(db *sql.DB) *QuotaRepo {
	return &QuotaRepo{db: db}
}

func (r *QuotaRepo) GetByUserID(ctx context.Context, userID int64) (*model.Quota, error) {
	var q model.Quota
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, max_vms, max_vcpus, max_ram_mb, max_disk_gb, max_ips, max_snapshots
		 FROM quotas WHERE user_id = $1`, userID,
	).Scan(&q.ID, &q.UserID, &q.MaxVMs, &q.MaxVCPUs, &q.MaxRAMMB, &q.MaxDiskGB, &q.MaxIPs, &q.MaxSnapshots)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

func (r *QuotaRepo) Update(ctx context.Context, q *model.Quota) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE quotas SET max_vms=$1, max_vcpus=$2, max_ram_mb=$3, max_disk_gb=$4, max_ips=$5, max_snapshots=$6
		 WHERE user_id=$7`,
		q.MaxVMs, q.MaxVCPUs, q.MaxRAMMB, q.MaxDiskGB, q.MaxIPs, q.MaxSnapshots, q.UserID)
	return err
}
