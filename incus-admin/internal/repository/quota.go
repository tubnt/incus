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
		`SELECT id, user_id, max_vms, max_vcpus, max_ram_mb, max_disk_gb, max_ips, max_snapshots,
		        max_firewall_groups, max_firewall_rules_per_group
		 FROM quotas WHERE user_id = $1`, userID,
	).Scan(&q.ID, &q.UserID, &q.MaxVMs, &q.MaxVCPUs, &q.MaxRAMMB, &q.MaxDiskGB, &q.MaxIPs, &q.MaxSnapshots,
		&q.MaxFirewallGroups, &q.MaxFirewallRulesPerGroup)
	// WP-E 契约修正：缺配额行 = 用户未设配额 = 零记录（走默认放行），不是错误。
	// 原实现把 sql.ErrNoRows 当 error 返回，调用方 fail-closed 分支误判成 DB 故障
	// 而回 503。统一约定：无行返 (nil, nil)，真 DB 错误才返 err，调用方据此 fail-closed。
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

func (r *QuotaRepo) Update(ctx context.Context, q *model.Quota) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE quotas SET max_vms=$1, max_vcpus=$2, max_ram_mb=$3, max_disk_gb=$4, max_ips=$5, max_snapshots=$6,
		                   max_firewall_groups=$7, max_firewall_rules_per_group=$8
		 WHERE user_id=$9`,
		q.MaxVMs, q.MaxVCPUs, q.MaxRAMMB, q.MaxDiskGB, q.MaxIPs, q.MaxSnapshots,
		q.MaxFirewallGroups, q.MaxFirewallRulesPerGroup, q.UserID)
	return err
}
