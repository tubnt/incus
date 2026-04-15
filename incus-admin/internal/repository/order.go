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

func (r *OrderRepo) Create(ctx context.Context, userID, productID, clusterID int64, amount float64) (*model.Order, error) {
	expiresAt := time.Now().AddDate(0, 1, 0)
	var o model.Order
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO orders (user_id, product_id, cluster_id, status, amount, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, user_id, product_id, cluster_id, status, amount, expires_at, created_at`,
		userID, productID, clusterID, model.OrderPending, amount, expiresAt,
	).Scan(&o.ID, &o.UserID, &o.ProductID, &o.ClusterID, &o.Status, &o.Amount, &o.ExpiresAt, &o.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	return &o, nil
}

func (r *OrderRepo) GetByID(ctx context.Context, id int64) (*model.Order, error) {
	var o model.Order
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, product_id, cluster_id, status, amount, expires_at, created_at FROM orders WHERE id = $1`, id,
	).Scan(&o.ID, &o.UserID, &o.ProductID, &o.ClusterID, &o.Status, &o.Amount, &o.ExpiresAt, &o.CreatedAt)
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
		`SELECT id, user_id, product_id, cluster_id, status, amount, expires_at, created_at FROM orders WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []model.Order
	for rows.Next() {
		var o model.Order
		if err := rows.Scan(&o.ID, &o.UserID, &o.ProductID, &o.ClusterID, &o.Status, &o.Amount, &o.ExpiresAt, &o.CreatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (r *OrderRepo) ListAll(ctx context.Context) ([]model.Order, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, product_id, cluster_id, status, amount, expires_at, created_at FROM orders ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []model.Order
	for rows.Next() {
		var o model.Order
		if err := rows.Scan(&o.ID, &o.UserID, &o.ProductID, &o.ClusterID, &o.Status, &o.Amount, &o.ExpiresAt, &o.CreatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (r *OrderRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE orders SET status = $1, updated_at = $2 WHERE id = $3`, status, time.Now(), id)
	return err
}

func (r *OrderRepo) PayWithBalance(ctx context.Context, orderID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var o model.Order
	err = tx.QueryRowContext(ctx,
		`SELECT id, user_id, amount, status FROM orders WHERE id = $1 FOR UPDATE`, orderID,
	).Scan(&o.ID, &o.UserID, &o.Amount, &o.Status)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	if o.Status != model.OrderPending {
		return fmt.Errorf("order not pending")
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
		`INSERT INTO invoices (order_id, user_id, amount, status, due_at, paid_at) VALUES ($1, $2, $3, 'paid', $4, $4)`,
		orderID, o.UserID, o.Amount, now)
	if err != nil {
		return fmt.Errorf("create invoice: %w", err)
	}

	return tx.Commit()
}
