package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	authcore "github.com/incuscloud/incus-admin/internal/auth"
	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/config"
	adminhandler "github.com/incuscloud/incus-admin/internal/handler/admin"
	authhandler "github.com/incuscloud/incus-admin/internal/handler/auth"
	"github.com/incuscloud/incus-admin/internal/handler/portal"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/server"
	"github.com/incuscloud/incus-admin/internal/service"
	"github.com/incuscloud/incus-admin/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

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

	if len(cfg.Clusters) > 0 {
		// SPKI pin store backed by clusters.tls_fingerprint (migration 006).
		// TOFU on first connect, refuses peers that diverge from the learned pin.
		pinStore := &clusterPinStore{repo: clusterRepo}
		clusterMgr, err = cluster.NewManager(cfg.Clusters, pinStore)
		if err != nil {
			slog.Warn("cluster manager init failed, running without clusters", "error", err)
		} else {
			// Seed DB clusters table from config and populate ID↔Name maps.
			for _, cc := range cfg.Clusters {
				id, upErr := clusterRepo.Upsert(context.Background(), cc.Name, cc.DisplayName, cc.APIURL)
				if upErr != nil {
					slog.Error("cluster upsert failed", "name", cc.Name, "error", upErr)
					continue
				}
				clusterMgr.SetID(cc.Name, id)
			}
			scheduler = cluster.NewScheduler(clusterMgr)
			vmSvc = service.NewVMService(clusterMgr)
			slog.Info("cluster manager ready", "clusters", len(clusterMgr.List()))
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
	auditRepo := repository.NewAuditRepo(db)
	apiTokenRepo := repository.NewAPITokenRepo(db)
	invoiceRepo := repository.NewInvoiceRepo(db)
	quotaRepo := repository.NewQuotaRepo(db)

	ipAddrRepo := repository.NewIPAddrRepo(db)
	healingRepo := repository.NewHealingEventRepo(db)
	portal.SetAuditRepo(auditRepo)
	portal.SetIPAddrRepo(ipAddrRepo)
	portal.SetUserRepo(userRepo)
	portal.SetHealingRepo(healingRepo)
	portal.SetAppEnv(cfg.Server.Env)
	middleware.SetEmergencySecret(cfg.Auth.EmergencyToken)

	middleware.SetTokenValidator(func(ctx context.Context, token string) (int64, error) {
		t, err := apiTokenRepo.ValidateToken(ctx, token)
		if err != nil || t == nil {
			return 0, fmt.Errorf("invalid token")
		}
		return t.UserID, nil
	})

	// Step-up OIDC is optional; absence just disables sensitive-route protection.
	// Init is outside the clusterMgr block because step-up has no cluster dependency.
	var stepUpHandler server.StepUpHandler
	if cfg.Auth.OIDCIssuer != "" && cfg.Auth.OIDCClientID != "" && cfg.Auth.OIDCClientSecret != "" && cfg.Auth.StepUpCallbackURL != "" {
		oidcClient, oidcErr := authcore.NewOIDCClient(context.Background(),
			cfg.Auth.OIDCIssuer, cfg.Auth.OIDCClientID, cfg.Auth.OIDCClientSecret, cfg.Auth.StepUpCallbackURL)
		if oidcErr != nil {
			slog.Warn("step-up OIDC discovery failed, disabled", "issuer", cfg.Auth.OIDCIssuer, "error", oidcErr)
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

	// Retention worker: deletes audit_logs rows older than
	// AUDIT_RETENTION_DAYS (default 365). Runs until server shutdown.
	go worker.RunAuditCleanup(workerCtx, auditRepo, cfg.Auth.AuditRetentionDays)

	// API token cleanup: removes expired rows after a 30d grace period so
	// audit cross-references survive short investigations.
	go worker.RunAPITokenCleanup(workerCtx, apiTokenRepo, 30*24*time.Hour)

	// PLAN-020 Phase A: VM state reverse-sync worker. Polls each Incus
	// cluster every 60s, diffs against active `vms` rows, and flips rows
	// that have vanished from Incus to status='gone' while releasing their
	// IPs. Only starts when a cluster manager exists (skipped in DB-only
	// test/dev environments).
	if clusterMgr != nil {
		snapshotFn := worker.ClusterSnapshotFromManager(clusterMgr, "customers")
		reconcileCfg := worker.VMReconcilerConfig{Interval: 60 * time.Second}
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
	}

	srv := server.New(cfg, userLookup, roleLookup, balanceLookup, stepUpLookup, auditWriter, server.Handlers{
		Admin:     portal.NewAdminVMHandler(vmSvc, vmRepo, sshKeyRepo, clusterMgr, scheduler),
		Portal:    portal.NewVMHandler(vmSvc, vmRepo, sshKeyRepo, clusterMgr),
		Users:     portal.NewUserHandler(userRepo),
		IPPools:   portal.NewIPPoolHandler(clusterMgr),
		Console:   portal.NewConsoleHandler(clusterMgr, vmRepo),
		Snaps:     portal.NewSnapshotHandler(clusterMgr, vmRepo),
		Metrics:   portal.NewMetricsHandler(clusterMgr, vmRepo),
		SSHKeys:   portal.NewSSHKeyHandler(sshKeyRepo),
		Tickets:   portal.NewTicketHandler(ticketRepo),
		Products:  portal.NewProductHandler(productRepo),
		Orders:    portal.NewOrderHandler(orderRepo, productRepo, vmSvc, vmRepo, sshKeyRepo, clusterMgr),
		Audit:     portal.NewAuditHandler(auditRepo),
		APITokens: portal.NewAPITokenHandler(apiTokenRepo),
		Invoices:    portal.NewInvoiceHandler(invoiceRepo),
		ClusterMgmt: portal.NewClusterMgmtHandler(clusterMgr),
		Ceph:        portal.NewCephHandler(cfg.Monitor.CephSSHHost, cfg.Monitor.CephSSHUser, cfg.Monitor.CephSSHKey, cfg.Monitor.SSHKnownHostsFile),
		NodeOps:     portal.NewNodeOpsHandler(cfg.Monitor.CephSSHUser, cfg.Monitor.CephSSHKey, cfg.Monitor.SSHKnownHostsFile),
		Quotas:      portal.NewQuotaHandler(quotaRepo, vmRepo),
		Events:      portal.NewEventsHandler(clusterMgr),
		Healing:     portal.NewHealingHandler(healingRepo, clusterMgr),
		Auth:        stepUpHandler,
		Shadow:      shadowHandler,
	})

	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
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
