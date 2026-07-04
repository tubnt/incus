package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/incuscloud/incus-admin/internal/repository"
)

// PLAN-041 / INFRA-009 alert evaluator
//
// 1 分钟 tick：
//   1) 拉 enabled alert_rules
//   2) 按 kind 派发到子例程（imbalance / vm_down / cluster_node_offline /
//      balance_low / job_failed / order_failed）
//   3) 触发 → UpsertWithGroup → 给每个 channel_id 入队一条 firing delivery
//   4) 不再触发 → ResolveAndReturn → 每个 channel_id 入队一条 resolved delivery
//
// 一期不评估 vm_cpu / vm_mem / vm_disk（需要 Incus metrics fan-out 完整框架，
// 留作 v2）。imbalance 是与现有 watchdog 协作模式：watchdog 仍然写 system_alerts，
// evaluator 仅扇出 dispatch。

// EvaluatorDeps 集中评估器依赖。
type EvaluatorDeps struct {
	Rules      *repository.AlertRuleRepo
	Alerts     *repository.SystemAlertRepo
	Deliveries *repository.AlertDeliveryRepo
	VMs        VMDownLister
	Users      UsersBalanceLister
	Jobs       JobFailureCounter
	Orders     OrderFailureCounter
	Nodes      ClusterNodeLister
}

// VMDownLister 给 vm_down 评估用：列窗口内进入 gone/error 的 VM。
type VMDownLister interface {
	ListRecentlyDown(ctx context.Context, since time.Time) ([]VMDownInfo, error)
}

type VMDownInfo struct {
	VMID    int64
	Name    string
	Cluster string
	Status  string
}

// UsersBalanceLister 给 balance_low 评估用：列余额低于阈值的用户。
type UsersBalanceLister interface {
	ListBelowBalance(ctx context.Context, threshold float64) ([]UserBalance, error)
}

type UserBalance struct {
	ID      int64
	Email   string
	Balance float64
}

// JobFailureCounter 给 job_failed 评估用：窗口内 failed job 数量。
type JobFailureCounter interface {
	CountFailedSince(ctx context.Context, since time.Time) (int, error)
}

// OrderFailureCounter 给 order_failed 评估用。
type OrderFailureCounter interface {
	CountFailedSince(ctx context.Context, since time.Time) (int, error)
}

// ClusterNodeLister 给 cluster_node_offline 评估用：
// 返回 (cluster, offline_node_names)。
type ClusterNodeLister interface {
	ListOfflineNodes(ctx context.Context) (map[string][]string, error)
}

// RunAlertEvaluator 启动评估循环。tickEvery <= 0 → 默认 1 分钟。
func RunAlertEvaluator(ctx context.Context, deps EvaluatorDeps, tickEvery time.Duration) {
	if deps.Rules == nil || deps.Alerts == nil || deps.Deliveries == nil {
		slog.Info("alert evaluator disabled (deps missing)")
		return
	}
	if tickEvery <= 0 {
		tickEvery = 1 * time.Minute
	}
	slog.Info("alert evaluator started", "tick", tickEvery)

	tick := time.NewTicker(tickEvery)
	defer tick.Stop()

	runOnce := func() {
		// 顶层 recover：单 cluster 评估出错不影响其他规则
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("alert evaluator panic", "panic", rec)
			}
		}()
		rules, err := deps.Rules.ListEnabled(ctx)
		if err != nil {
			slog.Warn("evaluator: list rules failed", "error", err)
			return
		}
		for _, rule := range rules {
			evalRule(ctx, deps, &rule)
		}
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("alert evaluator stopping")
			return
		case <-tick.C:
			runOnce()
		}
	}
}

// evalRule 按 kind 派发。每个子例程独立 recover，避免一条规则挂掉拖死其他。
func evalRule(ctx context.Context, deps EvaluatorDeps, rule *repository.AlertRule) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("evaluator rule panic", "rule_id", rule.ID, "kind", rule.Kind, "panic", rec)
		}
	}()
	switch rule.Kind {
	case repository.AlertKindImbalance:
		evalImbalance(ctx, deps, rule)
	case repository.AlertKindVMDown:
		evalVMDown(ctx, deps, rule)
	case repository.AlertKindClusterNodeOffline:
		evalClusterNodeOffline(ctx, deps, rule)
	case repository.AlertKindBalanceLow:
		evalBalanceLow(ctx, deps, rule)
	case repository.AlertKindJobFailed:
		evalJobFailed(ctx, deps, rule)
	case repository.AlertKindOrderFailed:
		evalOrderFailed(ctx, deps, rule)
	default:
		// vm_cpu / vm_mem / vm_disk / backup_failed 留作 v2
	}
}

