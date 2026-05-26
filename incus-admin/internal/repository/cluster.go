package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/incuscloud/incus-admin/internal/model"
)

type ClusterRepo struct {
	db *sql.DB
}

func NewClusterRepo(db *sql.DB) *ClusterRepo {
	return &ClusterRepo{db: db}
}

// Upsert inserts a cluster if missing, otherwise updates display_name / api_url /
// status keyed by name. Returns the resulting row's ID.
func (r *ClusterRepo) Upsert(ctx context.Context, name, displayName, apiURL string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO clusters (name, display_name, api_url, status)
		 VALUES ($1, $2, $3, 'active')
		 ON CONFLICT (name) DO UPDATE
		   SET display_name = EXCLUDED.display_name,
		       api_url      = EXCLUDED.api_url,
		       status       = 'active',
		       updated_at   = NOW()
		 RETURNING id`, name, displayName, apiURL).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert cluster %q: %w", name, err)
	}
	return id, nil
}

// 基础 SELECT 列集合（GetByName / GetByID / List 共用）。
// 含 PLAN-053 Phase C 的 country/city/region_status/capabilities，
// 由 scanBase + finalizeRegion 负责把 NULL → 默认值。
const clusterBaseColumns = `id, name, display_name, api_url, status,
	country, city,
	COALESCE(region_status, 'available') AS region_status,
	COALESCE(capabilities, '["instances"]'::jsonb) AS capabilities,
	created_at, updated_at`

// scanBase 把基础列填进 c，并把 capabilities 字节流 unmarshal 到 c.Capabilities。
// country / city 用 sql.NullString 兜底 NULL，避免 Cluster.Country 拿到 "(null)".
func (r *ClusterRepo) scanBase(rs interface{ Scan(...any) error }, c *model.Cluster) error {
	var country, city sql.NullString
	if err := rs.Scan(
		&c.ID, &c.Name, &c.DisplayName, &c.APIURL, &c.Status,
		&country, &city,
		&c.RegionStatus,
		&c.CapabilitiesJSON,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return err
	}
	c.Country = country.String
	c.City = city.String
	finalizeRegion(c)
	return nil
}

// finalizeRegion 把 CapabilitiesJSON 反序列化到 Capabilities，
// 失败 / 空 / 非数组都回退到 DefaultClusterCapabilities 副本（避免别名共享）。
func finalizeRegion(c *model.Cluster) {
	if c.RegionStatus == "" {
		c.RegionStatus = model.RegionStatusAvailable
	}
	c.Capabilities = nil
	if len(c.CapabilitiesJSON) > 0 {
		var caps []string
		if err := json.Unmarshal(c.CapabilitiesJSON, &caps); err == nil && caps != nil {
			c.Capabilities = caps
		}
	}
	if c.Capabilities == nil {
		c.Capabilities = append([]string{}, model.DefaultClusterCapabilities...)
	}
}

func (r *ClusterRepo) GetByName(ctx context.Context, name string) (*model.Cluster, error) {
	var c model.Cluster
	row := r.db.QueryRowContext(ctx,
		`SELECT `+clusterBaseColumns+` FROM clusters WHERE name = $1`, name)
	if err := r.scanBase(row, &c); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *ClusterRepo) GetByID(ctx context.Context, id int64) (*model.Cluster, error) {
	var c model.Cluster
	row := r.db.QueryRowContext(ctx,
		`SELECT `+clusterBaseColumns+` FROM clusters WHERE id = $1`, id)
	if err := r.scanBase(row, &c); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// GetTLSFingerprint reads the stored SPKI sha256 pin (hex) for a cluster.
// Returns "" if the row is absent or the column is NULL.
func (r *ClusterRepo) GetTLSFingerprint(ctx context.Context, name string) (string, error) {
	var fp sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT tls_fingerprint FROM clusters WHERE name = $1`, name,
	).Scan(&fp)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !fp.Valid {
		return "", nil
	}
	return fp.String, nil
}

