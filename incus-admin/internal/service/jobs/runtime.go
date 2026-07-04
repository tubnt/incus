package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

const (
	// jobCtxTimeout 是单个 job 执行 ctx 的上限（image-pull / 加节点最长耗时）。
	jobCtxTimeout = 30 * time.Minute
	// staleThreshold 是 sweeper / 启动恢复判定 job 过期的阈值。P1-1：必须 >
	// jobCtxTimeout，否则会把仍在正常运行、尚未触发自身超时的 job 误判为 stale
	// 而中途收尾。留 5min 余量。
	staleThreshold = 35 * time.Minute
	// compensationTimeout 是补偿（Rollback）+ 收尾（finalize）阶段独立 ctx 的
	// 上限。这段逻辑跑在 detached ctx 上，不受 job 自身超时/worker 取消牵连。
	compensationTimeout = 5 * time.Minute
)

// safeRun 以 recover 包裹 fn，捕获任意 panic 记 slog 而不向上传播——
// 保证单个 job / 单个后台任务的 panic 不会拖垮整个 worker goroutine 或进程。
func safeRun(task string, fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("background task panic recovered", "task", task, "panic", rec)
		}
	}()
	fn()
}

// Deps 集中声明 runtime 依赖。每条都是 jobs 必须的能力，调用方在 main 里组装。
type Deps struct {
	Jobs       *repository.ProvisioningJobRepo
	VMs        *repository.VMRepo
	IPAddrs    *repository.IPAddrRepo
	Users      *repository.UserRepo
	Orders     *repository.OrderRepo
	Audit      AuditWriter
	Clusters   *cluster.Manager
	OSTemplates *repository.OSTemplateRepo // 重装时按 slug 查 image_source
	// PLAN-036：vm.create finalize 软失败应用用户默认 firewall_groups。
	// 任一为 nil 时跳过（保持向后兼容 + 测试环境无 firewall service）。
	Firewall    DefaultFirewallApplier
	// PLAN-037：cluster.vm.migrate-batch executor 调用 service.VMService.Migrate；
	// nil 时跳过该 kind（测试可注入 stub）。
	Migrator    VMMigrator
	// PoolSize 是 worker 池容量；建议 4–8。0 取默认 4。
	PoolSize int
	// OPS-050：入队 channel buffer 长度。0 取默认 64。生产建议 256；
	// 满后 Enqueue 阻塞 HTTP（PayWithBalance 已 commit、余额已扣的状态下挂死），
	// 调大 buffer 是最直接的缓解手段，配合 PoolSize 拉宽 worker 池。
	QueueSize int
	// OPS-051 / PLAN-052：vm-create cloud-init 注入的 apt-cacher-ng proxy 全
	// 地址（形如 http://139.162.24.177:3142/）。空 → 不注入。
	AptProxyURL string
	// OPS-051 / PLAN-052：vm-create / reinstall 强制建立的统一登录账号；
	// 空 → "root"（Q7 决策：所有 Linux 默认 root 登录）。
	DefaultLoginUser string
}

// VMMigrator 由 service.VMService 实现。jobs 包通过此接口避免反向 import service
// （service 依赖 jobs 不成立但应保持单向）。
//
// PLAN-039 / OPS-043：mode 透传 auto / live / cold；空字符串 = auto。
type VMMigrator interface {
	Migrate(ctx context.Context, clusterName, project, vmName, targetNode, mode string) (*MigrateOutcome, error)
}

// MigrateOutcome 是 jobs 视角的迁移结果（与 service.MigrateResult 同形），
// 复制一份字段避免 jobs → service 的依赖。
type MigrateOutcome struct {
	WasRunning bool
	Target     string
	Mode       string // 实际走的模式 live/cold
}

// DefaultFirewallApplier 拓窄到 jobs 所需：列出用户默认组 + attach 到 VM。
// service.FirewallService + repository.FirewallRepo 通过 main 的 adapter 实现此接口。
type DefaultFirewallApplier interface {
	ListDefaultGroups(ctx context.Context, userID int64) ([]model.FirewallGroup, error)
	Attach(ctx context.Context, clusterName, project, vmName string, group *model.FirewallGroup) error
	Bind(ctx context.Context, vmID, groupID int64) error
}