// ----------------------------------------------------------------------------
// imbalance：与 watchdog 协作模式 —— watchdog 已写 system_alerts，evaluator
// 在 1 分钟 tick 中扇出 dispatch（让 imbalance 也能推钉钉/飞书）。
//
// 不重复评估 imbalance 逻辑，只查 active system_alerts(kind='imbalance') 行
// + 按 rule.channel_ids 入队 firing delivery；resolved 行同理（dedup 三元组
// 让重复 tick 不会重发同一 firing/resolved）。
// ----------------------------------------------------------------------------
func evalImbalance(ctx context.Context, deps EvaluatorDeps, rule *repository.AlertRule) {
	actives, err := deps.Alerts.ListActive(ctx)
	if err != nil {
		slog.Warn("evaluator imbalance: list active failed", "error", err)
		return
	}
	for _, a := range actives {
		if a.Kind != repository.AlertKindImbalance {
			continue
		}
		groupKey := derefOrPlain(a.GroupKey, a.Kind+":"+a.Cluster)
		ev := buildEvent(a.Kind, a.Cluster, rule.Severity, "firing", groupKey, a.Payload)
		enqueueAll(ctx, deps, &a.ID, &rule.ID, rule.ChannelIDs, groupKey, "firing", rule.Severity, ev)
	}
}

// ----------------------------------------------------------------------------
// vm_down：窗口内进入 gone / error 状态的 VM。
//
// 对每台 down VM 触发一个 group_key=vm_down:<vm_id>，severity 由 rule 决定。
// 没有 VMDownLister 实现时跳过（dev / 测试环境）。
// ----------------------------------------------------------------------------
func evalVMDown(ctx context.Context, deps EvaluatorDeps, rule *repository.AlertRule) {
	if deps.VMs == nil {
		return
	}
	since := time.Now().Add(-time.Duration(rule.WindowSeconds) * time.Second)
	downs, err := deps.VMs.ListRecentlyDown(ctx, since)
	if err != nil {
		slog.Warn("evaluator vm_down: list failed", "error", err)
		return
	}
	downSet := make(map[int64]bool, len(downs))
	for _, d := range downs {
		downSet[d.VMID] = true
		groupKey := fmt.Sprintf("vm_down:%d", d.VMID)
		payload, _ := json.Marshal(map[string]any{
			"vm_id": d.VMID, "name": d.Name, "status": d.Status,
		})
		scopeID := d.VMID
		alertID, err := deps.Alerts.UpsertWithGroup(
			ctx, rule.Kind, d.Cluster, rule.Severity, groupKey, "vm",
			&scopeID, &rule.ID, payload,
		)
		if err != nil {
			slog.Warn("evaluator vm_down: upsert failed", "error", err)
			continue
		}
		ev := buildEvent(rule.Kind, d.Cluster, rule.Severity, "firing", groupKey, payload)
		ev.Title = fmt.Sprintf("VM %s 进入异常状态: %s", d.Name, d.Status)
		ev.ScopeKind = "vm"
		ev.ScopeID = &scopeID
		enqueueAll(ctx, deps, &alertID, &rule.ID, rule.ChannelIDs, groupKey, "firing", rule.Severity, ev)
	}
	// resolve 分支：此前 firing 过、但本轮已不在 down 列表（VM 恢复到正常状态）
	// 的 vm_down 告警翻转为 resolved。只触发不消解会让告警永久悬挂。
	resolveRecoveredScoped(ctx, deps, rule, repository.AlertKindVMDown, downSet,
		func(a *repository.SystemAlert) string { return "VM 已恢复正常" })
}

