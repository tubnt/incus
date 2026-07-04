package repository

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/auth"
	"github.com/incuscloud/incus-admin/internal/model"
)

type VMRepo struct {
	db *sql.DB
}

func NewVMRepo(db *sql.DB) *VMRepo {
	return &VMRepo{db: db}
}

// productionEnv 由 main.go 在启动时注入（默认空 = test/dev）。Session-1 W3+ /
// PLAN-051 D-02：production 时加密失败必须 fail-closed（与 UpdatePassword 对称），
// 不再 silent fallback 明文；非 production 维持 passthrough 兼容老部署。
var productionEnv string

// SetEnvForTests 仅用于测试 / 启动注入。线程安全只在启动期一次性赋值后生效。
func SetEnv(env string) { productionEnv = strings.ToLower(strings.TrimSpace(env)) }

// encryptForWrite 把内存里的明文密码加密成 v1:... 形式给 INSERT/UPDATE 用。
// nil → nil；空 *string → 空 *string。
//   - production 加密失败 → 返 nil + slog.Error（调用方拿到 NULL；vm row 无密码
//     比明文落库安全，UI/SecretReveal 已对 nil 有 placeholder）
//   - 非 production 加密失败 → silent fallback 原明文（旧 dev 行为）
//
// OPS-022：encryption disabled 时 passthrough 原样返回。
func encryptForWrite(p *string) *string {
	if p == nil {
		return nil
	}
	if *p == "" {
		return p
	}
	enc, err := auth.EncryptPassword(*p)
	if err != nil {
		if productionEnv == "production" {
			slog.Error("vm password encrypt failed in production; storing NULL to avoid plaintext leak", "error", err)
			return nil
		}
		slog.Warn("vm password encrypt failed; storing plaintext (dev/test mode)", "error", err)
		return p
	}
	cp := enc
	return &cp
}

// decryptOnRead 反向：DB 读出的 *string 如果是 v1:... 解密。失败 → 返回特殊
// sentinel "<DECRYPT_FAILED>"，让 admin UI 区分 "未设密码" 与 "密钥轮换/损坏"。
// portal SecretReveal 看到该 sentinel 时按"未设"展示（不透露给最终用户）。
//
// Session-2 F-45 / PLAN-051 §2-E：原版返 nil 时 admin 与 portal 看到同样空，
// 无法分辨 vm 真没密码 vs 解密失败。
const PasswordDecryptFailed = "<DECRYPT_FAILED>"

func decryptOnRead(p *string) *string {
	if p == nil || *p == "" {
		return p
	}
	dec, err := auth.DecryptPassword(*p)
	if err != nil {
		slog.Warn("vm password decrypt failed; returning sentinel", "error", err)
		s := PasswordDecryptFailed
		return &s
	}
	cp := dec
	return &cp
}

func (r *VMRepo) Create(ctx context.Context, vm *model.VM) error {
	encPwd := encryptForWrite(vm.Password) // OPS-022
	return r.db.QueryRowContext(ctx,
		`INSERT INTO vms (name, cluster_id, user_id, order_id, ip, status, cpu, memory_mb, disk_gb, os_image, node, password)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id, created_at, updated_at`,
		vm.Name, vm.ClusterID, vm.UserID, vm.OrderID, vm.IP,
		vm.Status, vm.CPU, vm.MemoryMB, vm.DiskGB, vm.OSImage, vm.Node, encPwd,
	).Scan(&vm.ID, &vm.CreatedAt, &vm.UpdatedAt)
}