// AuditWriter 与 worker.AuditWriter 同形态：jobs 在终态写 vm.provisioning.{started,succeeded,failed} 三段 audit。
type AuditWriter interface {
	Log(ctx context.Context, userID *int64, action, targetType string, targetID int64, details map[string]any, ip string)
}

// Runtime 持有 worker 池 + broker，handler 调 Enqueue 入队，worker 异步跑。
//
// params 是 in-memory job 参数缓存，handler 入队时存入；worker 消费后清除；
// 进程崩溃时丢失没问题 —— sweeper 会把缺失 params 的 stale running job 直接
// 走 rollback 路径（释放 IP / 退款 / cancel order）。
type Runtime struct {
	deps    Deps
	broker  *Broker
	queue   chan int64
	wg      sync.WaitGroup
	// inflight 只统计"已被 worker 领取、正在 runOne 中执行"的 job（不含仍在
	// queue 里排队的）。Shutdown 等它归零 = 真正的 in-flight job 全部收尾，而不是
	// 等 worker goroutine 退出（那要等 ctx cancel，与 graceful 语义相悖）。
	inflight sync.WaitGroup
	once    sync.Once
	pmu     sync.Mutex
	params  map[int64]*Params
	// dispatchFn 在测试里可注入返回桩 Executor；生产留 nil 走 default dispatch。
	dispatchFn func(*model.ProvisioningJob) (Executor, error)
}

func NewRuntime(deps Deps) *Runtime {
	if deps.PoolSize <= 0 {
		deps.PoolSize = 4
	}
	if deps.QueueSize <= 0 {
		deps.QueueSize = 64
	}
	return &Runtime{
		deps:   deps,
		broker: NewBroker(),
		queue:  make(chan int64, deps.QueueSize),
		params: make(map[int64]*Params),
	}
}

func (r *Runtime) Broker() *Broker { return r.broker }

// QueueDepth 返回当前 channel 里堆积的待消费 job 数。
// 给 metrics ticker 与未来 /metrics endpoint 用。
func (r *Runtime) QueueDepth() int { return len(r.queue) }

func (r *Runtime) setParams(jobID int64, p Params) {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	cp := p
	r.params[jobID] = &cp
}

func (r *Runtime) takeParams(jobID int64) *Params {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	p := r.params[jobID]
	delete(r.params, jobID)
	return p
}

func (r *Runtime) peekParams(jobID int64) *Params {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	return r.params[jobID]
}

// Start 启动 N 个 worker goroutine + recovery sweeper（每 5min 扫一次老 row）。
// ctx 取消后 worker 退出；queue 中尚未消费的 job 会被下一次重启时的 sweeper 兜底。
func (r *Runtime) Start(ctx context.Context) {
	r.once.Do(func() {
		// 启动时先做一次 recovery：把上次进程崩溃留下的 running/queued 老 row
		// 标 partial 并触发 rollback。staleThreshold(35min) 留给未崩溃但仍在跑的
		// long job（也避免 HA 多实例部署时误 sweep 另一实例正在跑的 job）。
		r.recoverStale(ctx, staleThreshold)

		for i := 0; i < r.deps.PoolSize; i++ {
			r.wg.Add(1)
			go r.worker(ctx, i)
		}

		go r.sweeper(ctx)
		go r.queueMetrics(ctx)
	})
}

// queueMetrics 周期性打 queue 深度日志。每 30s 一行，仅当队列非空时输出，
// 空载不刷屏。给运维通过 grep 看实时压力用；未来接 Prometheus exporter 时
// 改读 QueueDepth() 即可。
func (r *Runtime) queueMetrics(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			depth := r.QueueDepth()
			if depth > 0 {
				slog.Info("provisioning queue depth",
					"depth", depth,
					"pool_size", r.deps.PoolSize,
					"capacity", cap(r.queue))
			}
		}
	}
}

// Stop 等所有 worker 退出。调用方应先 cancel runtime ctx。
func (r *Runtime) Stop() { r.wg.Wait() }