// ----------------------------------------------------------------------------
// cluster_node_offline：列各 cluster 当前 offline node。
// ----------------------------------------------------------------------------
func evalClusterNodeOffline(ctx context.Context, deps EvaluatorDeps, rule *repository.AlertRule) {
	if deps.Nodes == nil {
		return
	}
	offlineMap, err := deps.Nodes.ListOfflineNodes(ctx)
	if err != nil {
		slog.Warn("evaluator cluster_node_offline: list failed", "error", err)
		return
	}
	for cluster, nodes := range offlineMap {
		if len(nodes) == 0 {
			// 之前可能 firing 过 → 走 resolve 路径
			if a, _ := deps.Alerts.ResolveAndReturn(ctx, rule.Kind, cluster); a != nil {
				groupKey := derefOrPlain(a.GroupKey, rule.Kind+":"+cluster)
				ev := buildEvent(rule.Kind, cluster, rule.Severity, "resolved", groupKey, a.Payload)
				ev.Title = fmt.Sprintf("集群 %s 节点已全部恢复在线", cluster)
				enqueueAll(ctx, deps, &a.ID, &rule.ID, rule.ChannelIDs, groupKey, "resolved", rule.Severity, ev)
			}
			continue
		}
		groupKey := rule.Kind + ":" + cluster
		payload, _ := json.Marshal(map[string]any{"offline": nodes})
		alertID, err := deps.Alerts.UpsertWithGroup(
			ctx, rule.Kind, cluster, rule.Severity, groupKey, "cluster",
			nil, &rule.ID, payload,
		)
		if err != nil {
			slog.Warn("evaluator cluster_node_offline: upsert failed", "error", err)
			continue
		}
		ev := buildEvent(rule.Kind, cluster, rule.Severity, "firing", groupKey, payload)
		ev.Title = fmt.Sprintf("集群 %s 有节点离线", cluster)
		ev.Message = fmt.Sprintf("offline nodes: %v", nodes)
		ev.ScopeKind = "cluster"
		enqueueAll(ctx, deps, &alertID, &rule.ID, rule.ChannelIDs, groupKey, "firing", rule.Severity, ev)
	}
}

// ----------------------------------------------------------------------------
// balance_low：余额低于 threshold 的用户。
// ----------------------------------------------------------------------------
func evalBalanceLow(ctx context.Context, deps EvaluatorDeps, rule *repository.AlertRule) {
	if deps.Users == nil || rule.Threshold == nil {
		return
	}
	users, err := deps.Users.ListBelowBalance(ctx, *rule.Threshold)
	if err != nil {
		slog.Warn("evaluator balance_low: list failed", "error", err)
		return
	}
	lowSet := make(map[int64]bool, len(users))
	for _, u := range users {
		lowSet[u.ID] = true
		groupKey := fmt.Sprintf("balance_low:%d", u.ID)
		payload, _ := json.Marshal(map[string]any{
			"user_id": u.ID, "email": redactEmail(u.Email), "balance": u.Balance, "threshold": *rule.Threshold,
		})
		uid := u.ID
		alertID, err := deps.Alerts.UpsertWithGroup(
			ctx, rule.Kind, "", rule.Severity, groupKey, "user", &uid, &rule.ID, payload,
		)
		if err != nil {
			slog.Warn("evaluator balance_low: upsert failed", "error", err)
			continue
		}
		ev := buildEvent(rule.Kind, "", rule.Severity, "firing", groupKey, payload)
		ev.Title = fmt.Sprintf("用户余额低于阈值: $%.2f / $%.2f", u.Balance, *rule.Threshold)
		ev.ScopeKind = "user"
		ev.ScopeID = &uid
		enqueueAll(ctx, deps, &alertID, &rule.ID, rule.ChannelIDs, groupKey, "firing", rule.Severity, ev)
	}
	// resolve 分支：余额回升到阈值以上（充值后）的用户，其 balance_low 告警翻转
	// resolved。ListBelowBalance 无时间窗口、返回全量低余额用户，故"不在列表"即
	// 确定已恢复，不会误消解。
	resolveRecoveredScoped(ctx, deps, rule, repository.AlertKindBalanceLow, lowSet,
		func(a *repository.SystemAlert) string { return "用户余额已回升至阈值以上" })
}