func (r *VMRepo) GetByID(ctx context.Context, id int64) (*model.VM, error) {
	var vm model.VM
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms WHERE id = $1`, id,
	).Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.OrderID, &vm.IP,
		&vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password,
		&vm.RescueState, &vm.RescueStartedAt, &vm.RescueSnapshotName,
		&vm.TrashedAt, &vm.TrashedPrevStatus,
		&vm.CreatedAt, &vm.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err == nil {
		vm.Password = decryptOnRead(vm.Password) // OPS-022
	}
	return &vm, err
}

func (r *VMRepo) ListByUser(ctx context.Context, userID int64) ([]model.VM, error) {
	rows, err := r.db.QueryContext(ctx,
		// Users never need to see `gone` rows — the instance is unreachable
		// and they can't act on it. Admins see them via the admin list page
		// (ListPaged) so they can force-delete.
		// Trashed rows are also filtered — user-visible recycling is a separate
		// query (ListMyTrashed) so the main list stays clean during the 30s window.
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms WHERE user_id = $1 AND status NOT IN ('deleted','gone') AND trashed_at IS NULL ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVMs(rows)
}

// ListByUserPaged 返回某用户的可见 VM 分页结果与过滤后总数（PLAN-053 Phase B /v1/instances 用）。
// 过滤逻辑与 ListByUser 对齐：排除 deleted/gone + trashed，避免用户在 cloud-gateway
// /v1/instances 里看到回收站 / 已删 VM。limit<=0 表示不限制。
func (r *VMRepo) ListByUserPaged(ctx context.Context, userID int64, limit, offset int) ([]model.VM, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vms
		 WHERE user_id = $1 AND status NOT IN ('deleted','gone') AND trashed_at IS NULL`,
		userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count vms by user: %w", err)
	}

	query := `SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms WHERE user_id = $1 AND status NOT IN ('deleted','gone') AND trashed_at IS NULL ORDER BY id DESC`
	args := []any{userID}
	if limit > 0 {
		query += ` LIMIT $2 OFFSET $3`
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
	if vms == nil {
		vms = []model.VM{}
	}
	return vms, total, nil
}

func (r *VMRepo) ListAll(ctx context.Context) ([]model.VM, error) {
	vms, _, err := r.ListPaged(ctx, 0, 0)
	return vms, err
}

// ListPaged 返回非删除态 VM 的分页结果与过滤后总数。limit<=0 表示不限制。
// Trashed 行（PLAN-034）也排除——admin 主表只看 active VM；trash bin 走单独的 ListTrashedBefore。
//
// Session-2 F-46 / PLAN-051 §2-F：用 `COUNT(*) OVER ()` 单 SQL 同时拿 row + 总数，
// 避免 COUNT 与 SELECT 分两步在并发插入时返回不一致总数（race window）。
func (r *VMRepo) ListPaged(ctx context.Context, limit, offset int) ([]model.VM, int64, error) {
	query := `SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at, COUNT(*) OVER ()
		 FROM vms WHERE status != 'deleted' AND trashed_at IS NULL ORDER BY id DESC`
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

	var total int64
	out := make([]model.VM, 0)
	for rows.Next() {
		var vm model.VM
		var rowTotal int64
		if err := rows.Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.OrderID, &vm.IP,
			&vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password,
			&vm.RescueState, &vm.RescueStartedAt, &vm.RescueSnapshotName,
			&vm.TrashedAt, &vm.TrashedPrevStatus,
			&vm.CreatedAt, &vm.UpdatedAt, &rowTotal); err != nil {
			return nil, 0, err
		}
		vm.Password = decryptOnRead(vm.Password) // OPS-022
		total = rowTotal // 每行 total 都一样，最后一次赋值最稳
		out = append(out, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// 0 行结果时 total 仍为 0；调用方语义不变（ListAll 用 limit=0/offset=0 会扫全表）
	if total == 0 && len(out) == 0 {
		// 空结果时单独 COUNT；避免 limit/offset > 总数时漏报 total
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vms WHERE status != 'deleted' AND trashed_at IS NULL`).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count vms: %w", err)
		}
	}
	return out, total, nil
}

func (r *VMRepo) GetByName(ctx context.Context, name string) (*model.VM, error) {
	var vm model.VM
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms WHERE name = $1 AND status != 'deleted' AND trashed_at IS NULL LIMIT 1`, name,
	).Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.OrderID, &vm.IP, &vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password,
		&vm.RescueState, &vm.RescueStartedAt, &vm.RescueSnapshotName,
		&vm.TrashedAt, &vm.TrashedPrevStatus,
		&vm.CreatedAt, &vm.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vm by name: %w", err)
	}
	vm.Password = decryptOnRead(vm.Password) // OPS-022
	return &vm, nil
}