// Shutdown 是 graceful 退出 entrypoint：先关闭 queue（拒新工），然后等 in-flight
// worker 完成；ctx 超时则放弃等待返 ctx.Err()，调用方据此决定 exit code。
//
// Session-2 F-04：原版 main.go 在 SIGTERM 时只 cancel workerCtx 让 worker 立刻
// 退出（worker select 撞到 ctx.Done() 直接 return），但 in-flight job 在 detached
// 30min ctx 里继续跑——进程随后 os.Exit 把它强杀，DB 留 running 行没人收尾。
// Shutdown 在 worker ctx cancel 之前调，让正在跑的 job 跑完或者超时（默认运维侧
// 30s 上限）。runOne 用 detached ctx 是有意设计——shutdown 不打断业务正确性，
// 只决定我们等多久。
//
// P1（in-flight 计数修正）：原版等 r.wg（worker goroutine 生命周期）。但 Shutdown
// 在 workerCtx cancel 之前调，worker 此刻仍阻塞在 for-select，wg 永不归零 → 每次
// 都空等到超时。改为等 r.inflight（实际在跑的 job），空载时立即返回 clean。
func (r *Runtime) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("provisioning runtime shutdown clean", "in_flight", 0)
		return nil
	case <-ctx.Done():
		slog.Warn("provisioning runtime shutdown timeout; in-flight jobs will be sweeped on next start", "error", ctx.Err())
		return ctx.Err()
	}
}

// Enqueue 把已 INSERT 的 job 推入队列，并暂存 params。queue 满时阻塞等待
// （避免静默丢任务）。handler 拿到 job_id 后立即返 202。
func (r *Runtime) Enqueue(ctx context.Context, jobID int64, params Params) error {
	r.setParams(jobID, params)
	select {
	case <-ctx.Done():
		// ctx 取消，回收 params
		r.takeParams(jobID)
		return ctx.Err()
	case r.queue <- jobID:
		return nil
	}
}

func (r *Runtime) worker(ctx context.Context, idx int) {
	defer r.wg.Done()
	slog.Info("provisioning job worker started", "idx", idx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("provisioning job worker stopping", "idx", idx)
			return
		case jobID := <-r.queue:
			// inflight 在领取 job 时 +1、runOne 返回后 -1，供 Shutdown 精确等待
			// 在跑的 job 收尾。safeRun 兜住 dispatch / MarkRunning / finalize /
			// Rollback 的意外 panic（runOne 内部亦对 executor.Run 单独 recover），
			// 一个 job panic 不干掉 worker goroutine（否则 pool 永久缩容）。
			r.inflight.Add(1)
			safeRun("job-worker", func() { r.runOne(ctx, jobID) })
			r.inflight.Done()
		}
	}
}

