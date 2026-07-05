// OPS-052 / WP-I2：goose 迁移子命令。
//
// 用 goose runner + goose_db_version 版本表替代原先手工 psql 逐个执行
// db/migrations/*.sql 的做法。迁移文件通过 db 包的 go:embed 打进二进制，
// 部署产物自包含，无需在目标机额外拷贝 SQL 目录（符合「编译产物部署、
// 不在远端编译」的约束）。
//
// 子命令：
//
//	incus-admin migrate up       - 应用所有未执行的迁移到最新
//	incus-admin migrate down     - 回滚最近一次迁移（一步）
//	incus-admin migrate status   - 列出各迁移的应用状态（001..NNN）
//	incus-admin migrate version  - 打印当前 DB 版本号
//
// DSN 复用 server 的 DATABASE_URL，避免 migrate 还要求 SESSION_SECRET 等
// server-only env。
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"

	migrationsfs "github.com/incuscloud/incus-admin/db"
)

// migrationsDir 是 embed FS 内的迁移目录名（db/embed.go 的 //go:embed
// migrations/*.sql）。
const migrationsDir = "migrations"

// setupGoose 配置 goose 使用内嵌迁移 + postgres dialect。全局设置，幂等，
// startup 自动迁移与 migrate 子命令共用。
func setupGoose() error {
	goose.SetBaseFS(migrationsfs.MigrationsFS)
	return goose.SetDialect("postgres")
}

// openMigrateDB 打开 migrate 用的 DB 连接并 Ping 探活。
func openMigrateDB() (*sql.DB, error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL 未设置")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	return db, nil
}

// withGooseDB 组合 setupGoose + openMigrateDB + defer Close，供各子命令复用。
func withGooseDB(fn func(*sql.DB) error) error {
	if err := setupGoose(); err != nil {
		return err
	}
	db, err := openMigrateDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return fn(db)
}

func runMigrate(args []string) {
	root := &cobra.Command{
		Use:   "migrate",
		Short: "数据库迁移（goose runner + goose_db_version 版本表）",
		Long:  "用 goose 管理 db/migrations 下的 SQL 迁移，替代手工 psql。DSN 取自 DATABASE_URL。",
	}
	root.AddCommand(migrateUpCmd(), migrateDownCmd(), migrateStatusCmd(), migrateVersionCmd())
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func migrateUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "应用所有未执行的迁移到最新",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withGooseDB(func(db *sql.DB) error {
				return goose.UpContext(cmd.Context(), db, migrationsDir)
			})
		},
	}
}

func migrateDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "回滚最近一次迁移（一步）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withGooseDB(func(db *sql.DB) error {
				return goose.DownContext(cmd.Context(), db, migrationsDir)
			})
		},
	}
}

func migrateStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "列出各迁移的应用状态",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withGooseDB(func(db *sql.DB) error {
				return goose.StatusContext(cmd.Context(), db, migrationsDir)
			})
		},
	}
}

func migrateVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印当前 DB 版本号",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withGooseDB(func(db *sql.DB) error {
				return goose.VersionContext(cmd.Context(), db, migrationsDir)
			})
		},
	}
}

// runStartupMigrate 供 runServer 在 INCUS_ADMIN_AUTO_MIGRATE 开启时调用。
// 复用 server 已打开的 *sql.DB 连接池，把 DB 迁移到最新。
func runStartupMigrate(db *sql.DB) error {
	if err := setupGoose(); err != nil {
		return err
	}
	return goose.Up(db, migrationsDir)
}

// autoMigrateEnabled 读 INCUS_ADMIN_AUTO_MIGRATE。默认关闭，保持历史「启动
// 不自动迁移」行为不变；单机 docker 部署可显式打开做一键起库。
func autoMigrateEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("INCUS_ADMIN_AUTO_MIGRATE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
