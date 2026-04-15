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
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, email, name, role, COALESCE(logto_sub, ''), balance, created_at, updated_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.LogtoSub, &u.Balance, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