// runOne 执行一个 job 的全生命周期：load → MarkRunning → 派发到对应 executor →
// 写终态 → publish terminal event → 关订阅。
//
// 每个 job 用独立的 ctx（base + 30min 超时）+ 不被 worker ctx 取消。这样
// shutdown 时 in-flight job 仍能跑完，避免下一次启动 sweep 把它当 partial。
func (r *Runtime) runOne(parent context.Context, jobID int64) {
	job, err := r.deps.Jobs.GetByID(parent, jobID)
	if err != nil {
		slog.Error("load job failed", "job_id", jobID, "error", err)
		return
	}
	if job == nil {
		slog.Warn("job vanished before run", "job_id", jobID)
		return
	}

	if err := r.deps.Jobs.MarkRunning(parent, jobID); err != nil {
		// 已经 running（多 worker race）或已终态：忽略
		slog.Debug("mark running skipped", "job_id", jobID, "reason", err)
		return
	}

	// audit started —— action label 按 kind 派生，让查询日志的人能按 prefix 过滤：
	//   vm.create / vm.reinstall  → "vm.provisioning.started"
	//   cluster.node.add/remove   → "node.provisioning.started"
	uid := job.UserID
	r.deps.Audit.Log(parent, &uid, auditActionByKind(job.Kind, "started"), "job", jobID, map[string]any{
		"kind":        job.Kind,
		"target_name": job.TargetName,
		"cluster_id":  job.ClusterID,
	}, "")

	// detached ctx：worker shutdown 不取消进行中的 job；jobCtxTimeout 是 image-pull 上限
	jobCtx, cancel := context.WithTimeout(context.Background(), jobCtxTimeout)
	defer cancel()
	// 补偿(Rollback) + 收尾(finalize) 用独立 detached ctx。若 job 因 jobCtx 超时
	// 失败，用 jobCtx 去退款/收尾会因 ctx 已 Done 立刻失败——用户被扣款却退不了。
	// WithoutCancel 保留 value 但不继承 jobCtx 的取消/超时，另配 5min 上限。
	compCtx, compCancel := context.WithTimeout(context.WithoutCancel(jobCtx), compensationTimeout)
	defer compCancel()
	// Session-2 F-39 / PLAN-051 §2-K：集中 cred.Wipe。原版每个 executor 自觉调
	// （cluster_node_add 调，vm_reinstall 漏调）；改在 runOne defer 里统一兜底。
	// takeParams 已是幂等：executor 自己 take 之后 Wipe 后再次 take 返 nil 跳过。
	defer func() {
		if p := r.takeParams(jobID); p != nil && p.Credential != nil {
			p.Credential.Wipe()
		}
	}()

	exec, err := r.dispatch(job)
	if err != nil {
		r.finalize(compCtx, job, model.JobStatusFailed, err.Error())
		return
	}

	// 包一层 panic 兜底防 worker goroutine 永久死掉（pma-cr MEDIUM）。
	// 任何 executor 内未捕获 panic → finalize failed + Rollback。
	runErr := func() (rerr error) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("provisioning executor panic", "job_id", job.ID, "panic", rec)
				rerr = fmt.Errorf("executor panic: %v", rec)
			}
		}()
		return exec.Run(jobCtx, r, job)
	}()
	if runErr != nil {
		// pma-cr CRITICAL：Run 失败必须先做补偿（释放 IP / 退款 / 取消订单 /
		// 删残留 instance），再写终态。原版只 finalize failed 不调 Rollback ——
		// 用户被扣款 VM 没建成也没退款。用 compCtx（detached）确保 job 超时后
		// 退款/收尾仍能执行。
		exec.Rollback(compCtx, r, job, runErr.Error())
		r.finalize(compCtx, job, model.JobStatusFailed, runErr.Error())
		return
	}

	r.finalize(compCtx, job, model.JobStatusSucceeded, "")
}

func (r *Runtime) finalize(ctx context.Context, job *model.ProvisioningJob, status, errMsg string) {
	if err := r.deps.Jobs.Finish(ctx, job.ID, status, errMsg); err != nil {
		slog.Error("finalize job failed", "job_id", job.ID, "error", err)
	}
	uid := job.UserID
	suffix := "succeeded"
	details := map[string]any{
		"kind":        job.Kind,
		"target_name": job.TargetName,
	}
	if status != model.JobStatusSucceeded {
		suffix = "failed"
		details["error"] = errMsg
	}
	r.deps.Audit.Log(ctx, &uid, auditActionByKind(job.Kind, suffix), "job", job.ID, details, "")

	// publish terminal so SSE 订阅者收到后断开
	r.broker.Publish(StepEvent{JobID: job.ID, Terminal: true, Status: status})
}

// auditActionByKind 按 job.Kind 派生 audit action label，让 audit grep
// 能按 vm.* / node.* 过滤。suffix 应为 started / succeeded / failed。
func auditActionByKind(kind, suffix string) string {
	switch kind {
	case model.JobKindClusterNodeAdd, model.JobKindClusterNodeRemove:
		return "node.provisioning." + suffix
	case model.JobKindClusterVMMigrateBatch:
		return "vm.migrate-batch." + suffix
	default:
		return "vm.provisioning." + suffix
	}
}

