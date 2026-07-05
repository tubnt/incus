package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

type OrderRepo struct {
	db *sql.DB
}

func NewOrderRepo(db *sql.DB) *OrderRepo {
	return &OrderRepo{db: db}
}

// 列定义集中：所有 SELECT 走 orderSelectCols，PLAN-054 给 orders 加 period
// 后避免散落的 SELECT 漏写。Scan 顺序由 scanOrderRow 兜底，扫描错位是
// d6aee02 教训的重点防御点。
const orderSelectCols = `id, user_id, product_id, cluster_id, status, amount,
	COALESCE(currency, 'USD'), period, expires_at, created_at`

func scanOrderRow(row interface{ Scan(...any) error }, o *model.Order) error {
	return row.Scan(
		&o.ID, &o.UserID, &o.ProductID, &o.ClusterID, &o.Status, &o.Amount,
		&o.Currency, &o.Period, &o.ExpiresAt, &o.CreatedAt,
	)
}

// Create 兼容现行调用方：period 取 DB DEFAULT 'monthly'；新调用走 CreateWithPeriod。
func (r *OrderRepo) Create(ctx context.Context, userID, productID, clusterID int64, amount float64, currency string) (*model.Order, error) {
	return r.CreateWithPeriod(ctx, userID, productID, clusterID, amount, currency, model.BillingPeriodMonthly)
}

// CreateWithPeriod PLAN-054 / INFRA-013 新签名：显式传 period。L3-E 订单流改造时
// 调它替代 Create；旧调用方 Create() 自动落 'monthly'，行为不变。
func (r *OrderRepo) CreateWithPeriod(ctx context.Context, userID, productID, clusterID int64, amount float64, currency, period string) (*model.Order, error) {
	if currency == "" {
		currency = "USD"
	}
	if period == "" {
		period = model.BillingPeriodMonthly
	}
	expiresAt := time.Now().AddDate(0, 1, 0)
	var o model.Order
	row := r.db.QueryRowContext(ctx,
		`INSERT INTO orders (user_id, product_id, cluster_id, status, amount, currency, period, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+orderSelectCols,
		userID, productID, clusterID, model.OrderPending, amount, currency, period, expiresAt,
	)
	if err := scanOrderRow(row, &o); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	return &o, nil
}

func (r *OrderRepo) GetByID(ctx context.Context, id int64) (*model.Order, error) {
	var o model.Order
	row := r.db.QueryRowContext(ctx,
		`SELECT `+orderSelectCols+` FROM orders WHERE id = $1`, id)
	err := scanOrderRow(row, &o)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OrderRepo) ListByUser(ctx context.Context, userID int64) ([]model.Order, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+orderSelectCols+` FROM orders WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []model.Order
	for rows.Next() {
		var o model.Order
		if err := scanOrderRow(rows, &o); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (r *OrderRepo) ListAll(ctx context.Context) ([]model.Order, error) {
	orders, _, err := r.ListPaged(ctx, 0, 0)
	return orders, err
}

// ListPaged 返回全部订单的分页结果与过滤后总数。limit<=0 表示不限制。
func (r *OrderRepo) ListPaged(ctx context.Context, limit, offset int) ([]model.Order, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}

	query := `SELECT ` + orderSelectCols + ` FROM orders ORDER BY id DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	orders := make([]model.Order, 0)
	for rows.Next() {
		var o model.Order
		if err := scanOrderRow(rows, &o); err != nil {
			return nil, 0, err
		}
		orders = append(orders, o)
	}
	return orders, total, rows.Err()
}

func (r *OrderRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE orders SET status = $1, updated_at = $2 WHERE id = $3`, status, time.Now(), id)
	return err
}

// CancelIfPending 以条件 UPDATE 将订单置 cancelled，仅当当前 status=pending 且属于 userID 时生效。
// 返回 (是否改动, err)。与 PayWithBalance 的 FOR UPDATE 行锁天然互斥：Pay 持锁期间本语句会阻塞，
// Pay 提交后 status 已变为 paid，WHERE 条件不成立 → rows affected = 0，避免「扣款 + 订单取消」双写。
func (r *OrderRepo) CancelIfPending(ctx context.Context, orderID, userID int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE orders SET status = $1, updated_at = $2 WHERE id = $3 AND user_id = $4 AND status = $5`,
		model.OrderCancelled, time.Now(), orderID, userID, model.OrderPending,
	)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r *OrderRepo) PayWithBalance(ctx context.Context, orderID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var o model.Order
	err = tx.QueryRowContext(ctx,
		`SELECT id, user_id, amount, COALESCE(currency, 'USD'), status FROM orders WHERE id = $1 FOR UPDATE`, orderID,
	).Scan(&o.ID, &o.UserID, &o.Amount, &o.Currency, &o.Status)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	if o.Status != model.OrderPending {
		// 友好中文消息：常见于用户重复点击 Pay 或并发支付，让 toast 一眼看懂
		// （比技术词 "order not pending" 对终端用户更可读）。
		switch o.Status {
		case model.OrderPaid, model.OrderActive:
			return fmt.Errorf("订单已支付，请刷新页面查看 VM")
		case model.OrderCancelled:
			return fmt.Errorf("订单已取消，无法重复支付")
		default:
			return fmt.Errorf("订单状态异常（%s），请刷新页面", o.Status)
		}
	}

	var balance float64
	err = tx.QueryRowContext(ctx,
		`SELECT balance FROM users WHERE id = $1 FOR UPDATE`, o.UserID,
	).Scan(&balance)
	if err != nil {
		return fmt.Errorf("get balance: %w", err)
	}
	if balance < o.Amount {
		return fmt.Errorf("余额不足（需要 %.2f，当前 %.2f）", o.Amount, balance)
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE users SET balance = balance - $1, updated_at = $2 WHERE id = $3`, o.Amount, time.Now(), o.UserID)
	if err != nil {
		return fmt.Errorf("deduct balance: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE orders SET status = $1, updated_at = $2 WHERE id = $3`, model.OrderPaid, time.Now(), orderID)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO transactions (user_id, amount, type, description) VALUES ($1, $2, $3, $4)`,
		o.UserID, -o.Amount, "payment", fmt.Sprintf("订单 #%d 支付", orderID))
	if err != nil {
		return fmt.Errorf("record transaction: %w", err)
	}

	now := time.Now()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO invoices (order_id, user_id, amount, currency, status, due_at, paid_at) VALUES ($1, $2, $3, $4, 'paid', $5, $5)`,
		orderID, o.UserID, o.Amount, o.Currency, now)
	if err != nil {
		return fmt.Errorf("create invoice: %w", err)
	}

	return tx.Commit()
}
