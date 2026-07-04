package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/prometheus/client_golang/prometheus"

	authcore "github.com/incuscloud/incus-admin/internal/auth"
	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/config"
	adminhandler "github.com/incuscloud/incus-admin/internal/handler/admin"
	authhandler "github.com/incuscloud/incus-admin/internal/handler/auth"
	"github.com/incuscloud/incus-admin/internal/handler/openapi"
	"github.com/incuscloud/incus-admin/internal/handler/portal"
	"github.com/incuscloud/incus-admin/internal/handler/promexport"
	v1handler "github.com/incuscloud/incus-admin/internal/handler/v1"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/observability"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/server"
	"github.com/incuscloud/incus-admin/internal/service"
	"github.com/incuscloud/incus-admin/internal/service/aiassist"
	"github.com/incuscloud/incus-admin/internal/service/billing"
	"github.com/incuscloud/incus-admin/internal/service/jobs"
	"github.com/incuscloud/incus-admin/internal/service/notify"
	"github.com/incuscloud/incus-admin/internal/worker"
)

func main() {
	// PLAN-043 / INFRA-011 bootstrap 子命令分支：args[1] == "bootstrap" 时走
	// runBootstrap（cobra 子命令树 detect/first-node/apply），否则走 runServer。
	// 现有 server 启动行为零改动，向后兼容 systemd unit + 容器入口。
	if len(os.Args) > 1 && os.Args[1] == "bootstrap" {
		runBootstrap(os.Args[2:])
		return
	}
	runServer()
}

