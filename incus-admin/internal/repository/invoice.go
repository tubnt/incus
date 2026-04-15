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

func (r *InvoiceRepo) Create(ctx context.Context, orderID, userID int64, amount float64) (*model.Invoice, error) {
	now := time.Now()
	var inv model.Invoice
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO invoices (order_id, user_id, amount, status, due_at, paid_at)
		 VALUES ($1, $2, $3, 'paid', $4, $4)
		 RETURNING id, order_id, user_id, amount, status, due_at, paid_at, created_at`,
		orderID, userID, amount, now,
	).Scan(&inv.ID, &inv.OrderID, &inv.UserID, &inv.Amount, &inv.Status, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create invoice: %w", err)
	}
	return &inv, nil
}

func (r *InvoiceRepo) ListByUser(ctx context.Context, userID int64) ([]model.Invoice, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, order_id, user_id, amount, status, due_at, paid_at, created_at FROM invoices WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invoices []model.Invoice
	for rows.Next() {
		var inv model.Invoice
		if err := rows.Scan(&inv.ID, &inv.OrderID, &inv.UserID, &inv.Amount, &inv.Status, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invoices = append(invoices, inv)
	}
	return invoices, rows.Err()
}

func (r *InvoiceRepo) ListAll(ctx context.Context) ([]model.Invoice, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, order_id, user_id, amount, status, due_at, paid_at, created_at FROM invoices ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invoices []model.Invoice
	for rows.Next() {
		var inv model.Invoice
		if err := rows.Scan(&inv.ID, &inv.OrderID, &inv.UserID, &inv.Amount, &inv.Status, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invoices = append(invoices, inv)
	}
	return invoices, rows.Err()
}
