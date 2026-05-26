package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// SubscriptionRepo PLAN-054 / INFRA-013 vm_subscriptions 表读写。
//
// 本文件是 schema 阶段的 skeleton：方法签名稳定、SQL 完整可跑，但具体业务流
// （订单成功 hook / billing worker / restore 自愈）由 L3-E / L3-F 后续接入。
// 验收只要求编译通过 + 接口签名清晰，不强制业务测试覆盖。
type SubscriptionRepo struct {
	db *sql.DB
}

func NewSubscriptionRepo(db *sql.DB) *SubscriptionRepo {
	return &SubscriptionRepo{db: db}
}

const subSelectCols = `id, vm_id, product_id, user_id, period,
	daily_rate, monthly_rate, paid_until, status,
	suspended_at, grace_until, created_at, updated_at`

func scanSubscription(row interface{ Scan(...any) error }, s *model.VMSubscription) error {
	return row.Scan(
		&s.ID, &s.VMID, &s.ProductID, &s.UserID, &s.Period,
		&s.DailyRate, &s.MonthlyRate, &s.PaidUntil, &s.Status,
		&s.SuspendedAt, &s.GraceUntil, &s.CreatedAt, &s.UpdatedAt,
	)
}

// Insert 新建一行订阅。period 必须是 'daily' / 'monthly'（DB CHECK 兜底）。
// daily_rate / monthly_rate 由调用方决定填哪一个；另一个传 nil。
// status 必须显式给（'active' / 'suspended' / 'cancelled'）；DB CHECK 拒绝
// 空串。常规建订阅走 model.SubscriptionStatusActive。
func (r *SubscriptionRepo) Insert(ctx context.Context, s *model.VMSubscription) (*model.VMSubscription, error) {
	var out model.VMSubscription
	row := r.db.QueryRowContext(ctx,
		`INSERT INTO vm_subscriptions (vm_id, product_id, user_id, period,
			daily_rate, monthly_rate, paid_until, status, suspended_at, grace_until)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING `+subSelectCols,
		s.VMID, s.ProductID, s.UserID, s.Period,
		s.DailyRate, s.MonthlyRate, s.PaidUntil, s.Status,
		s.SuspendedAt, s.GraceUntil,
	)
	if err := scanSubscription(row, &out); err != nil {
		return nil, fmt.Errorf("insert subscription: %w", err)
	}
	return &out, nil
}