func runServer() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// PLAN-051 §2-A / Session-1 W1/W2/W3：production 必须显式落地三项关键
	// 安全配置；任一缺失启动 fail，避免"运维忘配 → 直接裸奔"。dev/test 维持
	// 历史 passthrough 兼容。fail 信息含具体补救命令。
	if err := requireProductionSecurity(cfg); err != nil {
		slog.Error("startup refused: production security misconfigured", "error", err)
		os.Exit(1)
	}

	// 把 ENV 注入 repository 层让 encryptForWrite 在 production 走 fail-closed
	repository.SetEnv(cfg.Server.Env)

	slog.Info("incus-admin starting",
		"listen", cfg.Server.Listen,
		"domain", cfg.Server.Domain,
		"clusters", len(cfg.Clusters),
	)

	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	if err := db.Ping(); err != nil {
		slog.Error("failed to ping database", "error", err)
		os.Exit(1)
	}
	slog.Info("database connected")

	userRepo := repository.NewUserRepo(db)
	clusterRepo := repository.NewClusterRepo(db)

	var clusterMgr *cluster.Manager
	var scheduler *cluster.Scheduler
	var vmSvc *service.VMService

	// PLAN-027 / INFRA-003：DB-driven cluster config。env cfg.Clusters 仅作 bootstrap，
	// 启动时持久化进 clusters 表（CreateFull 含 cert/kind/projects/pools），之后 DB
	// 是源。Admin 通过 UI add 的 cluster / standalone host 重启后仍在。
	bootCtx := context.Background()
	if len(cfg.Clusters) > 0 {
		upsertEnvClusters(bootCtx, clusterRepo, cfg.Clusters)
	}
	dbClusters, dbLoadErr := loadClustersFromDB(bootCtx, clusterRepo)
	if dbLoadErr != nil {
		slog.Error("load clusters from DB failed; falling back to env", "error", dbLoadErr)
		dbClusters = cfg.Clusters
	}

	if len(dbClusters) > 0 {
		// SPKI pin store backed by clusters.tls_fingerprint (migration 006).
		// TOFU on first connect, refuses peers that diverge from the learned pin.
		pinStore := &clusterPinStore{repo: clusterRepo}
		clusterMgr, err = cluster.NewManager(dbClusters, pinStore)
		if err != nil {
			slog.Warn("cluster manager init failed, running without clusters", "error", err)
		} else {
			// Populate ID↔Name maps from DB rows
			for _, cc := range dbClusters {
				id, upErr := clusterRepo.Upsert(bootCtx, cc.Name, cc.DisplayName, cc.APIURL)
				if upErr != nil {
					slog.Error("cluster upsert failed", "name", cc.Name, "error", upErr)
					continue
				}
				clusterMgr.SetID(cc.Name, id)
			}
			scheduler = cluster.NewScheduler(clusterMgr)
			vmSvc = service.NewVMService(clusterMgr)
			slog.Info("cluster manager ready", "clusters", len(clusterMgr.List()))

			// PLAN-021 Phase F+G follow-up: ensure customer-facing projects
			// allow cluster-member targeting so admin Migrate isn't blocked
			// by Incus' default `restricted.cluster.target=block`. Best-effort
			// per cluster + project; failures are logged not fatal.
			ensureCustomerProjectAllowsClusterTarget(clusterMgr)
		}
	}

	userLookup := func(ctx context.Context, email string) (int64, string, error) {
		user, err := userRepo.FindOrCreate(ctx, email, email, "", cfg.Auth.AdminEmails)
		if err != nil {
			return 0, "", err
		}
		return user.ID, user.Role, nil
	}

	roleLookup := func(ctx context.Context, userID int64) (string, error) {
		user, err := userRepo.GetByID(ctx, userID)
		if err != nil || user == nil {
			return "", fmt.Errorf("user not found")
		}
		return user.Role, nil
	}

	balanceLookup := func(ctx context.Context, userID int64) (float64, error) {
		user, err := userRepo.GetByID(ctx, userID)
		if err != nil || user == nil {
			return 0, fmt.Errorf("user not found")
		}
		return user.Balance, nil
	}

	vmRepo := repository.NewVMRepo(db)
	sshKeyRepo := repository.NewSSHKeyRepo(db)
	ticketRepo := repository.NewTicketRepo(db)
	productRepo := repository.NewProductRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	// PLAN-054 / INFRA-013：vm_subscriptions 记账。订单流写入 + VM trash/restore 联动。
	subRepo := repository.NewSubscriptionRepo(db)
	// PLAN-054 / INFRA-013 H：billing_charges 记账（charger worker + reactivate hook 共用）。
	chargeRepo := repository.NewChargeRepo(db)
	auditRepo := repository.NewAuditRepo(db)
	apiTokenRepo := repository.NewAPITokenRepo(db)
	nodeCredRepo := repository.NewNodeCredentialRepo(db)
	invoiceRepo := repository.NewInvoiceRepo(db)
	quotaRepo := repository.NewQuotaRepo(db)
	alertRepo := repository.NewSystemAlertRepo(db) // PLAN-039 / OPS-044
	// PLAN-041 / INFRA-009 监控告警闭环
	alertRuleRepo := repository.NewAlertRuleRepo(db)
	alertDeliveryRepo := repository.NewAlertDeliveryRepo(db)
	notifyChannelRepo := repository.NewNotifyChannelRepo(db)
	notifyRegistry := notify.NewRegistry()
	// PLAN-025 / INFRA-007 jobRepo 提前到此（PLAN-041 evaluator 需 CountFailedJobsSince）
	jobRepo := repository.NewProvisioningJobRepo(db)

	// PLAN-041 / INFRA-009 业务指标 collector + Prometheus exposition 端点。
	// 默认匿名（决策 D13 = A），METRICS_BEARER_TOKEN 非空时强制 Bearer。
	// StartRefresh 用 workerCtx 启动后台 30s 刷新；workerCtx 在下面创建后再调。
	promRegistry := prometheus.NewRegistry()
	bizMetrics := observability.New()
	bizMetrics.Register(promRegistry)
	bizMetrics.SetBuildInfo("dev", "", cfg.Server.Env)
	promExportHandler := promexport.New(promRegistry, os.Getenv("METRICS_BEARER_TOKEN"))

	ipAddrRepo := repository.NewIPAddrRepo(db)
	healingRepo := repository.NewHealingEventRepo(db)
	osTemplateRepo := repository.NewOSTemplateRepo(db)
	firewallRepo := repository.NewFirewallRepo(db)
	floatingIPRepo := repository.NewFloatingIPRepo(db)
	// PLAN-053 Phase E：cloud-gateway /v1 写操作幂等缓存。仓储 + middleware +
	// 24h cleanup worker 协同工作；缺一不可。middleware 接入见下方 Handlers.Idempotency。
	idempotencyRepo := repository.NewIdempotencyRepo(db)
	portal.SetAuditRepo(auditRepo)
	portal.SetIPAddrRepo(ipAddrRepo)
	portal.SetUserRepo(userRepo)
	portal.SetHealingRepo(healingRepo)
	portal.SetOSTemplateRepo(osTemplateRepo)
	portal.SetAppEnv(cfg.Server.Env)
	middleware.SetEmergencySecret(cfg.Auth.EmergencyToken)

	// OPS-022：vms.password 字段 AES-256-GCM 加密。空 key → passthrough。
	if err := authcore.SetPasswordEncryptionKey(cfg.Auth.PasswordEncryptionKey); err != nil {
		slog.Error("password encryption init failed", "error", err)
		os.Exit(1)
	}
	// 启动时把所有还是明文的 vms.password 行加密 in-place。Idempotent：v1: 前缀的跳过。
	// 生产首次部署时跑一次；之后每次启动都跑（迁移完成后扫到 0 行，毫秒级）。
	if authcore.PasswordEncryptionEnabled() {
		go migrateVMPasswordsToEncrypted(db) // 不阻塞启动；异步跑 + 失败 log
	}

	middleware.SetTokenValidator(func(ctx context.Context, token string) (int64, error) {
		t, err := apiTokenRepo.ValidateToken(ctx, token)
		if err != nil || t == nil {
			return 0, fmt.Errorf("invalid token")
		}
		return t.UserID, nil
	})

	// pma-cr M-2 / Session-1 O3：PKCE pkceStore 是进程内 map（state→verifier）。
	// 多实例部署下 callback 可能命中无 verifier 的实例 → step-up 失败用户无诊断。
	// 启动 warn 提醒：DEPLOYMENT_TOPOLOGY=multi-instance 时让运维注意，
	// 同步把 pkce store 升级到 DB / Redis（OPS-047 跟踪）。
	if topo := strings.ToLower(strings.TrimSpace(os.Getenv("DEPLOYMENT_TOPOLOGY"))); topo == "multi-instance" {
		slog.Warn("DEPLOYMENT_TOPOLOGY=multi-instance detected: in-memory pkceStore + emergency lock buckets are not shared between instances; step-up may fail when callback hits a different instance. See OPS-047 for shared store migration.")
	}

	// Step-up OIDC is optional; absence just disables sensitive-route protection.
	// Init is outside the clusterMgr block because step-up has no cluster dependency.
	var stepUpHandler server.StepUpHandler
	// stepUpFailClosed：OIDC 已配置但 discovery 失败时置 true，令敏感路由 fail-closed
	// 拒绝（见 middleware.RequireRecentAuthOnSensitive / PLAN-055 §5）。
	var stepUpFailClosed bool
	if cfg.Auth.OIDCIssuer != "" && cfg.Auth.OIDCClientID != "" && cfg.Auth.OIDCClientSecret != "" && cfg.Auth.StepUpCallbackURL != "" {
		// PLAN-055 / OPS-052 §5：discovery 加 10s 超时。此前用 context.Background()
		// 无超时，IdP 网络抖动会把整个进程启动无限期挂住。
		oidcCtx, oidcCancel := context.WithTimeout(context.Background(), 10*time.Second)
		oidcClient, oidcErr := authcore.NewOIDCClient(oidcCtx,
			cfg.Auth.OIDCIssuer, cfg.Auth.OIDCClientID, cfg.Auth.OIDCClientSecret, cfg.Auth.StepUpCallbackURL)
		oidcCancel()
		if oidcErr != nil {
			// PLAN-055 / OPS-052 §5：OIDC 已配置却 discovery 失败 → fail-closed。
			// 不再静默降级为 permissive（那会让 step-up 保护被 discovery 故障绕过），
			// 而是让敏感操作全部被拒（503），逼运维修复 OIDC 后重启恢复。
			slog.Error("step-up OIDC discovery failed; sensitive operations will be REJECTED (fail-closed) until OIDC recovers",
				"issuer", cfg.Auth.OIDCIssuer, "error", oidcErr)
			stepUpFailClosed = true
		} else {
			stateSecret := cfg.Auth.StepUpStateSecret
			if stateSecret == "" {
				// Reuse the session secret — saves an env var for single-node deploys.
				stateSecret = cfg.Server.SessionSecret
			}
			stepUpHandler = authhandler.NewHandler(oidcClient, userRepo, stateSecret)
			slog.Info("step-up OIDC ready", "issuer", cfg.Auth.OIDCIssuer, "max_age", cfg.Auth.StepUpMaxAge)
		}
	} else {
		slog.Info("step-up OIDC not configured, sensitive-route protection disabled")
	}

	// StepUpLookup stays nil when OIDC is disabled (middleware becomes a no-op).
	var stepUpLookup server.StepUpLookup
	if stepUpHandler != nil {
		stepUpLookup = userRepo.GetStepUpAuthAt
	}

	// Route-level audit writer wraps AuditRepo.Log; signature matches
	// server.AuditWriter. Every /api/admin write gets a coarse-grained row.
	auditWriter := server.AuditWriter(auditRepo.Log)

	// Shadow-session signing secret falls back to SessionSecret for
	// single-node deploys. Handler + cookie verifier share one key so the
	// verify path matches what LoginAdmin signs.
	shadowSecret := cfg.Auth.ShadowSessionSecret
	if shadowSecret == "" {
		shadowSecret = cfg.Server.SessionSecret
	}
	middleware.SetShadowVerifier(func(cookieValue string) (int64, string, int64, string, error) {
		c, err := authcore.VerifyShadow([]byte(shadowSecret), cookieValue)
		if err != nil {
			return 0, "", 0, "", err
		}
		return c.ActorID, c.ActorEmail, c.TargetID, c.TargetEmail, nil
	})
	shadowHandler := adminhandler.NewShadowHandler(userRepo, shadowSecret, func(actorID int64, action, targetType string, targetID int64, details map[string]any, ip string) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var uid *int64
		if actorID > 0 {
			uid = &actorID
		}
		auditRepo.Log(bgCtx, uid, action, targetType, targetID, details, ip)
	})

	// workerCtx is the parent context for every long-running background
	// goroutine (cleanup loops, reconciler, event listener, expire-stale
	// sweeper). srv.Run() blocks until SIGINT/SIGTERM + HTTP drain finishes
	// then returns, at which point the deferred cancel fires and workers
	// exit cleanly via their select on ctx.Done(). Using context.Background()
	// per worker worked, but left them alive until os.Exit — harmless on
	// systemd but prevented clean shutdown in tests.
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()

	// PLAN-041 / INFRA-009 业务指标后台 30s 刷新。挂在 workerCtx 之后避免引用未定义。
	bizMetrics.StartRefresh(workerCtx, db, 30*time.Second)

	// Retention worker: deletes audit_logs rows older than
	// AUDIT_RETENTION_DAYS (default 365). Runs until server shutdown.
	go worker.RunAuditCleanup(workerCtx, auditRepo, cfg.Auth.AuditRetentionDays)

	// API token cleanup: removes expired rows after a 30d grace period so
	// audit cross-references survive short investigations.
	go worker.RunAPITokenCleanup(workerCtx, apiTokenRepo, 30*24*time.Hour)

	// PLAN-053 Phase E：idempotency_keys 24h TTL cleanup。每小时 (~第 7 分) 跑一次，
	// 配合 middleware.Idempotency 共同实现 cloud-gateway 标准幂等。
	go worker.RunIdempotencyCleanup(workerCtx, idempotencyRepo, time.Hour)

	// PLAN-020 Phase A: VM state reverse-sync worker. Polls each Incus
	// cluster every 60s, diffs against active `vms` rows, and flips rows
	// that have vanished from Incus to status='gone' while releasing their
	// IPs. Only starts when a cluster manager exists (skipped in DB-only
	// test/dev environments).
	if clusterMgr != nil {
		snapshotFn := worker.ClusterSnapshotFromManager(clusterMgr, "customers")
		// OPS-052 P1-2：CreateBuffer 显式设 30s。配合 repo.ListActiveForReconcile
		// 已排除 'creating' 状态，双重保证刚 provision 的 VM 不被误判 gone（进而
		// 误 Release 掉刚分配的 IP 引发双分配）。
		reconcileCfg := worker.VMReconcilerConfig{Interval: 60 * time.Second, CreateBuffer: 30 * time.Second}
		go worker.RunVMReconciler(
			workerCtx,
			reconcileCfg,
			snapshotFn,
			vmRepo,
			ipAddrRepo,
			auditRepo,
		)

		// PLAN-020 Phase C: event-driven listener. Each cluster gets a
		// goroutine that subscribes to /1.0/events (lifecycle + cluster)
		// over WebSocket. Faster than the 60s reconciler for in-band
		// changes; reconciler stays as safety net + initial catch-up on
		// reconnect.
		streamFn := func() []worker.ClusterStream {
			out := make([]worker.ClusterStream, 0)
			for _, c := range clusterMgr.List() {
				tlsCfg, err := clusterMgr.TLSConfigForCluster(c.Name)
				if err != nil {
					slog.Warn("event listener: skip cluster, no tls", "cluster", c.Name, "error", err)
					continue
				}
				out = append(out, worker.ClusterStream{
					ID:     clusterMgr.IDByName(c.Name),
					Name:   c.Name,
					APIURL: c.APIURL,
					TLS:    tlsCfg,
				})
			}
			return out
		}
		// On reconnect, trigger a single reconcile pass so any drift that
		// accumulated during the outage surfaces quickly.
		reconcileOnDemand := func(ctx context.Context) {
			worker.RunVMReconcilerOnce(ctx, reconcileCfg, snapshotFn, vmRepo, ipAddrRepo, auditRepo)
		}
		// Phase D.2/D.3 healing adapter bridges worker.HealingTracker to
		// HealingEventRepo without leaking repository types into the worker
		// package.
		healingAdapter := healingTrackerAdapter{repo: healingRepo}
		go worker.RunEventListener(
			workerCtx,
			worker.EventListenerConfig{},
			streamFn,
			vmRepo,
			healingAdapter,
			reconcileOnDemand,
		)

		// PLAN-020 Phase D.3 sweeper: any healing_events row left in
		// 'in_progress' for more than 15 minutes is likely the consequence
		// of an Incus event we never received (network glitch, crash).
		// Flip to 'partial' so the history UI doesn't show stuck entries.
		go worker.RunHealingExpireStale(workerCtx, healingRepo, 15*time.Minute, 5*time.Minute)

		// PLAN-034: VM trash purger — 5s tick，30s 窗口过后接管 hard-delete。
		// 复用 service.VMService.PurgeTrashed（语义即原 Delete 路径），manager
		// 提供 ID→name 反查。worker 会同时把 DB 行翻 status='deleted'。
		trashWindow := time.Duration(model.VMTrashWindowSeconds) * time.Second
		// OPS-052 P0-3 / P1-5：purge 收口时释放 VM 关联的全部网络资源。以闭包注入，
		// 让 worker 包不反向依赖 repository/service；逐项 best-effort，个别失败仅告警，
		// 不阻断 DB 落 deleted（泄漏的 IP 由 AllocateNext 内联 RecoverCooldowns 兜底）。
		floatingIPSvcForReclaim := service.NewFloatingIPService(clusterMgr)
		reclaimVMResources := func(ctx context.Context, vm model.VM, clusterName, project string) error {
			// 1) 普通 IP 回可用池：Release 置 cooldown，过窗口后由 AllocateNext 回收。
			if vm.IP != nil && *vm.IP != "" {
				if err := ipAddrRepo.Release(ctx, *vm.IP); err != nil {
					slog.Warn("reclaim: release ip failed", "vm", vm.Name, "ip", *vm.IP, "error", err)
				}
			}
			// 2) Floating IP detach：先 best-effort 复原 Incus NIC 过滤（实例通常已删，
			//    忽略其错误），再把 DB 行切回 available 供复用。
			if fips, err := floatingIPRepo.ListByVM(ctx, vm.ID); err != nil {
				slog.Warn("reclaim: list floating ips failed", "vm", vm.Name, "error", err)
			} else {
				for _, fip := range fips {
					_, _ = floatingIPSvcForReclaim.DetachFromVM(ctx, clusterName, project, vm.Name, fip.IP)
					if _, derr := floatingIPRepo.Detach(ctx, fip.ID); derr != nil {
						slog.Warn("reclaim: detach floating ip failed", "vm", vm.Name, "fip_id", fip.ID, "error", derr)
					}
				}
			}
			// 3) firewall 绑定解除：VM 即将 hard-delete，只需清 vm_firewall_bindings 行。
			if groups, err := firewallRepo.ListBindingsByVM(ctx, vm.ID); err != nil {
				slog.Warn("reclaim: list firewall bindings failed", "vm", vm.Name, "error", err)
			} else {
				for _, g := range groups {
					if uerr := firewallRepo.Unbind(ctx, vm.ID, g.ID); uerr != nil {
						slog.Warn("reclaim: unbind firewall failed", "vm", vm.Name, "group_id", g.ID, "error", uerr)
					}
				}
			}
			return nil
		}
		go worker.RunVMTrashPurger(workerCtx, vmRepo, clusterMgr, vmSvc.PurgeTrashed, reclaimVMResources, trashWindow, 5*time.Second)

		// PLAN-039 / OPS-044: imbalance watchdog（仅告警，不自动迁移）
		// 5min tick × 3 ticks = 15min 持续不均衡 → 写 system_alerts。
		go worker.RunImbalanceWatchdog(workerCtx, scheduler, clusterMgr, vmRepo, alertRepo, 5*time.Minute, 3)

		// PLAN-041 / INFRA-009 监控告警闭环 worker：
		//   - evaluator 1min tick：拉 enabled rules 评估，触发 → upsert system_alerts
		//     + enqueue alert_deliveries
		//   - dispatcher 30s tick：扫 pending deliveries → sender.Send → 重试/失败
		//   - cleanup  24h tick：清 90 天前 alert_deliveries 行
		go worker.RunAlertEvaluator(workerCtx, worker.EvaluatorDeps{
			Rules:      alertRuleRepo,
			Alerts:     alertRepo,
			Deliveries: alertDeliveryRepo,
			VMs:        vmDownAdapter{repo: vmRepo},
			Users:      userBalanceAdapter{repo: userRepo},
			Jobs:       jobFailureAdapter{repo: jobRepo},
			Orders:     orderFailureAdapter{repo: orderRepo},
			Nodes:      offlineNodeAdapter{scheduler: scheduler, mgr: clusterMgr},
		}, 1*time.Minute)
		go worker.RunAlertDispatcher(workerCtx, worker.DispatcherDeps{
			Deliveries: alertDeliveryRepo,
			Channels:   notifyChannelRepo,
			Alerts:     alertRepo,
			Registry:   notifyRegistry,
		}, 30*time.Second, 50)
		go worker.RunAlertDeliveriesCleanup(workerCtx, alertDeliveryRepo, 90)

		// One-shot startup reconcile: ensure every DB firewall_groups row
		// has a matching Incus network ACL. Migration 011 INSERTs seed rows
		// (default-web / ssh-only / database-lan) but only handler.CreateGroup
		// calls EnsureACL, so seed groups never get pushed to Incus. Without
		// this, vm bind silently fails (Incus accepts unknown ACL name in
		// PUT but doesn't apply). Idempotent.
		go worker.ReconcileFirewallOnce(workerCtx, &firewallReconcileAdapter{
			repo: firewallRepo,
			svc:  service.NewFirewallService(clusterMgr, vmSvc),
		})
	}

	// PLAN-054 / INFRA-013 Phase H：billing 计费引擎 —— charger + grace_expire
	// 两个 worker + topup reactivate hook 共用一个 service.Service。
	// Enabled=false 时全跳过（charger 不 spawn，UserHandler 不注入 reactivator）。
	var billingSvc *billing.Service
	if cfg.Billing.Enabled {
		billingSvc = billing.NewService(
			db, subRepo, chargeRepo,
			billingVMTrasher{repo: vmRepo},
			billingAuditAdapter{repo: auditRepo},
			billing.WithGraceDuration(cfg.Billing.GraceDuration),
		)
		// charger 从启动立即跑一次 catchup，之后每 ChargerInterval tick。
		go worker.RunBillingDailyCharger(
			workerCtx,
			billingChargerAdapter{svc: billingSvc},
			cfg.Billing.ChargerInterval,
		)
		// grace_expire 错开 15min 起跑（charger 已先扫一遍可能触发的 suspended），
		// 之后每 GraceInterval tick。避免与 charger 在同一秒抢 users.balance 锁。
		go worker.RunBillingGraceExpire(
			workerCtx,
			billingGracerAdapter{svc: billingSvc},
			cfg.Billing.GraceInterval,
			15*time.Minute,
		)
		slog.Info("billing engine enabled",
			"charger_interval", cfg.Billing.ChargerInterval,
			"grace_interval", cfg.Billing.GraceInterval,
			"grace_duration", cfg.Billing.GraceDuration,
		)
	} else {
		slog.Info("billing engine disabled (INCUS_ADMIN_BILLING_ENABLED=false)")
	}

	// PLAN-025 / INFRA-007 异步 provisioning runtime。clusterMgr 为 nil 时
	// （DB-only 测试 / 配置缺失）跳过启动；handler 走兜底同步路径。
	// jobRepo 已在上文提前创建（PLAN-041 evaluator 共用）。
	var jobsRuntime *jobs.Runtime
	if clusterMgr != nil {
		jobsRuntime = jobs.NewRuntime(jobs.Deps{
			Jobs:        jobRepo,
			VMs:         vmRepo,
			IPAddrs:     ipAddrRepo,
			Users:       userRepo,
			Orders:      orderRepo,
			Audit:       auditAdapter{repo: auditRepo},
			Clusters:    clusterMgr,
			OSTemplates: osTemplateRepo,
			Firewall:    defaultFirewallApplier{repo: firewallRepo, svc: service.NewFirewallService(clusterMgr, vmSvc)},
			Migrator:    vmMigratorAdapter{svc: vmSvc}, // PLAN-037: cluster.vm.migrate-batch
			PoolSize:    cfg.Jobs.PoolSize,             // OPS-050: env JOBS_POOL_SIZE，默认 4
			QueueSize:   cfg.Jobs.QueueSize,            // OPS-050: env JOBS_QUEUE_SIZE，默认 64
			// OPS-051 / PLAN-052
			AptProxyURL:      cfg.Provisioning.AptProxyURL,
			DefaultLoginUser: cfg.Provisioning.DefaultLoginUser,
		})
		jobsRuntime.Start(workerCtx)
		slog.Info("provisioning jobs runtime started",
			"pool_size", cfg.Jobs.PoolSize,
			"queue_size", cfg.Jobs.QueueSize,
			"apt_proxy", cfg.Provisioning.AptProxyURL,
			"default_login_user", cfg.Provisioning.DefaultLoginUser)
	}

	adminVMHandler := portal.NewAdminVMHandler(vmSvc, vmRepo, sshKeyRepo, clusterMgr, scheduler).
		WithSubscriptions(subRepo) // PLAN-054 trash/restore 联动
	portalVMHandler := portal.NewVMHandler(vmSvc, vmRepo, sshKeyRepo, clusterMgr).
		WithSubscriptions(subRepo) // PLAN-054 trash/restore 联动
	orderHandler := portal.NewOrderHandler(orderRepo, productRepo, vmSvc, vmRepo, sshKeyRepo, clusterMgr).
		WithQuotas(quotaRepo).     // OPS-021：购买前 quota 强制
		WithSubscriptions(subRepo) // PLAN-054：订单 pay → vm_subscriptions 行写入
	// PLAN-038 / OPS-041 Phase B/C：AI provider —— anthropic / disabled 二选一。
	// AIConfig.Provider == "disabled"（默认）→ disabledProvider，所有 AI endpoint
	// 返 503，前端按需隐藏入口。生产没买 API key 也能正常跑。
	aiProvider := buildAIProvider(cfg.AI)

	clusterMgmtHandler := portal.NewClusterMgmtHandler(clusterMgr).
		WithPersistence(clusterRepo).
		WithNodeCredentials(nodeCredRepo).
		WithTopology(scheduler, vmRepo). // PLAN-037 / OPS-040
		WithAlerts(alertRepo).           // PLAN-039 / OPS-044
		WithAIProvider(aiProvider)       // PLAN-038 / OPS-041 Tier 2
	// PLAN-051 §2-K / Session-2 F-13 + F-14：aiRate 与 probeCache 内存 map
	// 后台周期 GC，防止低活跃用户 entry / 过期 probe 记录永久驻留 → 内存线性增长。
	clusterMgmtHandler.StartAIRateGC(workerCtx, 30*time.Minute)
	clusterMgmtHandler.StartProbeCacheGC(workerCtx, time.Minute)
	if jobsRuntime != nil {
		adminVMHandler.WithJobs(jobsRuntime, jobRepo)
		portalVMHandler.WithJobs(jobsRuntime, jobRepo)
		orderHandler.WithJobs(jobsRuntime, jobRepo)
		// PLAN-026 / INFRA-002：注入节点编排依赖。SSH 凭据复用 Ceph 维护通道
		// 同一对私钥（cfg.Monitor.CephSSHKey）—— 与 nodeops test-ssh / exec 一致。
		clusterMgmtHandler.WithNodeOrchestration(
			jobsRuntime, jobRepo,
			cfg.Monitor.CephSSHUser,
			cfg.Monitor.CephSSHKey,
			cfg.Monitor.SSHKnownHostsFile,
		)
	}

	srv := server.New(cfg, userLookup, roleLookup, balanceLookup, stepUpLookup, stepUpFailClosed, auditWriter, server.Handlers{
		Admin:           adminVMHandler,
		Portal:          portalVMHandler,
		Users:           userHandlerWithBilling(portal.NewUserHandler(userRepo), billingSvc),
		IPPools:         portal.NewIPPoolHandler(clusterMgr),
		Console:         portal.NewConsoleHandler(clusterMgr, vmRepo),
		Snaps:           portal.NewSnapshotHandler(clusterMgr, vmRepo),
		Metrics:         portal.NewMetricsHandler(clusterMgr, vmRepo),
		SSHKeys:         portal.NewSSHKeyHandler(sshKeyRepo),
		Tickets:         portal.NewTicketHandler(ticketRepo),
		Products:        portal.NewProductHandler(productRepo),
		Orders:          orderHandler,
		Subscriptions:   portal.NewSubscriptionHandler(subRepo),
		Audit:           portal.NewAuditHandler(auditRepo),
		APITokens:       portal.NewAPITokenHandler(apiTokenRepo),
		Invoices:        portal.NewInvoiceHandler(invoiceRepo),
		ClusterMgmt:     clusterMgmtHandler,
		Ceph:            portal.NewCephHandler(cfg.Monitor.CephSSHHost, cfg.Monitor.CephSSHUser, cfg.Monitor.CephSSHKey, cfg.Monitor.SSHKnownHostsFile),
		NodeOps:         portal.NewNodeOpsHandler(cfg.Monitor.CephSSHUser, cfg.Monitor.CephSSHKey, cfg.Monitor.SSHKnownHostsFile),
		NodeCredentials: portal.NewNodeCredentialHandler(nodeCredRepo),
		Quotas:          portal.NewQuotaHandler(quotaRepo, vmRepo),
		Events:          portal.NewEventsHandler(clusterMgr),
		Healing:         portal.NewHealingHandler(healingRepo, clusterMgr),
		OSTemplates:     portal.NewOSTemplateHandler(osTemplateRepo),
		Firewall:        portal.NewFirewallHandler(firewallRepo, service.NewFirewallService(clusterMgr, vmSvc), vmRepo, clusterMgr).WithQuotas(quotaRepo),
		FloatingIPs:     portal.NewFloatingIPHandler(floatingIPRepo, service.NewFloatingIPService(clusterMgr), vmRepo, clusterRepo, clusterMgr),
		Rescue:          portal.NewRescueHandler(vmRepo, service.NewRescueService(vmSvc, clusterMgr), clusterMgr),
		Jobs:            jobsHandlerOrNil(jobsRuntime, jobRepo, vmRepo, aiProvider),
		Auth:            stepUpHandler,
		Shadow:          shadowHandler,
		// PLAN-041 / INFRA-009 监控告警闭环
		NotifyChannels: portal.NewNotifyChannelHandler(notifyChannelRepo, notifyRegistry),
		AlertRules:     portal.NewAlertRuleHandler(alertRuleRepo, alertDeliveryRepo),
		PromExport:     promExportHandler,
		// PLAN-042 / INFRA-010 OpenAPI spec + Swagger UI
		OpenAPI: openapi.NewHandler(),
		// PLAN-053 / INFRA-012 cloud-gateway /v1 适配层
		V1: v1handler.New(v1handler.Deps{
			Users:             userRepo,
			VMs:               vmRepo,
			Products:          productRepo,
			Clusters:          clusterRepo,
			OSTemplates:       osTemplateRepo,
			SSHKeys:           sshKeyRepo,
			Orders:            orderRepo,
			Subscriptions:     subRepo,
			ClustersByName:    clusterRepo,
			OSTemplatesBySlug: osTemplateRepo,
			ProductsBySlug:    productRepo,
			SSHKeysOwner:      sshKeyRepo,
			OrderProvision:    orderHandler,
			VMTrash:           portalVMHandler,
			VMAction:          portalVMHandler,
		}),
		// PLAN-053 Phase E：/v1 POST/DELETE 幂等 middleware 依赖；server.go
		// 在 RequireBearer + RateLimitV1 之后挂 middleware.Idempotency(idempotencyRepo)
		Idempotency: idempotencyRepo,
	})

	runErr := srv.Run()

	// PLAN-051 §2-A / Session-2 F-04：HTTP server stopped accepting new connections
	// 之后再给 in-flight provisioning job 一段 graceful 等待。runtime.Shutdown
	// races wg.Wait() against ctx；超时则放弃，sweeper 会在下次启动时收尾。
	// JOBS_GRACEFUL_TIMEOUT 默认 30s（决策 D-05）。
	if jobsRuntime != nil {
		gracePeriod := jobsGracefulTimeout()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), gracePeriod)
		defer cancel()
		slog.Info("waiting for in-flight provisioning jobs", "timeout", gracePeriod)
		_ = jobsRuntime.Shutdown(shutdownCtx)
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", runErr)
		os.Exit(1)
	}
}

