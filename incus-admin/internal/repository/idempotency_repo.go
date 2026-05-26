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
// 与 middleware.IdempotencyStore 接口签名对齐：Get/Put/DeleteOlderThan。
// PRIMARY KEY (key) 已天然唯一，但 Get 仍把 user_id 进 WHERE，防止恶意用户
// 通过预测 key 观察他人缓存响应（cross-user replay 防护）。
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
//
// userID 进 WHERE 是 cross-user 防护：A 用户的 key 在 B 用户 ctx 下查不到，
// 也就无法观察到 A 的缓存响应体。
func (r *IdempotencyRepo) Get(ctx context.Context, key string, userID int64) (*model.IdempotencyKey, error) {
	var k model.IdempotencyKey
	row := r.db.QueryRowContext(ctx,
		`SELECT `+idempotencySelectCols+` FROM idempotency_keys WHERE key = $1 AND user_id = $2`,
		key, userID)
	err := scanIdempotency(row, &k)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// Put 写一条新缓存。ON CONFLICT (key) DO NOTHING 保证 race 场景下先到先得，
// 第二个写入不抛错也不覆盖（PLAN-053 risk #2 对策）。调用方关心后续重放时
// 是否读到自己写的那条，直接 Get 即可。
func (r *IdempotencyRepo) Put(ctx context.Context, k model.IdempotencyKey) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO idempotency_keys (key, user_id, method, path, status_code, response_body, request_hash)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (key) DO NOTHING`,
		k.Key, k.UserID, k.Method, k.Path, k.StatusCode, k.ResponseBody, k.RequestHash,
	)
	if err != nil {
		return fmt.Errorf("put idempotency: %w", err)
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