// SetTLSFingerprint writes back the learned pin during trust-on-first-use.
// Overwriting an existing pin is explicitly allowed so the reset-fingerprint
// admin action can rotate the value; callers must audit first.
func (r *ClusterRepo) SetTLSFingerprint(ctx context.Context, name, fingerprint string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE clusters SET tls_fingerprint = $1, updated_at = NOW() WHERE name = $2`,
		fingerprint, name,
	)
	return err
}

func (r *ClusterRepo) List(ctx context.Context) ([]model.Cluster, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+clusterBaseColumns+` FROM clusters ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Cluster
	for rows.Next() {
		var c model.Cluster
		if err := r.scanBase(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PLAN-027 / INFRA-003：完整 cluster 配置 CRUD。

// ListFull 返回所有 clusters 行的完整配置（含 cert/key/ca path、ip_pools_json、kind、
// PLAN-053 region metadata）。main.go 启动时调用，把 DB 内容转 ClusterConfig
// 喂给 cluster.Manager。
func (r *ClusterRepo) ListFull(ctx context.Context) ([]model.Cluster, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(display_name,''), api_url, status,
		        COALESCE(kind,'cluster'),
		        COALESCE(cert_file,''), COALESCE(key_file,''), COALESCE(ca_file,''),
		        COALESCE(default_project,''), COALESCE(storage_pool,''), COALESCE(network,''),
		        COALESCE(ip_pools_json::text,''),
		        country, city,
		        COALESCE(region_status, 'available'),
		        COALESCE(capabilities, '["instances"]'::jsonb),
		        created_at, updated_at
		 FROM clusters ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Cluster
	for rows.Next() {
		var c model.Cluster
		var country, city sql.NullString
		if err := rows.Scan(
			&c.ID, &c.Name, &c.DisplayName, &c.APIURL, &c.Status,
			&c.Kind,
			&c.CertFile, &c.KeyFile, &c.CAFile,
			&c.DefaultProject, &c.StoragePool, &c.Network,
			&c.IPPoolsJSON,
			&country, &city,
			&c.RegionStatus,
			&c.CapabilitiesJSON,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, err
		}
		c.Country = country.String
		c.City = city.String
		finalizeRegion(&c)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateFull 持久化一个完整 cluster / standalone host 配置。kind 缺省 'cluster'。
// ip_pools 为 nil 写 NULL；非 nil 序列化为 JSONB。
//
// 不在这里 SET region metadata（country/city/region_status/capabilities）——
// 留给后续的 admin UI region 编辑入口（PLAN-053 后续 task）。新行的默认值
// 由 migration 027 提供（country/city NULL, region_status='available',
// capabilities='["instances"]'）。
//
// 与 Upsert 区别：CreateFull 走 INSERT ... ON CONFLICT UPDATE 写完整字段；
// Upsert 仅更新基础字段（保留旧的 cert/kind 不变）—— Upsert 给 env bootstrap
// 用，CreateFull 给 admin UI add 用。
func (r *ClusterRepo) CreateFull(ctx context.Context, c *model.Cluster, ipPools any) (int64, error) {
	kind := c.Kind
	if kind == "" {
		kind = model.ClusterKindCluster
	}
	var poolsJSON sql.NullString
	if ipPools != nil {
		buf, err := json.Marshal(ipPools)
		if err != nil {
			return 0, fmt.Errorf("marshal ip_pools: %w", err)
		}
		poolsJSON = sql.NullString{String: string(buf), Valid: true}
	}

	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO clusters
		   (name, display_name, api_url, status, kind,
		    cert_file, key_file, ca_file, default_project, storage_pool, network, ip_pools_json)
		 VALUES ($1, $2, $3, 'active', $4, $5, $6, $7, $8, $9, $10, $11::jsonb)
		 ON CONFLICT (name) DO UPDATE SET
		   display_name    = EXCLUDED.display_name,
		   api_url         = EXCLUDED.api_url,
		   status          = 'active',
		   kind            = EXCLUDED.kind,
		   cert_file       = EXCLUDED.cert_file,
		   key_file        = EXCLUDED.key_file,
		   ca_file         = EXCLUDED.ca_file,
		   default_project = EXCLUDED.default_project,
		   storage_pool    = EXCLUDED.storage_pool,
		   network         = EXCLUDED.network,
		   ip_pools_json   = EXCLUDED.ip_pools_json,
		   updated_at      = NOW()
		 RETURNING id`,
		c.Name, c.DisplayName, c.APIURL, kind,
		c.CertFile, c.KeyFile, c.CAFile,
		c.DefaultProject, c.StoragePool, c.Network,
		poolsJSON,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert cluster %q: %w", c.Name, err)
	}
	return id, nil
}

// DeleteByName 删除 clusters 行。返回影响行数；0 表示不存在。
func (r *ClusterRepo) DeleteByName(ctx context.Context, name string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM clusters WHERE name = $1`, name,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