// jobsGracefulTimeout 读 JOBS_GRACEFUL_TIMEOUT 环境变量（time.ParseDuration），
// 默认 30s。超时上限是 image-pull 上限的下界——若设 >30min，sweeper 重启会重复劳动。
func jobsGracefulTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv("JOBS_GRACEFUL_TIMEOUT"))
	if v == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Warn("invalid JOBS_GRACEFUL_TIMEOUT; using 30s", "value", v, "error", err)
		return 30 * time.Second
	}
	return d
}

// requireProductionSecurity 检查 cfg.Server.Env=="production" 下的三项关键安全
// 配置，缺一项即 fail 启动。对 test/dev 全跳过，保持现有 dev 体验。
//   - W1: SSH_KNOWN_HOSTS_FILE 必须配 → MITM 保护
//   - W2: clusters[*].CAFile 必须配 → mTLS root chain 保护（首次 TOFU 不再有 CA 兜底）
//   - W3: PASSWORD_ENCRYPTION_KEY 必须配 → vms.password 落库加密
//
// PLAN-051 §2-A / 决策 D-01：选项 B（先 PR1 配 env，再 PR2 加 fail-fast），生效
// 时机由运维控制；本函数只在 Env=production 时拉闸。
func requireProductionSecurity(cfg *config.Config) error {
	if !strings.EqualFold(cfg.Server.Env, "production") {
		return nil
	}
	var missing []string
	if cfg.Monitor.SSHKnownHostsFile == "" {
		missing = append(missing, "SSH_KNOWN_HOSTS_FILE (mitigates MITM on node SSH; generate via 'ssh-keyscan -H <node-ip> >> /etc/incus-admin/known_hosts')")
	}
	if cfg.Auth.PasswordEncryptionKey == "" {
		missing = append(missing, "PASSWORD_ENCRYPTION_KEY (encrypts vms.password at rest; generate via 'openssl rand -base64 32')")
	}
	for _, cc := range cfg.Clusters {
		if cc.CAFile == "" {
			missing = append(missing, "clusters."+cc.Name+".ca_file (Incus mTLS root; without it first connect TOFU has no CA backstop)")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required production env(s):\n  - %s", strings.Join(missing, "\n  - "))
}

// ensureCustomerProjectAllowsClusterTarget patches every configured Incus
// project to set restricted.cluster.target=allow. Without this Incus 6.x
// rejects "POST /1.0/instances/{name}?target=NODE" with "This project
// doesn't allow cluster member targeting", breaking admin Migrate.
//
// Soft-fail per project: log + continue. Idempotent (PATCH on a project
// that already has the key is a no-op).
func ensureCustomerProjectAllowsClusterTarget(mgr *cluster.Manager) {
	if mgr == nil {
		return
	}
	for _, c := range mgr.List() {
		cc, ok := mgr.ConfigByName(c.Name)
		if !ok {
			continue
		}
		for _, p := range cc.Projects {
			projName := p.Name
			if projName == "" {
				continue
			}
			body := []byte(`{"config":{"restricted.cluster.target":"allow"}}`)
			path := fmt.Sprintf("/1.0/projects/%s", projName)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := c.APIPatch(ctx, path, bytes.NewReader(body))
			cancel()
			if err != nil {
				// Bug #4: incus-admin's mTLS client cert is restricted by design
				// (least-privilege), and Incus 6.x refuses project config edits
				// from restricted certs. We can't fix this from code; surface a
				// clear runbook hint so operators know what to do once and the
				// log noise becomes self-explanatory instead of alarming.
				if strings.Contains(err.Error(), "Certificate is restricted") {
					slog.Warn("project patch restricted.cluster.target=allow blocked by Incus restricted-cert policy — apply once via runbook",
						"cluster", c.Name, "project", projName,
						"runbook", "incus project set "+projName+" restricted.cluster.target=allow",
						"error", err)
				} else {
					slog.Warn("project patch restricted.cluster.target=allow failed",
						"cluster", c.Name, "project", projName, "error", err)
				}
				continue
			}
			slog.Info("project restricted.cluster.target ensured allow",
				"cluster", c.Name, "project", projName)
		}
	}
}

// clusterPinStore 将 repository.ClusterRepo 适配成 cluster.FingerprintStore。
// 仅做方法名转换，保持两边领域职责各自独立。
type clusterPinStore struct {
	repo *repository.ClusterRepo
}

func (s *clusterPinStore) Get(ctx context.Context, clusterName string) (string, error) {
	return s.repo.GetTLSFingerprint(ctx, clusterName)
}

func (s *clusterPinStore) Set(ctx context.Context, clusterName, fingerprint string) error {
	return s.repo.SetTLSFingerprint(ctx, clusterName, fingerprint)
}

// firewallReconcileAdapter glues repo + service into the consumer-side
// worker.FirewallReconciler interface so the worker package stays
// dependency-light.
type firewallReconcileAdapter struct {
	repo *repository.FirewallRepo
	svc  *service.FirewallService
}

func (a *firewallReconcileAdapter) ListGroups(ctx context.Context) ([]model.FirewallGroup, error) {
	return a.repo.ListGroups(ctx)
}

func (a *firewallReconcileAdapter) ListRules(ctx context.Context, groupID int64) ([]model.FirewallRule, error) {
	return a.repo.ListRules(ctx, groupID)
}

func (a *firewallReconcileAdapter) EnsureACL(ctx context.Context, group *model.FirewallGroup, rules []model.FirewallRule) error {
	return a.svc.EnsureACL(ctx, group, rules)
}

// defaultFirewallApplier 给 jobs runtime 注入用户默认 firewall_groups 应用能力（PLAN-036）。
// 实现 jobs.DefaultFirewallApplier 接口；任一方法失败由 jobs 软处理（log + audit）。
type defaultFirewallApplier struct {
	repo *repository.FirewallRepo
	svc  *service.FirewallService
}

func (a defaultFirewallApplier) ListDefaultGroups(ctx context.Context, userID int64) ([]model.FirewallGroup, error) {
	return a.repo.ListDefaultGroupsForUser(ctx, userID)
}

func (a defaultFirewallApplier) Attach(ctx context.Context, clusterName, project, vmName string, group *model.FirewallGroup) error {
	return a.svc.AttachACLToVM(ctx, clusterName, project, vmName, group)
}

func (a defaultFirewallApplier) Bind(ctx context.Context, vmID, groupID int64) error {
	return a.repo.Bind(ctx, vmID, groupID)
}

// vmMigratorAdapter 让 service.VMService 满足 jobs.VMMigrator 接口。
// 字段命名与返回类型由 jobs 决定（避免 jobs → service 反向 import service.MigrateResult）。
type vmMigratorAdapter struct {
	svc *service.VMService
}

func (a vmMigratorAdapter) Migrate(ctx context.Context, clusterName, project, vmName, targetNode, mode string) (*jobs.MigrateOutcome, error) {
	m := service.MigrateMode(mode)
	if m == "" {
		m = service.MigrateAuto
	}
	res, err := a.svc.Migrate(ctx, clusterName, project, vmName, targetNode, m)
	if err != nil {
		return nil, err
	}
	return &jobs.MigrateOutcome{WasRunning: res.WasRunning, Target: res.Target, Mode: string(res.Mode)}, nil
}

// healingTrackerAdapter lets the worker talk to HealingEventRepo without
// importing repository types — keeps the worker's interface consumer-side.
type healingTrackerAdapter struct {
	repo *repository.HealingEventRepo
}

func (a healingTrackerAdapter) FindInProgressByNode(ctx context.Context, clusterID int64, nodeName, trigger string) (int64, error) {
	return a.repo.FindInProgressByNode(ctx, clusterID, nodeName, trigger)
}

func (a healingTrackerAdapter) Create(ctx context.Context, clusterID int64, nodeName, trigger string, actorID *int64) (int64, error) {
	return a.repo.Create(ctx, clusterID, nodeName, trigger, actorID)
}

func (a healingTrackerAdapter) AppendEvacuatedVM(ctx context.Context, eventID int64, vm worker.HealingEvacuatedVM) error {
	return a.repo.AppendEvacuatedVM(ctx, eventID, repository.EvacuatedVM{
		VMID:     vm.VMID,
		Name:     vm.Name,
		FromNode: vm.FromNode,
		ToNode:   vm.ToNode,
	})
}

func (a healingTrackerAdapter) CompleteByNode(ctx context.Context, clusterID int64, nodeName string) (int64, error) {
	return a.repo.CompleteByNode(ctx, clusterID, nodeName)
}

// auditAdapter 把 repository.AuditRepo.Log 转成 jobs.AuditWriter 接口。
// 与 server.AuditWriter 类似，jobs runtime 通过这个 adapter 写
// vm.provisioning.{started,succeeded,failed} 三段 audit。
type auditAdapter struct {
	repo *repository.AuditRepo
}

func (a auditAdapter) Log(ctx context.Context, userID *int64, action, targetType string, targetID int64, details map[string]any, ip string) {
	a.repo.Log(ctx, userID, action, targetType, targetID, details, ip)
}

// jobsHandlerOrNil 在 jobs runtime 缺失时返回 nil（让 server.go 跳过路由注册），
// 避免空 handler 把 /portal/jobs/* 拉成 5xx。
func jobsHandlerOrNil(rt *jobs.Runtime, jobRepo *repository.ProvisioningJobRepo, vmRepo *repository.VMRepo, ai aiassist.Provider) *portal.JobsHandler {
	if rt == nil {
		return nil
	}
	return portal.NewJobsHandler(jobRepo, vmRepo, rt).WithAIProvider(ai)
}

// buildAIProvider 按 AIConfig.Provider 返回 LLM provider；anthropic key 缺失 / 任意
// 配置错误 → 退化 disabled，启动不阻塞。
//
// PLAN-038 / OPS-041。
func buildAIProvider(cfg config.AIConfig) aiassist.Provider {
	switch strings.ToLower(cfg.Provider) {
	case "anthropic":
		p, err := aiassist.NewAnthropicProvider(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.RequestTimeoutSec)
		if err != nil {
			slog.Warn("ai provider anthropic init failed; disabling", "error", err)
			return aiassist.NewDisabledProvider()
		}
		slog.Info("ai provider initialized", "provider", "anthropic", "model", cfg.Model)
		return p
	case "", "disabled":
		return aiassist.NewDisabledProvider()
	default:
		slog.Warn("unknown ai provider; disabling", "provider", cfg.Provider)
		return aiassist.NewDisabledProvider()
	}
}

// PLAN-027 / INFRA-003：DB 行 → config.ClusterConfig 转换。
//   - ip_pools_json 反序列化到 []config.IPPoolConfig
//   - kind 不放进 ClusterConfig（Manager 不区分；UI 通过 DB query 拿 kind 展示）
//   - projects: clusters 表暂时没专门列，按约定从 default + DefaultProject
//     兜底（去重）。env-bootstrap 路径硬编码 [default, customers]；DB-load
//     必须保持一致语义，否则 ListClusterVMs / 跨 project 列表会漏 customers
//     里的 VM（OPS-027 修复）。未来扩 projects_jsonb 列时这块兜底自然让位。
func clusterFromDB(c model.Cluster) (config.ClusterConfig, error) {
	cc := config.ClusterConfig{
		Name:           c.Name,
		DisplayName:    c.DisplayName,
		APIURL:         c.APIURL,
		CertFile:       c.CertFile,
		KeyFile:        c.KeyFile,
		CAFile:         c.CAFile,
		StoragePool:    c.StoragePool,
		Network:        c.Network,
		DefaultProject: c.DefaultProject,
		Projects:       defaultProjectsFor(c.DefaultProject),
	}
	if s := strings.TrimSpace(c.IPPoolsJSON); s != "" {
		if err := json.Unmarshal([]byte(s), &cc.IPPools); err != nil {
			return cc, fmt.Errorf("parse ip_pools_json for %s: %w", c.Name, err)
		}
	}
	return cc, nil
}

// defaultProjectsFor 给 DB-load 的 cluster 兜底 project 列表。always 包含
// "default"；DefaultProject 不为空且 != "default" 时再加它。顺序保证 default
// 在前，与 env-bootstrap 路径 (config.go:168) 完全等价。
func defaultProjectsFor(defaultProject string) []config.ProjectConfig {
	out := []config.ProjectConfig{
		{Name: "default", Access: "internal", Description: "Default project"},
	}
	if defaultProject != "" && defaultProject != "default" {
		out = append(out, config.ProjectConfig{
			Name:        defaultProject,
			Access:      "public",
			Description: defaultProject + " VMs",
		})
	}
	return out
}

// upsertEnvClusters 启动时把 env 配置的 cluster 持久化到 DB（含 cert/key/ca/
// kind=cluster 默认值）。已存在同名 row 时只覆盖 env 提供的字段，admin 后期
// 通过 UI 改的 cert/kind 不会被 env 重启吞掉 —— 但 cert/api_url/etc 仍以 env
// 为准（设计假设：env 改了等价于"想要它生效"）。
func upsertEnvClusters(ctx context.Context, repo *repository.ClusterRepo, envClusters []config.ClusterConfig) {
	for _, cc := range envClusters {
		row := &model.Cluster{
			Name:           cc.Name,
			DisplayName:    cc.DisplayName,
			APIURL:         cc.APIURL,
			Kind:           model.ClusterKindCluster,
			CertFile:       cc.CertFile,
			KeyFile:        cc.KeyFile,
			CAFile:         cc.CAFile,
			DefaultProject: cc.DefaultProject,
			StoragePool:    cc.StoragePool,
			Network:        cc.Network,
		}
		var pools any
		if len(cc.IPPools) > 0 {
			pools = cc.IPPools
		}
		if _, err := repo.CreateFull(ctx, row, pools); err != nil {
			slog.Warn("env cluster upsert to DB failed", "name", cc.Name, "error", err)
		}
	}
}

// loadClustersFromDB 读 DB 全部 clusters 行 → []config.ClusterConfig。
// 返回空切片表示 DB 没有 cluster（首次部署且 env 也无）。
func loadClustersFromDB(ctx context.Context, repo *repository.ClusterRepo) ([]config.ClusterConfig, error) {
	rows, err := repo.ListFull(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]config.ClusterConfig, 0, len(rows))
	for _, r := range rows {
		cc, err := clusterFromDB(r)
		if err != nil {
			slog.Warn("skip invalid cluster row", "name", r.Name, "error", err)
			continue
		}
		out = append(out, cc)
	}
	return out, nil
}

// migrateVMPasswordsToEncrypted 启动时把所有遗留明文 vms.password 行 in-place
// 加密。OPS-022 v1：循环 SELECT 一批 → 用 auth.EncryptPassword 加密 → UPDATE。
// 单条出错 → log warn + 继续；批量完成或 0 行时 return。Idempotent：WHERE 子句
// 排除已 'v1:' 前缀的行，重启反复跑也不会重新加密。
func migrateVMPasswordsToEncrypted(db *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const batch = 200
	migrated := 0
	for {
		rows, err := db.QueryContext(ctx,
			`SELECT id, password FROM vms
			 WHERE password IS NOT NULL AND password <> ''
			   AND password NOT LIKE 'v1:%'
			 ORDER BY id LIMIT $1`, batch)
		if err != nil {
			slog.Error("migrate vm passwords select failed", "error", err)
			return
		}
		type rec struct {
			id    int64
			plain string
		}
		var todo []rec
		for rows.Next() {
			var r rec
			if scanErr := rows.Scan(&r.id, &r.plain); scanErr == nil {
				todo = append(todo, r)
			}
		}
		_ = rows.Close()

		if len(todo) == 0 {
			break
		}
		for _, r := range todo {
			enc, err := authcore.EncryptPassword(r.plain)
			if err != nil {
				slog.Warn("encrypt vm password failed; skipping", "vm_id", r.id, "error", err)
				continue
			}
			if _, err := db.ExecContext(ctx,
				`UPDATE vms SET password = $1, updated_at = NOW() WHERE id = $2 AND password = $3`,
				enc, r.id, r.plain,
			); err != nil {
				slog.Warn("update encrypted vm password failed", "vm_id", r.id, "error", err)
				continue
			}
			migrated++
		}
	}
	if migrated > 0 {
		slog.Info("vms.password migrated to encrypted", "count", migrated)
	}
}

// ============================================================================
// PLAN-041 / INFRA-009 evaluator 适配器
//
// worker 包通过几个小接口（VMDownLister / UsersBalanceLister / ...）拿数据，
// 避免 worker 反向 import repository 包。每个 adapter 仅做方法名 / 类型转换。
// ============================================================================

// vmDownAdapter 把 VMRepo.ListVMRecentlyDown 转成 worker.VMDownLister。
type vmDownAdapter struct{ repo *repository.VMRepo }

func (a vmDownAdapter) ListRecentlyDown(ctx context.Context, since time.Time) ([]worker.VMDownInfo, error) {
	if a.repo == nil {
		return nil, nil
	}
	rows, err := a.repo.ListVMRecentlyDown(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]worker.VMDownInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, worker.VMDownInfo{
			VMID: r.VMID, Name: r.Name, Cluster: r.Cluster, Status: r.Status,
		})
	}
	return out, nil
}

