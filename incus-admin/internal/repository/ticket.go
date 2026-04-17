package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

type TicketRepo struct {
	db *sql.DB
}

func NewTicketRepo(db *sql.DB) *TicketRepo {
	return &TicketRepo{db: db}
}

func (r *TicketRepo) ListByUser(ctx context.Context, userID int64) ([]model.Ticket, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, subject, status, priority, created_at, updated_at FROM tickets WHERE user_id = $1 ORDER BY updated_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickets []model.Ticket
	for rows.Next() {
		var t model.Ticket
		if err := rows.Scan(&t.ID, &t.UserID, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tickets = append(tickets, t)
	}
	return tickets, rows.Err()
}

func (r *TicketRepo) ListAll(ctx context.Context) ([]model.Ticket, error) {
	tickets, _, err := r.ListPaged(ctx, 0, 0)
	return tickets, err
}

// ListPaged 返回全部工单的分页结果与过滤后总数。limit<=0 表示不限制。
func (r *TicketRepo) ListPaged(ctx context.Context, limit, offset int) ([]model.Ticket, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tickets: %w", err)
	}

	query := `SELECT t.id, t.user_id, t.subject, t.status, t.priority, t.created_at, t.updated_at FROM tickets t ORDER BY t.updated_at DESC`
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

	tickets := make([]model.Ticket, 0)
	for rows.Next() {
		var t model.Ticket
		if err := rows.Scan(&t.ID, &t.UserID, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, 0, err
		}
		tickets = append(tickets, t)
	}
	return tickets, total, rows.Err()
}

func (r *TicketRepo) GetByID(ctx context.Context, id int64) (*model.Ticket, error) {
	var t model.Ticket
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, subject, status, priority, created_at, updated_at FROM tickets WHERE id = $1`, id,
	).Scan(&t.ID, &t.UserID, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TicketRepo) Create(ctx context.Context, userID int64, subject, priority string) (*model.Ticket, error) {
	var t model.Ticket
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO tickets (user_id, subject, priority) VALUES ($1, $2, $3)
		 RETURNING id, user_id, subject, status, priority, created_at, updated_at`,
		userID, subject, priority,
	).Scan(&t.ID, &t.UserID, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create ticket: %w", err)
	}
	return &t, nil
}

func (r *TicketRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tickets SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now(), id)
	return err
}

func (r *TicketRepo) ListMessages(ctx context.Context, ticketID int64) ([]model.TicketMessage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, ticket_id, user_id, body, is_staff, created_at FROM ticket_messages WHERE ticket_id = $1 ORDER BY created_at ASC`,
		ticketID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []model.TicketMessage
	for rows.Next() {
		var m model.TicketMessage
		if err := rows.Scan(&m.ID, &m.TicketID, &m.UserID, &m.Body, &m.IsStaff, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (r *TicketRepo) AddMessage(ctx context.Context, ticketID, userID int64, body string, isStaff bool) (*model.TicketMessage, error) {
	var m model.TicketMessage
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO ticket_messages (ticket_id, user_id, body, is_staff) VALUES ($1, $2, $3, $4)
		 RETURNING id, ticket_id, user_id, body, is_staff, created_at`,
		ticketID, userID, body, isStaff,
	).Scan(&m.ID, &m.TicketID, &m.UserID, &m.Body, &m.IsStaff, &m.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("add message: %w", err)
	}

	r.db.ExecContext(ctx, `UPDATE tickets SET updated_at = $1 WHERE id = $2`, time.Now(), ticketID)
	return &m, nil
}