// IPsByNames returns a name → ip map for the given VM names (non-deleted rows only).
// Used by admin cluster listings to enrich Incus payloads with DB-recorded IPs.
//
// Session-2 F-47 / PLAN-051 §2-F：分批 1000 防 PG 参数表（限 65535）撑爆。pgx
// stdlib 驱动不自动转 []string → text[]，保留 placeholder 列表写法但加 chunk。
func (r *VMRepo) IPsByNames(ctx context.Context, names []string) (map[string]string, error) {
	result := map[string]string{}
	if len(names) == 0 {
		return result, nil
	}
	const chunkSize = 1000
	for i := 0; i < len(names); i += chunkSize {
		end := i + chunkSize
		if end > len(names) {
			end = len(names)
		}
		batch := names[i:end]
		if err := r.ipsByNamesChunk(ctx, batch, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *VMRepo) ipsByNamesChunk(ctx context.Context, names []string, out map[string]string) error {
	placeholders := make([]string, len(names))
	args := make([]any, len(names))
	for i, n := range names {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = n
	}
	q := `SELECT name, host(ip)::text FROM vms
	      WHERE name IN (` + strings.Join(placeholders, ",") + `)
	        AND status != 'deleted' AND trashed_at IS NULL AND ip IS NOT NULL`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("ips by names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, ip string
		if err := rows.Scan(&name, &ip); err != nil {
			return err
		}
		if ip != "" {
			out[name] = ip
		}
	}
	return rows.Err()
}

func (r *VMRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now(), id)
	return err
}

func (r *VMRepo) UpdatePassword(ctx context.Context, id int64, password string) error {
	enc, err := auth.EncryptPassword(password) // OPS-022
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`UPDATE vms SET password = $1, updated_at = $2 WHERE id = $3`,
		enc, time.Now(), id)
	return err
}

func (r *VMRepo) UpdateNode(ctx context.Context, id int64, node string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET node = $1, updated_at = $2 WHERE id = $3`,
		node, time.Now(), id)
	return err
}

// UpdateAfterProvision 在 jobs runner finalize 步骤一次性把 status / node /
// password 写回。仅在 status='creating' 或 status='running'（重装情形）时改，
// 防止把 admin 已手动转 'error' 的行又翻回 running。
func (r *VMRepo) UpdateAfterProvision(ctx context.Context, id int64, node, password string) error {
	enc, err := auth.EncryptPassword(password) // OPS-022
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`UPDATE vms
		 SET status = 'running', node = $1, password = $2, updated_at = $3
		 WHERE id = $4 AND status IN ('creating','running')`,
		node, enc, time.Now(), id)
	return err
}

func (r *VMRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET status = 'deleted', updated_at = $1 WHERE id = $2`,
		time.Now(), id)
	return err
}

// ListActiveForReconcile returns VMs in "live" statuses older than `cutoff`
// for the given cluster, used by PLAN-020 vm_reconciler to diff against the
// Incus instance listing. The cutoff excludes just-provisioned rows so a VM
// created within the buffer window isn't mislabelled as gone while Incus is
// still materialising it. Does not include status='gone' or 'deleted' rows.
// Trashed rows are excluded too — they're awaiting hard-delete by the trash
// purger, no point flipping them to 'gone' if Incus already considers them gone.
//
// OPS-052 P1-2：'creating' 状态**不**纳入比对。creating 表示 provisioning job 还在
// 跑（可能持续几分钟），此时 Incus 实例尚未创建可见——若 created_at 已越过 10s buffer
// 就会被误判 gone，reconciler 随即 Release 掉刚分配的 IP，导致 job finalize 时 IP 双
// 分配。只比对已就绪（running/stopped/migrating）的 VM，creating 由 job 自身负责收口。
func (r *VMRepo) ListActiveForReconcile(ctx context.Context, clusterID int64, cutoff time.Time) ([]model.VM, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms
		 WHERE cluster_id = $1
		   AND status IN ('running','stopped','migrating')
		   AND trashed_at IS NULL
		   AND created_at < $2`,
		clusterID, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("list active for reconcile: %w", err)
	}
	defer rows.Close()
	return scanVMs(rows)
}

// MarkGone flips a VM's status to 'gone' — distinct from 'deleted' (which
// records user-initiated removals). The reconciler uses this when a row
// exists in DB but the Incus instance has disappeared, so quota accounting
// and UIs can surface the drift without mutating the original `deleted`
// lifecycle.
func (r *VMRepo) MarkGone(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE vms SET status = 'gone', updated_at = NOW()
		 WHERE id = $1 AND status NOT IN ('gone','deleted')`,
		id,
	)
	if err != nil {
		return fmt.Errorf("mark vm gone: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either already gone/deleted or the row vanished under us. Not an
		// error for the reconciler — next cycle will simply see no drift.
		return nil
	}
	return nil
}

