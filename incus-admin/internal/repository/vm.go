package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

type VMRepo struct {
	db *sql.DB
}

func NewVMRepo(db *sql.DB) *VMRepo {
	return &VMRepo{db: db}
}

func (r *VMRepo) Create(ctx context.Context, vm *model.VM) error {
	return r.db.QueryRowContext(ctx,
		`INSERT INTO vms (name, cluster_id, user_id, order_id, ip, status, cpu, memory_mb, disk_gb, os_image, node, password)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id, created_at, updated_at`,
		vm.Name, vm.ClusterID, vm.UserID, vm.OrderID, vm.IP,
		vm.Status, vm.CPU, vm.MemoryMB, vm.DiskGB, vm.OSImage, vm.Node, vm.Password,
	).Scan(&vm.ID, &vm.CreatedAt, &vm.UpdatedAt)
}

func (r *VMRepo) GetByID(ctx context.Context, id int64) (*model.VM, error) {
	var vm model.VM
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, created_at, updated_at
		 FROM vms WHERE id = $1`, id,
	).Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.OrderID, &vm.IP,
		&vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password,
		&vm.CreatedAt, &vm.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &vm, err
}

func (r *VMRepo) ListByUser(ctx context.Context, userID int64) ([]model.VM, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, created_at, updated_at
		 FROM vms WHERE user_id = $1 AND status != 'deleted' ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVMs(rows)
}

func (r *VMRepo) ListAll(ctx context.Context) ([]model.VM, error) {
	vms, _, err := r.ListPaged(ctx, 0, 0)
	return vms, err
}

// ListPaged 返回非删除态 VM 的分页结果与过滤后总数。limit<=0 表示不限制。
func (r *VMRepo) ListPaged(ctx context.Context, limit, offset int) ([]model.VM, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vms WHERE status != 'deleted'`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count vms: %w", err)
	}

	query := `SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, created_at, updated_at
		 FROM vms WHERE status != 'deleted' ORDER BY id DESC`
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
	vms, err := scanVMs(rows)
	if err != nil {
		return nil, 0, err
	}
	return vms, total, nil
}

func (r *VMRepo) GetByName(ctx context.Context, name string) (*model.VM, error) {
	var vm model.VM
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, created_at, updated_at
		 FROM vms WHERE name = $1 AND status != 'deleted' LIMIT 1`, name,
	).Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.OrderID, &vm.IP, &vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password, &vm.CreatedAt, &vm.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vm by name: %w", err)
	}
	return &vm, nil
}

// IPsByNames returns a name → ip map for the given VM names (non-deleted rows only).
// Used by admin cluster listings to enrich Incus payloads with DB-recorded IPs.
func (r *VMRepo) IPsByNames(ctx context.Context, names []string) (map[string]string, error) {
	result := map[string]string{}
	if len(names) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(names))
	args := make([]any, len(names))
	for i, n := range names {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = n
	}
	q := `SELECT name, host(ip)::text FROM vms
	      WHERE name IN (` + strings.Join(placeholders, ",") + `)
	        AND status != 'deleted' AND ip IS NOT NULL`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ips by names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, ip string
		if err := rows.Scan(&name, &ip); err != nil {
			return nil, err
		}
		if ip != "" {
			result[name] = ip
		}
	}
	return result, rows.Err()
}

func (r *VMRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now(), id)
	return err
}

func (r *VMRepo) UpdatePassword(ctx context.Context, id int64, password string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET password = $1, updated_at = $2 WHERE id = $3`,
		password, time.Now(), id)
	return err
}

func (r *VMRepo) UpdateNode(ctx context.Context, id int64, node string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET node = $1, updated_at = $2 WHERE id = $3`,
		node, time.Now(), id)
	return err
}

func (r *VMRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET status = 'deleted', updated_at = $1 WHERE id = $2`,
		time.Now(), id)
	return err
}

func (r *VMRepo) CountByUser(ctx context.Context, userID int64) (vms int, vcpus int, ramMB int, diskGB int, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(cpu),0), COALESCE(SUM(memory_mb),0), COALESCE(SUM(disk_gb),0)
		 FROM vms WHERE user_id = $1 AND status NOT IN ('deleted','error')`, userID,
	).Scan(&vms, &vcpus, &ramMB, &diskGB)
	return
}

// CountRunningByCluster returns how many VMs the DB believes are running for a cluster.
// Used by the admin monitoring page to distinguish "no VMs at all" from "DB/Incus drift".
func (r *VMRepo) CountRunningByCluster(ctx context.Context, clusterID int64) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vms WHERE cluster_id = $1 AND status = 'running'`, clusterID,
	).Scan(&n)
	return n, err
}

func scanVMs(rows *sql.Rows) ([]model.VM, error) {
	var vms []model.VM
	for rows.Next() {
		var vm model.VM
		if err := rows.Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.OrderID, &vm.IP,
			&vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password,
			&vm.CreatedAt, &vm.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan vm: %w", err)
		}
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}
