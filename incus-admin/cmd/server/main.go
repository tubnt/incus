package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/config"
	"github.com/incuscloud/incus-admin/internal/handler/portal"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/server"
	"github.com/incuscloud/incus-admin/internal/service"
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

	var clusterMgr *cluster.Manager
	var scheduler *cluster.Scheduler
	var vmSvc *service.VMService

	if len(cfg.Clusters) > 0 {
		clusterMgr, err = cluster.NewManager(cfg.Clusters)
		if err != nil {
			slog.Warn("cluster manager init failed, running without clusters", "error", err)
		} else {
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
	portal.SetAuditRepo(auditRepo)
	portal.SetIPAddrRepo(ipAddrRepo)
	middleware.SetEmergencySecret(cfg.Auth.EmergencyToken)

	middleware.SetTokenValidator(func(ctx context.Context, token string) (int64, error) {
		t, err := apiTokenRepo.ValidateToken(ctx, token)
		if err != nil || t == nil {
			return 0, fmt.Errorf("invalid token")
		}
		return t.UserID, nil
	})

	srv := server.New(cfg, userLookup, roleLookup, balanceLookup, server.Handlers{
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
		Ceph:        portal.NewCephHandler(cfg.Monitor.CephSSHHost, cfg.Monitor.CephSSHUser, cfg.Monitor.CephSSHKey),
		NodeOps:     portal.NewNodeOpsHandler(cfg.Monitor.CephSSHUser, cfg.Monitor.CephSSHKey),
		Quotas:      portal.NewQuotaHandler(quotaRepo, vmRepo),
	})

	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