func (r *VMRepo) CountByUser(ctx context.Context, userID int64) (vms int, vcpus int, ramMB int, diskGB int, err error) {
	err = r.db.QueryRowContext(ctx,
		// 'gone' = Incus instance vanished out-of-band (PLAN-020 reconciler).
		// Counting it would double-charge quota after the reconciler flips
		// the row but before the admin force-deletes it.
		// Trashed rows (PLAN-034) are excluded from quota — once trashed_at is set
		// the VM is on the way to hard-delete and shouldn't count against the user's cap.
		`SELECT COUNT(*), COALESCE(SUM(cpu),0), COALESCE(SUM(memory_mb),0), COALESCE(SUM(disk_gb),0)
		 FROM vms WHERE user_id = $1 AND status NOT IN ('deleted','error','gone') AND trashed_at IS NULL`, userID,
	).Scan(&vms, &vcpus, &ramMB, &diskGB)
	return
}

// MarkGoneByName flips status to 'gone' identified by (cluster_id, name).
// Used by the event listener when an instance-deleted event arrives — the
// name is all the event carries, no DB id. Active statuses only; already
// 'deleted' (user-initiated) or 'gone' rows are left alone. Returns nil
// (not ErrNoRows) when the name isn't found so event arrival after the
// reconciler already cleaned up is a no-op.
//
// PLAN-034: trashed rows are explicitly excluded — when the trash purger
// drives Incus DELETE the resulting instance-deleted event would otherwise
// race against the purger's own DB UPDATE status='deleted'.
func (r *VMRepo) MarkGoneByName(ctx context.Context, clusterID int64, name string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET status = 'gone', updated_at = NOW()
		 WHERE cluster_id = $1 AND name = $2
		   AND status IN ('creating','running','stopped','migrating')
		   AND trashed_at IS NULL`,
		clusterID, name,
	)
	if err != nil {
		return fmt.Errorf("mark gone by name: %w", err)
	}
	return nil
}

// LookupForEvent returns (id, currentNode) for the VM matching
// (cluster_id, name). Used by the event listener to capture the from-node
// before applying UpdateNodeByName, so an in_progress healing_events row
// can record where each VM came from. Returns (0, "", nil) when the row
// isn't found — events for out-of-band / external instances are silent.
func (r *VMRepo) LookupForEvent(ctx context.Context, clusterID int64, name string) (int64, string, error) {
	var id int64
	var node sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, node FROM vms
		 WHERE cluster_id = $1 AND name = $2
		   AND status NOT IN ('deleted','gone')
		 LIMIT 1`,
		clusterID, name,
	).Scan(&id, &node)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("lookup for event: %w", err)
	}
	if node.Valid {
		return id, node.String, nil
	}
	return id, "", nil
}