// GetByVM 按 vm_id 查最新一条 active 订阅。trash 后插新 active 行的设计下
// 历史可能有多行；只返 active；无 active 返 (nil, nil)。
func (r *SubscriptionRepo) GetByVM(ctx context.Context, vmID int64) (*model.VMSubscription, error) {
	var s model.VMSubscription
	row := r.db.QueryRowContext(ctx,
		`SELECT `+subSelectCols+`
		 FROM vm_subscriptions
		 WHERE vm_id = $1 AND status = 'active'
		 ORDER BY id DESC LIMIT 1`, vmID)
	err := scanSubscription(row, &s)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetLatestByVM 按 vm_id 查最新一条订阅（任意状态）。restore 流程用：先拿
// period 信息再算新的 paid_until。无任何订阅返 (nil, nil)。
func (r *SubscriptionRepo) GetLatestByVM(ctx context.Context, vmID int64) (*model.VMSubscription, error) {
	var s model.VMSubscription
	row := r.db.QueryRowContext(ctx,
		`SELECT `+subSelectCols+`
		 FROM vm_subscriptions
		 WHERE vm_id = $1
		 ORDER BY id DESC LIMIT 1`, vmID)
	err := scanSubscription(row, &s)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListByUser 列某用户全部订阅，按 created DESC（最新优先）。
// status="" 表示不过滤；否则按指定状态过滤。
func (r *SubscriptionRepo) ListByUser(ctx context.Context, userID int64, status string) ([]model.VMSubscription, error) {
	query := `SELECT ` + subSelectCols + ` FROM vm_subscriptions WHERE user_id = $1`
	args := []any{userID}
	if status != "" {
		query += ` AND status = $2`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()
	out := make([]model.VMSubscription, 0)
	for rows.Next() {
		var s model.VMSubscription
		if err := scanSubscription(rows, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListDueActive worker 用：所有 paid_until <= asOf 的 active 订阅，扫描扣费。
// limit<=0 表示不限制；建议 worker 分批 1000 行/轮，控制单次事务大小。
func (r *SubscriptionRepo) ListDueActive(ctx context.Context, asOf time.Time, limit int) ([]model.VMSubscription, error) {
	query := `SELECT ` + subSelectCols + `
		FROM vm_subscriptions
		WHERE status = 'active' AND paid_until <= $1
		ORDER BY paid_until ASC`
	args := []any{asOf}
	if limit > 0 {
		query += ` LIMIT $2`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list due subscriptions: %w", err)
	}
	defer rows.Close()
	out := make([]model.VMSubscription, 0)
	for rows.Next() {
		var s model.VMSubscription
		if err := scanSubscription(rows, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateStatus 切状态；suspended/cancelled 时调用方应同时给 suspended_at /
// grace_until 赋值（用 UpdateSuspension 更精确，本方法只动 status + updated_at）。
func (r *SubscriptionRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vm_subscriptions SET status = $1, updated_at = NOW() WHERE id = $2`,
		status, id)
	return err
}

// UpdatePaidUntil 扣费成功后推进 paid_until；worker 内部使用。
func (r *SubscriptionRepo) UpdatePaidUntil(ctx context.Context, id int64, paidUntil time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vm_subscriptions SET paid_until = $1, updated_at = NOW() WHERE id = $2`,
		paidUntil, id)
	return err
}

// UpdateSuspension 进入 suspended 时一次写齐 status + suspended_at + grace_until；
// 余额恢复时反向调用 ClearSuspension 把三个字段清回 active。
func (r *SubscriptionRepo) UpdateSuspension(ctx context.Context, id int64, suspendedAt, graceUntil time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vm_subscriptions
		 SET status = 'suspended', suspended_at = $1, grace_until = $2, updated_at = NOW()
		 WHERE id = $3`,
		suspendedAt, graceUntil, id)
	return err
}

// ClearSuspension 余额恢复后清理 suspended_at / grace_until，回到 active。
func (r *SubscriptionRepo) ClearSuspension(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vm_subscriptions
		 SET status = 'active', suspended_at = NULL, grace_until = NULL, updated_at = NOW()
		 WHERE id = $1`, id)
	return err
}

// CancelByVM 把指定 VM 名下所有 active 订阅切到 cancelled。VM trash 时调，
// 只动 active 行（避免误把 suspended/已 cancelled 行重写）。返回受影响行数。
// paid_until 不动 —— 用户已付到的时段保留显示，cancelled 仅表示停扣。
func (r *SubscriptionRepo) CancelByVM(ctx context.Context, vmID int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE vm_subscriptions
		 SET status = 'cancelled', updated_at = NOW()
		 WHERE vm_id = $1 AND status = 'active'`, vmID)
	if err != nil {
		return 0, fmt.Errorf("cancel subscription by vm: %w", err)
	}
	return res.RowsAffected()
}

// ReactivateByVM VM restore 时把最新一条 cancelled 订阅恢复成 active，并把
// paid_until 重置为传入值（重新算一个完整周期，免费"恢复"语义）。返回受
// 影响行数；0 表示该 VM 无 cancelled 订阅（例：旧数据 / 异常路径），调用方
// 自行决定是否记 warning。suspended 行不在此处理 —— 它走 worker 余额恢复路径。
func (r *SubscriptionRepo) ReactivateByVM(ctx context.Context, vmID int64, paidUntil time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE vm_subscriptions
		 SET status = 'active', paid_until = $2, updated_at = NOW()
		 WHERE id = (
		   SELECT id FROM vm_subscriptions
		   WHERE vm_id = $1 AND status = 'cancelled'
		   ORDER BY id DESC LIMIT 1
		 )`, vmID, paidUntil)
	if err != nil {
		return 0, fmt.Errorf("reactivate subscription by vm: %w", err)
	}
	return res.RowsAffected()
}

// GetByID 按主键取一行；不存在返 (nil, nil)。admin reactivate / 单条详情用。
func (r *SubscriptionRepo) GetByID(ctx context.Context, id int64) (*model.VMSubscription, error) {
	var s model.VMSubscription
	row := r.db.QueryRowContext(ctx,
		`SELECT `+subSelectCols+` FROM vm_subscriptions WHERE id = $1`, id)
	err := scanSubscription(row, &s)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListAll admin 视角列全部订阅，按 id DESC（最新优先）。
// status="" 表示不过滤；否则按指定状态过滤。
// 早期数量级与 VM 一致，不分页；数据涨上去再加 ListAllPaged。
func (r *SubscriptionRepo) ListAll(ctx context.Context, status string) ([]model.VMSubscription, error) {
	query := `SELECT ` + subSelectCols + ` FROM vm_subscriptions`
	args := []any{}
	if status != "" {
		query += ` WHERE status = $1`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list all subscriptions: %w", err)
	}
	defer rows.Close()
	out := make([]model.VMSubscription, 0)
	for rows.Next() {
		var s model.VMSubscription
		if err := scanSubscription(rows, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AdminReactivate 是管理员手动恢复入口：无论 suspended / cancelled 一律切回
// active，paid_until 重置为传入值，并清空 suspended_at / grace_until。
// 与 ReactivateByVM（VM restore 触发，只动 cancelled）正交，本路径走 sub_id
// 直接定位行，不限制原状态（已 active 走 handler 层幂等短路）。
func (r *SubscriptionRepo) AdminReactivate(ctx context.Context, id int64, paidUntil time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vm_subscriptions
		 SET status = 'active',
		     paid_until = $1,
		     suspended_at = NULL,
		     grace_until = NULL,
		     updated_at = NOW()
		 WHERE id = $2`, paidUntil, id)
	if err != nil {
		return fmt.Errorf("admin reactivate subscription: %w", err)
	}
	return nil
}
