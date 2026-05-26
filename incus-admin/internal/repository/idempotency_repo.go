package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// IdempotencyRepo PLAN-053 / INFRA-012 idempotency_keys 表读写。
//
// skeleton：方法签名稳定，middleware 接入留给 L3-H。
type IdempotencyRepo struct {
	db *sql.DB
}

func NewIdempotencyRepo(db *sql.DB) *IdempotencyRepo {
	return &IdempotencyRepo{db: db}
}

const idempotencySelectCols = `key, user_id, method, path, status_code, response_body, request_hash, created_at`

func scanIdempotency(row interface{ Scan(...any) error }, k *model.IdempotencyKey) error {
	return row.Scan(
		&k.Key, &k.UserID, &k.Method, &k.Path, &k.StatusCode, &k.ResponseBody, &k.RequestHash, &k.CreatedAt,
	)
}

// Get 拉一条缓存。未命中返 (nil, nil)。命中后由 middleware 比对 request_hash
// 决定是回放（hash 一致）还是返 422（同 key 异 payload）。
func (r *IdempotencyRepo) Get(ctx context.Context, key string) (*model.IdempotencyKey, error) {
	var k model.IdempotencyKey
	row := r.db.QueryRowContext(ctx,
		`SELECT `+idempotencySelectCols+` FROM idempotency_keys WHERE key = $1`, key)
	err := scanIdempotency(row, &k)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// Insert 写一条新缓存。并发命中会触发 PRIMARY KEY 冲突，调用方应当退回 Get
// 拿现存记录（PLAN-053 risk #2 对策）。
func (r *IdempotencyRepo) Insert(ctx context.Context, k *model.IdempotencyKey) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO idempotency_keys (key, user_id, method, path, status_code, response_body, request_hash)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		k.Key, k.UserID, k.Method, k.Path, k.StatusCode, k.ResponseBody, k.RequestHash,
	)
	if err != nil {
		return fmt.Errorf("insert idempotency: %w", err)
	}
	return nil
}

// DeleteOlderThan cleanup worker 调用，回收 24h 之前的缓存行；返回删除行数。
func (r *IdempotencyRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM idempotency_keys WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cleanup idempotency: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}