// UpdateNodeByName records a new host node for a VM identified by name +
// cluster, driven by instance-updated / migrate events. No-op when the row
// isn't found so stale events don't error out.
func (r *VMRepo) UpdateNodeByName(ctx context.Context, clusterID int64, name string, node string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vms SET node = $3, updated_at = NOW()
		 WHERE cluster_id = $1 AND name = $2`,
		clusterID, name, node,
	)
	if err != nil {
		return fmt.Errorf("update node by name: %w", err)
	}
	return nil
}

// ListGone returns rows flagged by the reconciler as 'gone' — Incus instance
// vanished out-of-band. Admin surfaces these for force-delete / investigation.
// Ordered by updated_at DESC so the freshest drift shows first.
func (r *VMRepo) ListGone(ctx context.Context) ([]model.VM, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms WHERE status = 'gone' ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list gone vms: %w", err)
	}
	defer rows.Close()
	return scanVMs(rows)
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

// NodeVMStat 单节点的 VM 计数 + 按状态分桶（PLAN-037 / OPS-040 topology）。
type NodeVMStat struct {
	Node    string
	Total   int
	Running int
	Stopped int
	Other   int
}

// RebalanceVM 是 watchdog / rebalance 计算用的 VM 最小投影（PLAN-039 / OPS-044）。
type RebalanceVM struct {
	Name     string
	Project  string
	Node     string
	MemoryMB int64
}

// ListActiveForRebalance 返某 cluster 下所有活跃 VM 的最小投影。
// trashed/deleted/gone 不参与；node 为空也不参与（无法迁移）。
func (r *VMRepo) ListActiveForRebalance(ctx context.Context, clusterID int64) ([]RebalanceVM, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT name, COALESCE(node, '') AS node, memory_mb
		FROM vms
		WHERE cluster_id = $1
		  AND status IN ('creating','running','stopped','migrating')
		  AND trashed_at IS NULL
		  AND COALESCE(node, '') <> ''
	`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list active for rebalance: %w", err)
	}
	defer rows.Close()
	var out []RebalanceVM
	for rows.Next() {
		var v RebalanceVM
		if err := rows.Scan(&v.Name, &v.Node, &v.MemoryMB); err != nil {
			return nil, err
		}
		v.Project = "customers" // 项目当前固定 customers；DB 未单独存
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountVMsByNode 返回某 cluster 下所有 (node, status) 桶。trashed/deleted/gone
// 不参与（topology 关心当前活跃放置）。空 node 字符串视为 "unassigned"。
func (r *VMRepo) CountVMsByNode(ctx context.Context, clusterID int64) ([]NodeVMStat, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(node, ''), '') AS node, status, COUNT(*) AS n
		FROM vms
		WHERE cluster_id = $1
		  AND status NOT IN ('deleted', 'gone')
		  AND trashed_at IS NULL
		GROUP BY node, status`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("count vms by node: %w", err)
	}
	defer rows.Close()
	byNode := make(map[string]*NodeVMStat)
	for rows.Next() {
		var node, status string
		var n int
		if err := rows.Scan(&node, &status, &n); err != nil {
			return nil, err
		}
		s, ok := byNode[node]
		if !ok {
			s = &NodeVMStat{Node: node}
			byNode[node] = s
		}
		s.Total += n
		switch status {
		case model.VMStatusRunning:
			s.Running += n
		case model.VMStatusStopped:
			s.Stopped += n
		default:
			s.Other += n
		}
	}
	out := make([]NodeVMStat, 0, len(byNode))
	for _, s := range byNode {
		out = append(out, *s)
	}
	return out, nil
}

func scanVMs(rows *sql.Rows) ([]model.VM, error) {
	var vms []model.VM
	for rows.Next() {
		var vm model.VM
		if err := rows.Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.OrderID, &vm.IP,
			&vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password,
			&vm.RescueState, &vm.RescueStartedAt, &vm.RescueSnapshotName,
			&vm.TrashedAt, &vm.TrashedPrevStatus,
			&vm.CreatedAt, &vm.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan vm: %w", err)
		}
		vm.Password = decryptOnRead(vm.Password) // OPS-022
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}

// MarkTrashed atomically flags a VM as trashed (PLAN-034). prevStatus is
// recorded so Restore can return the VM to the same lifecycle (running →
// running，stopped → stopped). Idempotent: a row already in trash returns
// (false, nil). Returns the row id on success so the caller can audit.
func (r *VMRepo) MarkTrashed(ctx context.Context, vmID int64) (bool, error) {
	// OPS-052：软删同时清空 password —— VM 一旦进回收站即在 hard-delete 通路上，
	// 没必要继续在 DB 里保留可解密的登录凭据。restore 只恢复生命周期状态，密码由
	// 用户自行 reset（VM 内的实际口令不受影响，此处仅清 DB 展示记录）。
	res, err := r.db.ExecContext(ctx,
		`UPDATE vms
		 SET trashed_at = NOW(), trashed_prev_status = status, password = NULL, updated_at = NOW()
		 WHERE id = $1 AND trashed_at IS NULL AND status NOT IN ('deleted','gone')`,
		vmID,
	)
	if err != nil {
		return false, fmt.Errorf("mark trashed: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UnmarkTrashed reverses MarkTrashed within the trash window. Idempotent:
// a row not in trash returns (false, nil). Caller can read trashed_prev_status
// off the row before calling to know what state to restore Incus to.
func (r *VMRepo) UnmarkTrashed(ctx context.Context, vmID int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE vms
		 SET trashed_at = NULL, trashed_prev_status = NULL, updated_at = NOW()
		 WHERE id = $1 AND trashed_at IS NOT NULL`,
		vmID,
	)
	if err != nil {
		return false, fmt.Errorf("unmark trashed: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ListTrashedBefore returns trashed VMs whose trashed_at <= cutoff so the
// purger worker can hard-delete them. Includes only rows that haven't already
// completed hard-delete (status != 'deleted'). The caller iterates and calls
// service.PurgeTrashed for each.
func (r *VMRepo) ListTrashedBefore(ctx context.Context, cutoff time.Time) ([]model.VM, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms WHERE trashed_at IS NOT NULL AND trashed_at <= $1 AND status != 'deleted'
		 ORDER BY trashed_at ASC`, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("list trashed before: %w", err)
	}
	defer rows.Close()
	return scanVMs(rows)
}

// ListMyTrashed returns the user's currently-trashed VMs (within the trash
// window). Used by the portal to show an "undo" affordance even after page
// reload — the toast lives in localStorage but the source of truth is the DB.
func (r *VMRepo) ListMyTrashed(ctx context.Context, userID int64) ([]model.VM, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms
		 WHERE user_id = $1 AND trashed_at IS NOT NULL AND status != 'deleted'
		 ORDER BY trashed_at DESC`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list my trashed: %w", err)
	}
	defer rows.Close()
	return scanVMs(rows)
}

// GetByNameIncludingTrashed is like GetByName but also returns rows currently
// in the trash window. Restore / Purge handlers need this since GetByName
// filters trashed rows out by design.
func (r *VMRepo) GetByNameIncludingTrashed(ctx context.Context, name string) (*model.VM, error) {
	var vm model.VM
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, cluster_id, user_id, order_id, host(ip)::text, status, cpu, memory_mb, disk_gb, os_image, node, password, rescue_state, rescue_started_at, rescue_snapshot_name, trashed_at, trashed_prev_status, created_at, updated_at
		 FROM vms WHERE name = $1 AND status != 'deleted' LIMIT 1`, name,
	).Scan(&vm.ID, &vm.Name, &vm.ClusterID, &vm.UserID, &vm.OrderID, &vm.IP, &vm.Status, &vm.CPU, &vm.MemoryMB, &vm.DiskGB, &vm.OSImage, &vm.Node, &vm.Password,
		&vm.RescueState, &vm.RescueStartedAt, &vm.RescueSnapshotName,
		&vm.TrashedAt, &vm.TrashedPrevStatus,
		&vm.CreatedAt, &vm.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vm by name (incl trashed): %w", err)
	}
	vm.Password = decryptOnRead(vm.Password)
	return &vm, nil
}

// SetRescueState atomically transitions a VM from 'normal' to 'rescue' and
// records the snapshot name. Returns (false, nil) when the row wasn't in
// 'normal' state so the caller can convert to 409 (already-in-rescue).
func (r *VMRepo) SetRescueState(ctx context.Context, vmID int64, snapshotName string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE vms SET rescue_state = 'rescue', rescue_started_at = NOW(), rescue_snapshot_name = $1, updated_at = NOW()
		 WHERE id = $2 AND rescue_state = 'normal'`,
		snapshotName, vmID,
	)
	if err != nil {
		return false, fmt.Errorf("set rescue state: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ClearRescueState transitions 'rescue' back to 'normal' and clears the
// snapshot name + started_at. Idempotent: already-normal rows return
// (false, nil) without error.
func (r *VMRepo) ClearRescueState(ctx context.Context, vmID int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE vms SET rescue_state = 'normal', rescue_started_at = NULL, rescue_snapshot_name = NULL, updated_at = NOW()
		 WHERE id = $1 AND rescue_state = 'rescue'`,
		vmID,
	)
	if err != nil {
		return false, fmt.Errorf("clear rescue state: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
