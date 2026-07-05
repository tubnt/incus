package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/incuscloud/incus-admin/internal/model"
)

type FirewallRepo struct {
	db *sql.DB
}

func NewFirewallRepo(db *sql.DB) *FirewallRepo {
	return &FirewallRepo{db: db}
}

// --- groups ---

// PLAN-035：owner_id 区分 admin 共享组（NULL）与用户私有组。所有 SELECT 一并取。
const firewallGroupColumns = `id, slug, name, description, owner_id, created_at, updated_at`

func scanFirewallGroup(row interface{ Scan(dest ...any) error }, g *model.FirewallGroup) error {
	return row.Scan(&g.ID, &g.Slug, &g.Name, &g.Description, &g.OwnerID, &g.CreatedAt, &g.UpdatedAt)
}

// ListGroups 返回所有 firewall_groups。仅 admin 端使用。
// portal 端用 ListGroupsForUser 做 owner 过滤。
func (r *FirewallRepo) ListGroups(ctx context.Context) ([]model.FirewallGroup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+firewallGroupColumns+` FROM firewall_groups ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.FirewallGroup, 0)
	for rows.Next() {
		var g model.FirewallGroup
		if err := scanFirewallGroup(rows, &g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListGroupsForUser 返回用户**可见**的 firewall_groups：admin 共享组（owner_id IS NULL）
// + 自己拥有的私有组（owner_id = userID）。PLAN-035 portal 端使用。
func (r *FirewallRepo) ListGroupsForUser(ctx context.Context, userID int64) ([]model.FirewallGroup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+firewallGroupColumns+` FROM firewall_groups
		 WHERE owner_id IS NULL OR owner_id = $1
		 ORDER BY owner_id NULLS FIRST, id ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.FirewallGroup, 0)
	for rows.Next() {
		var g model.FirewallGroup
		if err := scanFirewallGroup(rows, &g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CountGroupsByUser 统计某用户的私有组数量（不含共享组），用于 quota 校验。
func (r *FirewallRepo) CountGroupsByUser(ctx context.Context, userID int64) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM firewall_groups WHERE owner_id = $1`, userID,
	).Scan(&n)
	return n, err
}

func (r *FirewallRepo) GetGroupByID(ctx context.Context, id int64) (*model.FirewallGroup, error) {
	var g model.FirewallGroup
	err := scanFirewallGroup(
		r.db.QueryRowContext(ctx, `SELECT `+firewallGroupColumns+` FROM firewall_groups WHERE id = $1`, id),
		&g,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (r *FirewallRepo) CreateGroup(ctx context.Context, g *model.FirewallGroup) (*model.FirewallGroup, error) {
	var out model.FirewallGroup
	err := scanFirewallGroup(
		r.db.QueryRowContext(ctx,
			`INSERT INTO firewall_groups (slug, name, description, owner_id)
			 VALUES ($1, $2, $3, $4)
			 RETURNING `+firewallGroupColumns,
			g.Slug, g.Name, g.Description, g.OwnerID,
		),
		&out,
	)
	if err != nil {
		return nil, fmt.Errorf("create firewall_group: %w", err)
	}
	return &out, nil
}

func (r *FirewallRepo) UpdateGroup(ctx context.Context, g *model.FirewallGroup) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE firewall_groups SET slug=$1, name=$2, description=$3, updated_at=NOW() WHERE id=$4`,
		g.Slug, g.Name, g.Description, g.ID,
	)
	return err
}

// CountBindingsForGroup returns how many VMs are currently bound to a group;
// callers (DeleteGroup handler) use it to refuse deletion of in-use groups.
func (r *FirewallRepo) CountBindingsForGroup(ctx context.Context, groupID int64) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vm_firewall_bindings WHERE group_id = $1`, groupID,
	).Scan(&n)
	return n, err
}

func (r *FirewallRepo) DeleteGroup(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM firewall_groups WHERE id = $1`, id)
	return err
}

