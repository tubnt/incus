package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

type InvoiceRepo struct {
	db *sql.DB
}

func NewInvoiceRepo(db *sql.DB) *InvoiceRepo {
	return &InvoiceRepo{db: db}
}

func (r *InvoiceRepo) Create(ctx context.Context, orderID, userID int64, amount float64, currency string) (*model.Invoice, error) {
	if currency == "" {
		currency = "USD"
	}
	now := time.Now()
	var inv model.Invoice
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO invoices (order_id, user_id, amount, currency, status, due_at, paid_at)
		 VALUES ($1, $2, $3, $4, 'paid', $5, $5)
		 RETURNING id, order_id, user_id, amount, COALESCE(currency, 'USD'), status, due_at, paid_at, created_at`,
		orderID, userID, amount, currency, now,
	).Scan(&inv.ID, &inv.OrderID, &inv.UserID, &inv.Amount, &inv.Currency, &inv.Status, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create invoice: %w", err)
	}
	return &inv, nil
}

func (r *InvoiceRepo) ListByUser(ctx context.Context, userID int64) ([]model.Invoice, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, order_id, user_id, amount, COALESCE(currency, 'USD'), status, due_at, paid_at, created_at FROM invoices WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invoices []model.Invoice
	for rows.Next() {
		var inv model.Invoice
		if err := rows.Scan(&inv.ID, &inv.OrderID, &inv.UserID, &inv.Amount, &inv.Currency, &inv.Status, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invoices = append(invoices, inv)
	}
	return invoices, rows.Err()
}

func (r *InvoiceRepo) ListAll(ctx context.Context) ([]model.Invoice, error) {
	invoices, _, err := r.ListPaged(ctx, 0, 0)
	return invoices, err
}

// ListPaged 返回全部发票的分页结果与过滤后总数。limit<=0 表示不限制。
func (r *InvoiceRepo) ListPaged(ctx context.Context, limit, offset int) ([]model.Invoice, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count invoices: %w", err)
	}

	query := `SELECT id, order_id, user_id, amount, COALESCE(currency, 'USD'), status, due_at, paid_at, created_at FROM invoices ORDER BY id DESC`
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

	invoices := make([]model.Invoice, 0)
	for rows.Next() {
		var inv model.Invoice
		if err := rows.Scan(&inv.ID, &inv.OrderID, &inv.UserID, &inv.Amount, &inv.Currency, &inv.Status, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt); err != nil {
			return nil, 0, err
		}
		invoices = append(invoices, inv)
	}
	return invoices, total, rows.Err()
}