// userBalanceAdapter 把 UserRepo.ListUsersBelowBalance 转成 worker.UsersBalanceLister。
type userBalanceAdapter struct{ repo *repository.UserRepo }

func (a userBalanceAdapter) ListBelowBalance(ctx context.Context, threshold float64) ([]worker.UserBalance, error) {
	if a.repo == nil {
		return nil, nil
	}
	rows, err := a.repo.ListUsersBelowBalance(ctx, threshold)
	if err != nil {
		return nil, err
	}
	out := make([]worker.UserBalance, 0, len(rows))
	for _, r := range rows {
		out = append(out, worker.UserBalance{ID: r.ID, Email: r.Email, Balance: r.Balance})
	}
	return out, nil
}

// jobFailureAdapter 把 ProvisioningJobRepo.CountFailedJobsSince 转成 worker.JobFailureCounter。
type jobFailureAdapter struct {
	repo *repository.ProvisioningJobRepo
}

func (a jobFailureAdapter) CountFailedSince(ctx context.Context, since time.Time) (int, error) {
	if a.repo == nil {
		return 0, nil
	}
	return a.repo.CountFailedJobsSince(ctx, since)
}

// orderFailureAdapter 把 OrderRepo.CountFailedOrdersSince 转成 worker.OrderFailureCounter。
type orderFailureAdapter struct{ repo *repository.OrderRepo }

