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

	vmRepo := repository.NewVMRepo(db)

	adminHandler := portal.NewAdminVMHandler(vmSvc, clusterMgr, scheduler)
	portalHandler := portal.NewVMHandler(vmSvc, vmRepo, clusterMgr)
	userHandler := portal.NewUserHandler(userRepo)
	ipPoolHandler := portal.NewIPPoolHandler(clusterMgr)
	consoleHandler := portal.NewConsoleHandler(clusterMgr)
	snapshotHandler := portal.NewSnapshotHandler(clusterMgr)
	metricsHandler := portal.NewMetricsHandler(clusterMgr)

	srv := server.New(cfg, userLookup, adminHandler, portalHandler, userHandler, ipPoolHandler, consoleHandler, snapshotHandler, metricsHandler)

	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