// --- rules ---

const firewallRuleColumns = `id, group_id, COALESCE(direction, 'ingress'), action, protocol, destination_port, source_cidr, description, sort_order, created_at`

func scanFirewallRule(row interface{ Scan(dest ...any) error }, rule *model.FirewallRule) error {
	return row.Scan(&rule.ID, &rule.GroupID, &rule.Direction, &rule.Action, &rule.Protocol, &rule.DestinationPort,
		&rule.SourceCIDR, &rule.Description, &rule.SortOrder, &rule.CreatedAt)
}

func (r *FirewallRepo) ListRules(ctx context.Context, groupID int64) ([]model.FirewallRule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+firewallRuleColumns+` FROM firewall_rules WHERE group_id = $1 ORDER BY sort_order ASC, id ASC`,
		groupID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.FirewallRule, 0)
	for rows.Next() {
		var rule model.FirewallRule
		if err := scanFirewallRule(rows, &rule); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// ListRulesByGroups 一次拉多 group 的全部 rules，按 group_id 分桶返回。
// Session-2 F-20 / PLAN-051 §2-F：替代 ListGroups + N×ListRules 的 N+1 模式。
// 50 group → 1 SQL，原版 51 SQL。
func (r *FirewallRepo) ListRulesByGroups(ctx context.Context, groupIDs []int64) (map[int64][]model.FirewallRule, error) {
	out := map[int64][]model.FirewallRule{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	// 用 placeholder 列表（pgx stdlib 不自动转 []int64 → bigint[]）
	placeholders := make([]string, len(groupIDs))
	args := make([]any, len(groupIDs))
	for i, id := range groupIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	q := `SELECT ` + firewallRuleColumns + ` FROM firewall_rules
	      WHERE group_id IN (` + strings.Join(placeholders, ",") + `)
	      ORDER BY group_id, sort_order ASC, id ASC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rule model.FirewallRule
		if err := scanFirewallRule(rows, &rule); err != nil {
			return nil, err
		}
		out[rule.GroupID] = append(out[rule.GroupID], rule)
	}
	return out, rows.Err()
}

// ReplaceRules atomically swaps all rules for a group. Used by PATCH
// /firewall/groups/{id} which accepts the full rule list at once — simpler
// contract than per-rule PUT/DELETE.
func (r *FirewallRepo) ReplaceRules(ctx context.Context, groupID int64, rules []model.FirewallRule) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM firewall_rules WHERE group_id = $1`, groupID); err != nil {
		return fmt.Errorf("clear rules: %w", err)
	}
	for _, rule := range rules {
		dir := rule.Direction
		if dir == "" {
			dir = "ingress"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO firewall_rules (group_id, direction, action, protocol, destination_port, source_cidr, description, sort_order)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			groupID, dir, rule.Action, rule.Protocol, rule.DestinationPort, rule.SourceCIDR, rule.Description, rule.SortOrder,
		); err != nil {
			return fmt.Errorf("insert rule: %w", err)
		}
	}
	return tx.Commit()
}

// --- bindings ---

func (r *FirewallRepo) ListBindingsByVM(ctx context.Context, vmID int64) ([]model.FirewallGroup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT g.id, g.slug, g.name, g.description, g.owner_id, g.created_at, g.updated_at
		 FROM firewall_groups g
		 JOIN vm_firewall_bindings b ON b.group_id = g.id
		 WHERE b.vm_id = $1
		 ORDER BY g.id ASC`,
		vmID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.FirewallGroup, 0)
	for rows.Next() {
		var g model.FirewallGroup
		if err := scanFirewallGroup(rows, &g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *FirewallRepo) Bind(ctx context.Context, vmID, groupID int64) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO vm_firewall_bindings (vm_id, group_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`,
		vmID, groupID,
	)
	return err
}

