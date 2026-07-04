package worker

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/model"
)

// isAlreadyGoneErr identifies "Incus instance vanished out-of-band" errors so
// the trash purger treats them as success (idempotent hard-delete). Without
// this, a row whose underlying instance was wiped by the reconciler / manual
// `incus delete` / failed creation would loop forever in the purger because
// service.Delete passes Incus's 404 back as a regular error.
//
// 匹配字面量判断而非 errors.Is，是因为 cluster.Client.APIDelete 把 Incus 4xx
// 状态包成 fmt.Errorf 字符串（"incus error: Instance not found" 等）。
//
// OPS-052：收窄匹配 —— 只认 Incus 明确的"实例不存在"措辞，不再用宽泛的裸
// "not found"。裸 "not found" 会把 project not found / network not found / storage
// pool not found 等无关错误也吞成"已删"，导致 DB 行被误标 deleted 而 Incus 实例
// 其实还在（资源泄漏 + 计费错乱）。
func isAlreadyGoneErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "instance not found") ||
		strings.Contains(s, "no such object") ||
		strings.Contains(s, "not found instance")
}

// VMTrashRepo 暴露 trash purger 需要的最小切片。
type VMTrashRepo interface {
	ListTrashedBefore(ctx context.Context, cutoff time.Time) ([]model.VM, error)
	Delete(ctx context.Context, id int64) error // 标 status='deleted'
}

// ClusterResolver 把 DB cluster_id 解析为 service.PurgeTrashed 需要的
// (clusterName, project)。直接复用 cluster.Manager 即可，但拆口子让 worker 单元测试可注入。
type ClusterResolver interface {
	List() []*cluster.Client
	IDByName(name string) int64
}

// VMTrashPurgeFn 是 service.VMService.PurgeTrashed 的拓窄接口。
type VMTrashPurgeFn func(ctx context.Context, clusterName, project, vmName string) error

// VMReclaimFn 在 hard-delete 收口时释放 VM 关联的全部网络资源：普通 IP 回可用池
// （走 cooldown）、Floating IP detach、firewall ACL 绑定解除（OPS-052 P0-3 / P1-5）。
// 实现由 main.go 以闭包注入（持有 ipAddr/floatingIP/firewall repo + FloatingIPService），
// 保持 worker 包不反向依赖 repository/service。内部逐项 best-effort，返回的错误仅供
// purger 记 warn，不阻断 DB 落 deleted —— 避免个别资源清理失败让行永远悬挂在回收站。
// nil 表示未接线（DB-only 测试环境），purger 跳过资源回收。
type VMReclaimFn func(ctx context.Context, vm model.VM, clusterName, project string) error

// RunVMTrashPurger 周期扫 trashed_at <= NOW()-window 的 VM，调 purgeFn 走原 hard-delete。
// 无 manager 或 trash 行为零时不工作。失败行下次再试，purgeFn 自身需幂等（Incus
// 实例不存在 = 已经走过；DB Delete 是 status='deleted' 写入也是幂等的 UPDATE）。
//
// tickEvery <= 0 默认 5s（窗口 30s 时一个周期内能赶上）。window <= 0 → worker 退出。
func RunVMTrashPurger(
	ctx context.Context,
	repo VMTrashRepo,
	clusters ClusterResolver,
	purgeFn VMTrashPurgeFn,
	reclaimFn VMReclaimFn,
	window, tickEvery time.Duration,
) {
	if repo == nil || clusters == nil || purgeFn == nil || window <= 0 {
		slog.Info("vm trash purger disabled", "window", window)
		return
	}
	if tickEvery <= 0 {
		tickEvery = 5 * time.Second
	}
	slog.Info("vm trash purger started", "window", window, "tick", tickEvery)

	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("vm trash purger stopping")
			return
		case <-tick.C:
			cutoff := time.Now().Add(-window)
			rows, err := repo.ListTrashedBefore(ctx, cutoff)
			if err != nil {
				slog.Error("trash purger: list failed", "error", err)
				continue
			}
			if len(rows) == 0 {
				continue
			}
			// cluster_id → cluster name 反向缓存（manager 没有反向 lookup，
			// 这里跑一次 O(N) 把 List() 中每个 client 的 IDByName 映射收齐；
			// cluster 数量数量级 ≤ 10，开销可忽略）。
			byID := map[int64]string{}
			for _, c := range clusters.List() {
				if id := clusters.IDByName(c.Name); id != 0 {
					byID[id] = c.Name
				}
			}
			for _, vm := range rows {
				clusterName := byID[vm.ClusterID]
				if clusterName == "" {
					slog.Warn("trash purger: cluster not found", "vm", vm.Name, "cluster_id", vm.ClusterID)
					continue
				}
				project := "customers" // 与 portal 默认一致
				if err := purgeFn(ctx, clusterName, project, vm.Name); err != nil {
					if isAlreadyGoneErr(err) {
						// Incus 实例早已消失（reconciler 抢先 / 运维手工 incus delete /
						// 创建失败）——视为 hard-delete 成功，落 status='deleted' 即可。
						// 否则 worker 会无限 retry，DB 行永远悬挂在 trash 中。
						slog.Info("trash purger: instance already gone, treating as purged", "vm", vm.Name)
					} else {
						slog.Error("trash purger: purge failed (will retry)", "vm", vm.Name, "error", err)
						continue
					}
				}
				// OPS-052 P0-3 / P1-5：purge 成功（或实例本就消失）后统一收口释放
				// 关联资源——普通 IP 回可用池、Floating IP detach、firewall 绑定解除。
				// best-effort：失败仅告警，仍继续落 DB deleted（个别资源清理失败不应
				// 让行永远悬挂回收站；泄漏的 IP 由 reconciler / RecoverCooldowns 兜底）。
				if reclaimFn != nil {
					if err := reclaimFn(ctx, vm, clusterName, project); err != nil {
						slog.Warn("trash purger: reclaim resources failed (continuing)", "vm", vm.Name, "error", err)
					}
				}
				if err := repo.Delete(ctx, vm.ID); err != nil {
					slog.Error("trash purger: db delete failed", "vm", vm.Name, "error", err)
					continue
				}
				slog.Info("trash purger: vm hard-deleted", "vm", vm.Name)
			}
		}
	}
}
