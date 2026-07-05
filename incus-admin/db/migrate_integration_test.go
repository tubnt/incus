//go:build integration

// OPS-052 / WP-I2：goose 迁移体系集成测试。用 testcontainers 起一次性
// Postgres，验证：
//   - 从空库全量 apply 成功（goose_db_version 建立，版本推进到 030）
//   - 重复 apply 幂等，不报错
//   - status 能正常枚举（不 panic）
//   - down 一步 + 再 up 的回滚/前滚 roundtrip 成功（覆盖 030 的 Down/Up）
//
// 无 Docker 环境自动 skip（与 internal/testhelper 一致），不误伤无 Docker 的 CI。
package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	migrationsdb "github.com/incuscloud/incus-admin/db"
)

// wantVersion 是最后一个迁移文件的编号（db/migrations/030_*.sql）。新增迁移
// 时同步 +1，让本测试成为「忘补 goose 注解 / 漏 apply」的兜底告警。
const wantVersion int64 = 30

func newGooseTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("incusadmin_goose_test"),
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
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	host, err := pg.Host(ctx)
	if err != nil {
		t.Fatalf("get host: %v", err)
	}
	port, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("get port: %v", err)
	}
	dsn := fmt.Sprintf("postgres://test:test@%s:%s/incusadmin_goose_test?sslmode=disable", host, port.Port())
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		t.Skipf("skipping integration test: postgres container unreachable (%v)", err)
		return nil
	}
	return db
}

func TestGooseMigrationsApplyIdempotent(t *testing.T) {
	db := newGooseTestDB(t)
	if db == nil {
		return
	}
	ctx := context.Background()

	goose.SetBaseFS(migrationsdb.MigrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}

	// 1) 空库全量 apply
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		t.Fatalf("first up: %v", err)
	}
	v, err := goose.GetDBVersion(db)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if v != wantVersion {
		t.Fatalf("version after up = %d, want %d", v, wantVersion)
	}

	// goose_db_version 版本表应已建立
	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'goose_db_version')`,
	).Scan(&exists); err != nil {
		t.Fatalf("check version table: %v", err)
	}
	if !exists {
		t.Fatal("goose_db_version table not created")
	}

	// 2) status 不报错
	if err := goose.StatusContext(ctx, db, "migrations"); err != nil {
		t.Fatalf("status: %v", err)
	}

	// 3) 重复 apply 幂等
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		t.Fatalf("second up (idempotent): %v", err)
	}
	if v2, err := goose.GetDBVersion(db); err != nil || v2 != wantVersion {
		t.Fatalf("version after re-up = %d (err=%v), want %d", v2, err, wantVersion)
	}

	// 4) down 一步 + 再 up：覆盖最后一个迁移的 Down/Up roundtrip
	if err := goose.DownContext(ctx, db, "migrations"); err != nil {
		t.Fatalf("down: %v", err)
	}
	if v3, err := goose.GetDBVersion(db); err != nil || v3 != wantVersion-1 {
		t.Fatalf("version after down = %d (err=%v), want %d", v3, err, wantVersion-1)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if v4, err := goose.GetDBVersion(db); err != nil || v4 != wantVersion {
		t.Fatalf("version after up-after-down = %d (err=%v), want %d", v4, err, wantVersion)
	}
}
