//go:build integration

// Package testhelper 提供集成测试脚手架。只在 -tags=integration 下编译。
package testhelper

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewTestDB 启动一个一次性 Postgres 容器并应用所有迁移，返回连接池。
// 调用方应在有 Docker 的环境里运行；无 Docker 时用 t.Skip 跳过。
// migrationsDir 允许为空，默认使用仓库根下的 db/migrations。
func NewTestDB(t *testing.T, migrationsDir string) *sql.DB {
	t.Helper()
	ctx := context.Background()

	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("incusadmin_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("skipping integration test: cannot start postgres container: %v", err)
		return nil
	}
	t.Cleanup(func() {
		_ = pg.Terminate(context.Background())
	})

	// ConnectionString 可能返回 Docker bridge 上的 hostname（例如 "wg0"），
	// 在部分开发环境无法解析；这里显式用 Host() + MappedPort() 拼 DSN，
	// 确保永远走可达的 host:port。
	host, err := pg.Host(ctx)
	if err != nil {
		t.Fatalf("get host: %v", err)
	}
	port, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("get port: %v", err)
	}
	dsn := fmt.Sprintf("postgres://test:test@%s:%s/incusadmin_test?sslmode=disable", host, port.Port())
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 在嵌套容器等 Docker 网络受限的环境下，mapped port 不一定对调用方可达；
	// 做一次 Ping 做环境探针，不通则 skip 而非 fail，避免误伤 CI 无 Docker 的
	// 构建流程。
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		t.Skipf("skipping integration test: postgres container unreachable (%v)", err)
		return nil
	}

	if migrationsDir == "" {
		migrationsDir = defaultMigrationsDir(t)
	}
	if err := applyMigrations(ctx, db, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

// defaultMigrationsDir 从当前测试文件向上找 db/migrations。
func defaultMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "db", "migrations")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("cannot locate db/migrations from testdir")
	return ""
}

func applyMigrations(ctx context.Context, db *sql.DB, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			return err
		}
	}
	return nil
}