func (r *Runtime) dispatch(job *model.ProvisioningJob) (Executor, error) {
	if r.dispatchFn != nil {
		return r.dispatchFn(job)
	}
	switch job.Kind {
	case model.JobKindVMCreate:
		return &vmCreateExecutor{}, nil
	case model.JobKindVMReinstall:
		return &vmReinstallExecutor{}, nil
	case model.JobKindClusterNodeAdd:
		return &clusterNodeAddExecutor{}, nil
	case model.JobKindClusterNodeRemove:
		return &clusterNodeRemoveExecutor{}, nil
	case model.JobKindClusterVMMigrateBatch:
		return &vmMigrateBatchExecutor{}, nil
	default:
		return nil, fmt.Errorf("unknown job kind %q", job.Kind)
	}
}

// sweeper 周期性扫超过 staleThreshold 的 running job，标 partial 并触发 rollback。
// 与 healing_expire 同模式：5min tick，比 35min 阈值密 7 倍，最多 5min 漂移。
// safeRun 兜住 recoverStale 内的意外 panic，避免 sweeper goroutine 静默死掉后
// stale job 再也没人收。
func (r *Runtime) sweeper(ctx context.Context) {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			safeRun("job-sweeper", func() { r.recoverStale(ctx, staleThreshold) })
		}
	}
}

func (r *Runtime) recoverStale(ctx context.Context, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	stale, err := r.deps.Jobs.FindStaleRunning(ctx, cutoff)
	if err != nil {
		slog.Error("find stale jobs failed", "error", err)
		return
	}
	for _, j := range stale {
		j := j
		slog.Warn("recovering stale provisioning job", "job_id", j.ID, "kind", j.Kind, "started_at", j.StartedAt)
		// 标 partial 让 finalize 走 fail 分支 + 触发 audit；rollback 走 dispatch。
		// Finish 的 status IN 守卫保证：若该 job 已被 worker 正常收尾（竞态），此处
		// 命中 0 行、不覆盖终态；后续 rollback 自身幂等/best-effort，重复执行安全。
		if err := r.deps.Jobs.Finish(ctx, j.ID, model.JobStatusPartial, "recovered after process restart or stale > 35m"); err != nil {
			slog.Error("finalize stale failed", "job_id", j.ID, "error", err)
			continue
		}
		// 派发同一 executor 的 Rollback 路径（创建态已部分写入资源时清扫）
		if exec, derr := r.dispatch(&j); derr == nil {
			rec := j
			exec.Rollback(ctx, r, &rec, "stale recovery")
		}
		r.deps.Audit.Log(ctx, &j.UserID, auditActionByKind(j.Kind, "failed"), "job", j.ID, map[string]any{
			"kind":   j.Kind,
			"reason": "stale recovery",
		}, "")
		r.broker.Publish(StepEvent{JobID: j.ID, Terminal: true, Status: model.JobStatusPartial})
	}
}

// step 是 executor 内部的进度推送辅助。封装 AppendStep + Publish 双写，
// 失败仅记录 slog 不中断流程（broker 是尽力而为，DB 是真源）。
func (r *Runtime) step(ctx context.Context, jobID int64, seq int, name, status, detail string) {
	step, err := r.deps.Jobs.AppendStep(ctx, jobID, seq, name, status, detail)
	if err != nil {
		slog.Error("append step failed", "job_id", jobID, "seq", seq, "error", err)
		return
	}
	r.broker.Publish(StepEvent{JobID: jobID, Step: *step})
}

// finishStep 把 running step 翻到终态并推送。
func (r *Runtime) finishStep(ctx context.Context, jobID int64, seq int, name, status, detail string) {
	if err := r.deps.Jobs.UpdateStep(ctx, jobID, seq, status, detail); err != nil {
		slog.Error("update step failed", "job_id", jobID, "seq", seq, "error", err)
	}
	// 重新拉一次拿最新的 completed_at，简化下游
	steps, err := r.deps.Jobs.ListSteps(ctx, jobID, seq-1)
	if err != nil || len(steps) == 0 {
		return
	}
	for _, s := range steps {
		if s.Seq == seq {
			r.broker.Publish(StepEvent{JobID: jobID, Step: s})
			return
		}
	}
}