// ----------------------------------------------------------------------------
// job_failed：窗口内失败 job 数 ≥ threshold。
// ----------------------------------------------------------------------------
func evalJobFailed(ctx context.Context, deps EvaluatorDeps, rule *repository.AlertRule) {
	if deps.Jobs == nil || rule.Threshold == nil {
		return
	}
	since := time.Now().Add(-time.Duration(rule.WindowSeconds) * time.Second)
	count, err := deps.Jobs.CountFailedSince(ctx, since)
	if err != nil {
		slog.Warn("evaluator job_failed: count failed", "error", err)
		return
	}
	groupKey := "job_failed:global"
	if float64(count) >= *rule.Threshold {
		payload, _ := json.Marshal(map[string]any{
			"count": count, "window_seconds": rule.WindowSeconds, "threshold": *rule.Threshold,
		})
		alertID, err := deps.Alerts.UpsertWithGroup(
			ctx, rule.Kind, "", rule.Severity, groupKey, "global", nil, &rule.ID, payload,
		)
		if err != nil {
			slog.Warn("evaluator job_failed: upsert failed", "error", err)
			return
		}
		ev := buildEvent(rule.Kind, "", rule.Severity, "firing", groupKey, payload)
		ev.Title = fmt.Sprintf("最近 %ds 内 %d 个 job 失败", rule.WindowSeconds, count)
		enqueueAll(ctx, deps, &alertID, &rule.ID, rule.ChannelIDs, groupKey, "firing", rule.Severity, ev)
	} else {
		// 不再越线 → 走 resolve
		if a, _ := deps.Alerts.ResolveAndReturn(ctx, rule.Kind, ""); a != nil {
			groupKeyR := derefOrPlain(a.GroupKey, groupKey)
			ev := buildEvent(rule.Kind, "", rule.Severity, "resolved", groupKeyR, a.Payload)
			ev.Title = "Job 失败率已恢复正常"
			enqueueAll(ctx, deps, &a.ID, &rule.ID, rule.ChannelIDs, groupKeyR, "resolved", rule.Severity, ev)
		}
	}
}

// ----------------------------------------------------------------------------
// order_failed：与 job_failed 同形态。
// ----------------------------------------------------------------------------
func evalOrderFailed(ctx context.Context, deps EvaluatorDeps, rule *repository.AlertRule) {
	if deps.Orders == nil || rule.Threshold == nil {
		return
	}
	since := time.Now().Add(-time.Duration(rule.WindowSeconds) * time.Second)
	count, err := deps.Orders.CountFailedSince(ctx, since)
	if err != nil {
		slog.Warn("evaluator order_failed: count failed", "error", err)
		return
	}
	groupKey := "order_failed:global"
	if float64(count) >= *rule.Threshold {
		payload, _ := json.Marshal(map[string]any{
			"count": count, "window_seconds": rule.WindowSeconds, "threshold": *rule.Threshold,
		})
		alertID, err := deps.Alerts.UpsertWithGroup(
			ctx, rule.Kind, "", rule.Severity, groupKey, "global", nil, &rule.ID, payload,
		)
		if err != nil {
			slog.Warn("evaluator order_failed: upsert failed", "error", err)
			return
		}
		ev := buildEvent(rule.Kind, "", rule.Severity, "firing", groupKey, payload)
		ev.Title = fmt.Sprintf("最近 %ds 内 %d 个订单失败", rule.WindowSeconds, count)
		enqueueAll(ctx, deps, &alertID, &rule.ID, rule.ChannelIDs, groupKey, "firing", rule.Severity, ev)
	} else {
		if a, _ := deps.Alerts.ResolveAndReturn(ctx, rule.Kind, ""); a != nil {
			groupKeyR := derefOrPlain(a.GroupKey, groupKey)
			ev := buildEvent(rule.Kind, "", rule.Severity, "resolved", groupKeyR, a.Payload)
			ev.Title = "订单失败率已恢复正常"
			enqueueAll(ctx, deps, &a.ID, &rule.ID, rule.ChannelIDs, groupKeyR, "resolved", rule.Severity, ev)
		}
	}
}

// ============================================================================
// helpers
// ============================================================================

