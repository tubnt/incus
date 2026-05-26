package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// ChargeRepo PLAN-054 / INFRA-013 billing_charges 表读写。
//
// skeleton：方法签名稳定，业务接入留给 L3-F billing worker。
type ChargeRepo struct {
	db *sql.DB
}

func NewChargeRepo(db *sql.DB) *ChargeRepo {
	return &ChargeRepo{db: db}
}

const chargeSelectCols = `id, subscription_id, charge_date, amount, status, transaction_id, created_at`

func scanCharge(row interface{ Scan(...any) error }, c *model.BillingCharge) error {
	return row.Scan(
		&c.ID, &c.SubscriptionID, &c.ChargeDate, &c.Amount, &c.Status, &c.TransactionID, &c.CreatedAt,
	)
}

// ErrChargeDuplicate 由 Insert 在唯一约束 (subscription_id, charge_date) 冲突时返回；
// worker 应当把它视为正常 idempotent 跳过，不报错升级。
var ErrChargeDuplicate = errors.New("billing charge already exists for this sub+date")

// Insert 写入一次扣费记账。UNIQUE 冲突会返回 ErrChargeDuplicate（cron 重跑保护）。
// transactionID 仅 status='paid' 时填，'insufficient'/'skipped' 传 nil。
func (r *ChargeRepo) Insert(ctx context.Context, c *model.BillingCharge) (*model.BillingCharge, error) {
	var out model.BillingCharge
	row := r.db.QueryRowContext(ctx,
		`INSERT INTO billing_charges (subscription_id, charge_date, amount, status, transaction_id)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+chargeSelectCols,
		c.SubscriptionID, c.ChargeDate, c.Amount, c.Status, c.TransactionID,
	)
	if err := scanCharge(row, &out); err != nil {
		// PostgreSQL UNIQUE 冲突 (sqlstate 23505)：检测字符串而不是导入 pgconn，
		// 与现有 repo（如 user_repo）保持一致风格。
		if isUniqueViolation(err) {
			return nil, ErrChargeDuplicate
		}
		return nil, fmt.Errorf("insert charge: %w", err)
	}
	return &out, nil
}

// ListBySubscription 列某订阅最近的扣费记录，DESC。limit<=0 表示不限制。
// portal /billing 详情页 + admin 单 sub 排错都用它。
func (r *ChargeRepo) ListBySubscription(ctx context.Context, subID int64, limit int) ([]model.BillingCharge, error) {
	query := `SELECT ` + chargeSelectCols + `
		FROM billing_charges
		WHERE subscription_id = $1
		ORDER BY charge_date DESC, id DESC`
	args := []any{subID}
	if limit > 0 {
		query += ` LIMIT $2`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list charges: %w", err)
	}
	defer rows.Close()
	out := make([]model.BillingCharge, 0)
	for rows.Next() {
		var c model.BillingCharge
		if err := scanCharge(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetByDate 查 (sub_id, date) 是否已有记账。worker 在 INSERT 前可选地用它做
// 预探测，但 INSERT 的 UNIQUE 冲突已是兜底，本方法主要给 admin 排错。
func (r *ChargeRepo) GetByDate(ctx context.Context, subID int64, date time.Time) (*model.BillingCharge, error) {
	var c model.BillingCharge
	row := r.db.QueryRowContext(ctx,
		`SELECT `+chargeSelectCols+`
		 FROM billing_charges WHERE subscription_id = $1 AND charge_date = $2`,
		subID, date)
	err := scanCharge(row, &c)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// isUniqueViolation 通过错误字符串检测 pgx 唯一约束冲突。
// 不引入 pgconn 直接依赖，与本 package 已有的 pgInt64Array / pgTextArray
// 「轻量私有适配」风格保持一致。pgx/v5 stdlib 错误信息形如：
//   "ERROR: duplicate key value violates unique constraint ... (SQLSTATE 23505)"
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505") || strings.Contains(msg, "unique constraint")
}
