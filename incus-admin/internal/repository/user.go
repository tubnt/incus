package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

type UserRepo struct {
	db *sql.DB
}

func NewUserRepo(db *sql.DB) *UserRepo {
	return &UserRepo{db: db}
}

func (r *UserRepo) FindOrCreate(ctx context.Context, email, name, logtoSub string, adminEmails []string) (*model.User, error) {
	var user model.User
	err := r.db.QueryRowContext(ctx,
		`SELECT id, email, name, role, COALESCE(logto_sub, ''), balance, created_at, updated_at FROM users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Role, &user.LogtoSub, &user.Balance, &user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		role := model.RoleCustomer
		for _, ae := range adminEmails {
			if ae == email {
				role = model.RoleAdmin
				break
			}
		}

		var subParam any
		if logtoSub != "" {
			subParam = logtoSub
		}
		err = r.db.QueryRowContext(ctx,
			`INSERT INTO users (email, name, role, logto_sub) VALUES ($1, $2, $3, $4)
			 RETURNING id, email, name, role, COALESCE(logto_sub, ''), balance, created_at, updated_at`,
			email, name, role, subParam,
		).Scan(&user.ID, &user.Email, &user.Name, &user.Role, &user.LogtoSub, &user.Balance, &user.CreatedAt, &user.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}

		_, err = r.db.ExecContext(ctx,
			`INSERT INTO quotas (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, user.ID)
		if err != nil {
			return nil, fmt.Errorf("create default quota: %w", err)
		}

		return &user, nil
	}

	if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}

	if logtoSub != "" && user.LogtoSub != logtoSub {
		r.db.ExecContext(ctx, `UPDATE users SET logto_sub = $1, updated_at = $2 WHERE id = $3`, logtoSub, time.Now(), user.ID)
		user.LogtoSub = logtoSub
	}

	return &user, nil
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	var user model.User
	err := r.db.QueryRowContext(ctx,
		`SELECT id, email, name, role, COALESCE(logto_sub, ''), balance, created_at, updated_at FROM users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Role, &user.LogtoSub, &user.Balance, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &user, nil
}

func (r *UserRepo) GetByID(ctx context.Context, id int64) (*model.User, error) {
	var user model.User
	err := r.db.QueryRowContext(ctx,
		`SELECT id, email, name, role, COALESCE(logto_sub, ''), balance, created_at, updated_at FROM users WHERE id = $1`,
		id,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Role, &user.LogtoSub, &user.Balance, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &user, nil
}

func (r *UserRepo) UpdateRole(ctx context.Context, id int64, role string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE users SET role = $1, updated_at = $2 WHERE id = $3`, role, time.Now(), id)
	return err
}

func (r *UserRepo) AdjustBalance(ctx context.Context, userID int64, amount float64, txType, desc string, createdBy *int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var newBalance float64
	err = tx.QueryRowContext(ctx,
		`UPDATE users SET balance = balance + $1, updated_at = $2 WHERE id = $3 RETURNING balance`,
		amount, time.Now(), userID,
	).Scan(&newBalance)
	if err != nil {
		return fmt.Errorf("adjust balance: %w", err)
	}

	if newBalance < 0 {
		return fmt.Errorf("insufficient balance")
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO transactions (user_id, amount, type, description, created_by) VALUES ($1, $2, $3, $4, $5)`,
		userID, amount, txType, desc, createdBy,
	)
	if err != nil {
		return fmt.Errorf("record transaction: %w", err)
	}

	return tx.Commit()
}

func (r *UserRepo) ListAll(ctx context.Context) ([]model.User, error) {
	users, _, err := r.ListPaged(ctx, 0, 0)
	return users, err
}

// TopUpWithDailyCap 在同一事务内对用户行加写锁，累加滚动窗口内的 deposit，
// 若不超过 cap 则同时写入 users.balance 与 transactions，从根本上避免
// "读 + 写"竞态导致轻微越限。ok=false 表示额度不足（err=nil），
// 此时事务已回滚、used 为当次查询到的窗口累计额。
func (r *UserRepo) TopUpWithDailyCap(
	ctx context.Context,
	userID int64,
	amount float64,
	description string,
	createdBy *int64,
	window time.Duration,
	cap float64,
) (used float64, newBalance float64, ok bool, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, false, err
	}
	defer tx.Rollback()

	// Row-level lock serialises concurrent TopUps for the same user within Postgres.
	var locked int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&locked); err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, false, fmt.Errorf("user %d not found", userID)
		}
		return 0, 0, false, fmt.Errorf("lock user: %w", err)
	}

	var sum sql.NullFloat64
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM transactions
		 WHERE user_id = $1 AND type = 'deposit' AND created_at >= $2`,
		userID, time.Now().Add(-window),
	).Scan(&sum)
	if err != nil {
		return 0, 0, false, fmt.Errorf("sum deposits: %w", err)
	}
	used = sum.Float64
	if used+amount > cap {
		return used, 0, false, nil
	}

	err = tx.QueryRowContext(ctx,
		`UPDATE users SET balance = balance + $1, updated_at = NOW() WHERE id = $2 RETURNING balance`,
		amount, userID,
	).Scan(&newBalance)
	if err != nil {
		return used, 0, false, fmt.Errorf("update balance: %w", err)
	}

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO transactions (user_id, amount, type, description, created_by)
		 VALUES ($1, $2, 'deposit', $3, $4)`,
		userID, amount, description, createdBy,
	); err != nil {
		return used, 0, false, fmt.Errorf("record transaction: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return used, 0, false, fmt.Errorf("commit: %w", err)
	}
	return used, newBalance, true, nil
}

// SumDepositsSince 汇总指定用户自 since 起的 deposit 流水金额，
// 用于 TopUp 日额度校验。无记录时返回 0。
func (r *UserRepo) SumDepositsSince(ctx context.Context, userID int64, since time.Time) (float64, error) {
	var sum sql.NullFloat64
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM transactions
		 WHERE user_id = $1 AND type = 'deposit' AND created_at >= $2`,
		userID, since,
	).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return sum.Float64, nil
}

// ListPaged 返回分页后的用户列表以及过滤后的总数。
// limit<=0 表示不限制（保持与 ListAll 等价）。
func (r *UserRepo) ListPaged(ctx context.Context, limit, offset int) ([]model.User, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	query := `SELECT id, email, name, role, COALESCE(logto_sub, ''), balance, created_at, updated_at FROM users ORDER BY id`
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

	users := make([]model.User, 0)
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.LogtoSub, &u.Balance, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}