// AlertEventDTO 与 service/notify.AlertEvent 等价，但本包不引 service 包避免循环依赖。
// dispatcher 在出队时把 payload JSON 转成 service/notify.AlertEvent。
type AlertEventDTO struct {
	GroupKey  string         `json:"group_key"`
	Kind      string         `json:"kind"`
	Severity  string         `json:"severity"`
	Phase     string         `json:"phase"`
	Cluster   string         `json:"cluster,omitempty"`
	ScopeKind string         `json:"scope_kind,omitempty"`
	ScopeID   *int64         `json:"scope_id,omitempty"`
	Title     string         `json:"title,omitempty"`
	Message   string         `json:"message,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

func buildEvent(kind, cluster, severity, phase, groupKey string, raw json.RawMessage) AlertEventDTO {
	ev := AlertEventDTO{
		GroupKey: groupKey,
		Kind:     kind,
		Severity: severity,
		Phase:    phase,
		Cluster:  cluster,
	}
	if raw != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &ev.Extra)
	}
	return ev
}

// enqueueAll 给 rule.channel_ids 列表里每个 channel 入队一条 delivery。
// 三元组 dedup 由 EnqueueIfNotDuplicated 保证（24h 内同 (channel, group_key, phase)
// 已 pending/success → 跳过）。
func enqueueAll(
	ctx context.Context, deps EvaluatorDeps,
	alertID, ruleID *int64, channelIDs []int64,
	groupKey, phase, severity string, ev AlertEventDTO,
) {
	if len(channelIDs) == 0 {
		return
	}
	payload, _ := json.Marshal(ev)
	for _, cid := range channelIDs {
		_, err := deps.Deliveries.EnqueueIfNotDuplicated(
			ctx, alertID, ruleID, cid, groupKey, phase, severity, payload,
		)
		if err != nil {
			slog.Warn("evaluator enqueue delivery failed",
				"channel_id", cid, "group_key", groupKey, "phase", phase, "error", err)
		}
	}
}

// resolveRecoveredScoped 把某 kind 下当前仍 active、但 scope_id 已不在 stillFiring
// 集合中的告警翻转为 resolved，并给每个 channel 入队一条 resolved delivery。
// 用于 vm_down / balance_low 这类 per-scope（单 VM / 单用户）告警的消解：只触发
// 不消解会让告警永久悬挂。titleFn 生成 resolved 通知标题。
//
// ResolveByGroup 的 UPDATE ... WHERE resolved_at IS NULL RETURNING 天然幂等：
// 多条 rule / 重复 tick 抢同一 group 时只有一条拿到行，其余得 nil 跳过。
func resolveRecoveredScoped(
	ctx context.Context, deps EvaluatorDeps, rule *repository.AlertRule,
	kind string, stillFiring map[int64]bool, titleFn func(*repository.SystemAlert) string,
) {
	actives, err := deps.Alerts.ListActive(ctx)
	if err != nil {
		slog.Warn("evaluator: list active for resolve failed", "kind", kind, "error", err)
		return
	}
	for i := range actives {
		a := actives[i]
		if a.Kind != kind || a.GroupKey == nil {
			continue
		}
		if a.ScopeID != nil && stillFiring[*a.ScopeID] {
			continue // 仍在触发条件内，不消解
		}
		resolved, err := deps.Alerts.ResolveByGroup(ctx, *a.GroupKey)
		if err != nil {
			slog.Warn("evaluator: resolve by group failed", "kind", kind, "group_key", *a.GroupKey, "error", err)
			continue
		}
		if resolved == nil {
			continue // 已被并发消解
		}
		ev := buildEvent(kind, resolved.Cluster, rule.Severity, "resolved", *resolved.GroupKey, resolved.Payload)
		ev.Title = titleFn(resolved)
		if resolved.ScopeKind != nil {
			ev.ScopeKind = *resolved.ScopeKind
		}
		ev.ScopeID = resolved.ScopeID
		enqueueAll(ctx, deps, &resolved.ID, &rule.ID, rule.ChannelIDs, *resolved.GroupKey, "resolved", rule.Severity, ev)
	}
}

func derefOrPlain(p *string, fallback string) string {
	if p == nil || *p == "" {
		return fallback
	}
	return *p
}

func redactEmail(e string) string {
	for i, c := range e {
		if c == '@' {
			if i <= 2 {
				return "**" + e[i:]
			}
			return e[:2] + "***" + e[i:]
		}
	}
	return "***"
}