func (a orderFailureAdapter) CountFailedSince(ctx context.Context, since time.Time) (int, error) {
	if a.repo == nil {
		return 0, nil
	}
	return a.repo.CountFailedOrdersSince(ctx, since)
}

// offlineNodeAdapter 用 scheduler 缓存 + cluster manager 列出每集群 offline node。
type offlineNodeAdapter struct {
	scheduler *cluster.Scheduler
	mgr       *cluster.Manager
}

func (a offlineNodeAdapter) ListOfflineNodes(_ context.Context) (map[string][]string, error) {
	if a.scheduler == nil || a.mgr == nil {
		return nil, nil
	}
	out := make(map[string][]string)
	for _, c := range a.mgr.List() {
		nodes := a.scheduler.GetNodes(c.Name)
		var offline []string
		for _, n := range nodes {
			if n.Status != "Online" {
				offline = append(offline, n.Name)
			}
		}
		out[c.Name] = offline
	}
	return out, nil
}

// ============================================================================
// PLAN-054 / INFRA-013 billing 适配器（main 端 thin shims）
// ============================================================================

// billingVMTrasher 让 billing.Service 不直接 import repository。MarkTrashed
// 在 grace_expire 路径触发 —— 落 trashed_at / trashed_prev_status，由后续
// VMTrashPurger 真正 hard-delete。
type billingVMTrasher struct{ repo *repository.VMRepo }

