package repository

import (
	"context"
	"database/sql"
	"fmt"
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
		`SELECT id, name, cluster_id, user_id, order_id, ip, status, cpu, memory_mb, disk_gb, os_image, node, password, created_at, updated_at
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
		`SELECT id, name, cluster_id, user_id, order_id, ip, status, cpu, memory_mb, disk_gb, os_image, node, password, created_at, updated_at
		 FROM vms WHERE user_id = $1 AND status != 'deleted' ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVMs(rows)
}

func (r *VMRepo) ListAll(ctx context.Context) ([]model.VM, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, ip, status, cpu, memory_mb, disk_gb, os_image, node, password, created_at, updated_at
		 FROM vms WHERE status != 'deleted' ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVMs(rows)
}

func (r *VMRepo) GetByName(ctx context.Context, name string) (*model.VM, error) {
	var vm model.VM
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, cluster_id, user_id, status, cpu, memory_mb, disk_gb, os_image, node, password, created_at, updated_at
		 FROM vms WHERE name = $1 AND status != 'deleted' LIMIT 1`, name,
	).Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password, &vm.CreatedAt, &vm.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vm by name: %w", err)
	}
	return &vm, nil
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
