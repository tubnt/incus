package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

type APITokenRepo struct {
	db *sql.DB
}

func NewAPITokenRepo(db *sql.DB) *APITokenRepo {
	return &APITokenRepo{db: db}
}

func (r *APITokenRepo) Create(ctx context.Context, userID int64, name string, expiresAt *time.Time) (*model.APIToken, error) {
	raw := make([]byte, 32)
	rand.Read(raw)
	token := "ica_" + hex.EncodeToString(raw)
	hash := sha256Hash(token)

	var t model.APIToken
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, expires_at) VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, name, token_hash, last_used_at, expires_at, created_at`,
		userID, name, hash, expiresAt,
	).Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create token: %w", err)
	}
	t.Token = token
	return &t, nil
}

func (r *APITokenRepo) ListByUser(ctx context.Context, userID int64) ([]model.APIToken, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, name, last_used_at, expires_at, created_at FROM api_tokens WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []model.APIToken
	for rows.Next() {
		var t model.APIToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

func (r *APITokenRepo) Delete(ctx context.Context, id, userID int64) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("token not found")
	}
	return nil
}

func (r *APITokenRepo) ValidateToken(ctx context.Context, token string) (*model.APIToken, error) {
	hash := sha256Hash(token)
	var t model.APIToken
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, expires_at, created_at FROM api_tokens WHERE token_hash = $1`, hash,
	).Scan(&t.ID, &t.UserID, &t.Name, &t.ExpiresAt, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now()) {
		return nil, nil
	}
	_, _ = r.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = $1 WHERE id = $2`, time.Now(), t.ID)
	return &t, nil
}

// Renew atomically invalidates the old token row (expires_at = NOW()) and
// inserts a new one inheriting the original name. The old row is kept so
// audit trails retain the relationship; the retention worker cleans it up
// after the grace period. ttl must be positive.
func (r *APITokenRepo) Renew(ctx context.Context, oldID, userID int64, ttl time.Duration) (*model.APIToken, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Lock + validate old row belongs to this user and isn't already expired.
	var name string
	var oldExp sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT name, expires_at FROM api_tokens WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		oldID, userID,
	).Scan(&name, &oldExp)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("token not found")
	}
	if err != nil {
		return nil, fmt.Errorf("lock token: %w", err)
	}
	if oldExp.Valid && oldExp.Time.Before(time.Now()) {
		return nil, fmt.Errorf("token already expired; create a new one instead")
	}

	// Invalidate the old row; the existing hash stays in place so leaked
	// tokens can still be traced in audit but ValidateToken rejects them.
	if _, err := tx.ExecContext(ctx,
		`UPDATE api_tokens SET expires_at = NOW() WHERE id = $1`, oldID,
	); err != nil {
		return nil, fmt.Errorf("invalidate old token: %w", err)
	}

	// Mint the new one inheriting the name + user.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	newToken := "ica_" + hex.EncodeToString(raw)
	hash := sha256Hash(newToken)
	newExpiresAt := time.Now().Add(ttl)

	var t model.APIToken
	err = tx.QueryRowContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, expires_at) VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, name, token_hash, last_used_at, expires_at, created_at`,
		userID, name, hash, newExpiresAt,
	).Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert new token: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	t.Token = newToken
	return &t, nil
}

// DeleteExpiredBefore removes rows whose expires_at is set and already past
// the given cutoff. Called by the cleanup worker with cutoff = NOW() - grace
// period so recently invalidated tokens stay for audit purposes.
func (r *APITokenRepo) DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE expires_at IS NOT NULL AND expires_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