func (a billingVMTrasher) MarkTrashed(ctx context.Context, vmID int64) (bool, error) {
	if a.repo == nil {
		return false, nil
	}
	return a.repo.MarkTrashed(ctx, vmID)
}

// billingAuditAdapter 让 billing.Service 共用 AuditRepo.Log。与
// auditAdapter (jobs) 同形态。
type billingAuditAdapter struct{ repo *repository.AuditRepo }

func (a billingAuditAdapter) Log(ctx context.Context, userID *int64, action, targetType string, targetID int64, details map[string]any, ip string) {
	if a.repo == nil {
		return
	}
	a.repo.Log(ctx, userID, action, targetType, targetID, details, ip)
}

// billingChargerAdapter 把 billing.Service.ChargeDue 转成 worker.BillingCharger。
// 两边 stats 字段同形态，直接 struct conversion。
type billingChargerAdapter struct{ svc *billing.Service }

func (a billingChargerAdapter) ChargeDue(ctx context.Context) (worker.BillingChargeStats, error) {
	s, err := a.svc.ChargeDue(ctx)
	return worker.BillingChargeStats(s), err
}

// billingGracerAdapter 把 billing.Service.ExpireGrace 转成 worker.BillingGracer。
type billingGracerAdapter struct{ svc *billing.Service }

func (a billingGracerAdapter) ExpireGrace(ctx context.Context) (worker.BillingGraceStats, error) {
	s, err := a.svc.ExpireGrace(ctx)
	return worker.BillingGraceStats(s), err
}

// billingReactivatorAdapter 把 billing.Service.ReactivateOnTopUp 转成
// portal.BillingReactivator（handler 只关心 err，不消费 stats）。
type billingReactivatorAdapter struct{ svc *billing.Service }

func (a billingReactivatorAdapter) ReactivateOnTopUp(ctx context.Context, userID int64) error {
	_, err := a.svc.ReactivateOnTopUp(ctx, userID)
	return err
}

// userHandlerWithBilling 在 billing 未启用时直接返回原 handler，避免给 nil
// adapter 让 handler 误以为已注入。
func userHandlerWithBilling(h *portal.UserHandler, svc *billing.Service) *portal.UserHandler {
	if svc == nil {
		return h
	}
	return h.WithBillingReactivator(billingReactivatorAdapter{svc: svc})
}