func (r *FirewallRepo) Unbind(ctx context.Context, vmID, groupID int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM vm_firewall_bindings WHERE vm_id = $1 AND group_id = $2`,
		vmID, groupID,
	)
	return err
}

// --- defaults (PLAN-036) ---

// ListDefaultGroupsForUser 返回用户的默认 firewall_groups（按 sort_order 升序）。
// 仅新建 VM 时被 jobs finalize 调用一次性应用；现有 VM 不受影响。
func (r *FirewallRepo) ListDefaultGroupsForUser(ctx context.Context, userID int64) ([]model.FirewallGroup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT g.id, g.slug, g.name, g.description, g.owner_id, g.created_at, g.updated_at
		 FROM firewall_groups g
		 JOIN user_default_firewall_groups d ON d.group_id = g.id
		 WHERE d.user_id = $1
		 ORDER BY d.sort_order ASC, g.id ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.FirewallGroup, 0)
	for rows.Next() {
		var g model.FirewallGroup
		if err := scanFirewallGroup(rows, &g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ReplaceDefaultGroups 原子替换某 user 的默认组列表。groupIDs 顺序即 sort_order。
// 调用方应已在 handler 层校验：每个 group 必须 owner_id IS NULL OR = userID。
func (r *FirewallRepo) ReplaceDefaultGroups(ctx context.Context, userID int64, groupIDs []int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_default_firewall_groups WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear defaults: %w", err)
	}
	for i, gid := range groupIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_default_firewall_groups (user_id, group_id, sort_order) VALUES ($1, $2, $3)`,
			userID, gid, i*10,
		); err != nil {
			return fmt.Errorf("insert default: %w", err)
		}
	}
	return tx.Commit()
}

// BindingCountsForUser 一次返回 group_id → user 自己 VM 中已绑该组的数量。
// admin 共享组在本结果里只计该用户自己的 VM，避免跨用户信息泄漏。
// 用于 PortalListGroups 给每行显示 "已绑 X 台" chip，免去 N+1。
//
// COUNT(v.id) 不计 LEFT JOIN 落空（非本用户 VM / 已删 / 已 trash）的 b 行；
// 用 COUNT(b.vm_id) 会让 admin 共享组把别人挂的 VM 数泄漏给当前用户。
func (r *FirewallRepo) BindingCountsForUser(ctx context.Context, userID int64) (map[int64]int, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT g.id, COUNT(v.id)
		 FROM firewall_groups g
		 LEFT JOIN vm_firewall_bindings b ON b.group_id = g.id
		 LEFT JOIN vms v ON v.id = b.vm_id
		   AND v.user_id = $1
		   AND v.status NOT IN ('deleted','gone')
		   AND v.trashed_at IS NULL
		 WHERE g.owner_id IS NULL OR g.owner_id = $1
		 GROUP BY g.id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var gid int64
		var n int
		if err := rows.Scan(&gid, &n); err != nil {
			return nil, err
		}
		out[gid] = n
	}
	return out, rows.Err()
}

// ListBoundVMsForGroup 返回当前用户拥有的、绑定到给定 group 的 VM 列表。
// 仅返自己的 VM —— admin 共享组的跨用户绑定不通过此 endpoint 暴露。
func (r *FirewallRepo) ListBoundVMsForGroup(ctx context.Context, groupID, userID int64) ([]model.VM, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT v.id, v.name, v.cluster_id, v.user_id, v.order_id, host(v.ip)::text, v.status,
		        v.cpu, v.memory_mb, v.disk_gb, v.os_image, v.node, v.password,
		        v.rescue_state, v.rescue_started_at, v.rescue_snapshot_name,
		        v.trashed_at, v.trashed_prev_status, v.created_at, v.updated_at
		 FROM vms v
		 JOIN vm_firewall_bindings b ON b.vm_id = v.id
		 WHERE b.group_id = $1 AND v.user_id = $2 AND v.status NOT IN ('deleted','gone') AND v.trashed_at IS NULL
		 ORDER BY v.id DESC`,
		groupID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVMs(rows)
}
