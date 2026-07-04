//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/testhelper"
)

// WP-E：GetByUserID 缺行契约。无 quota 行 = 用户未设配额 = 零记录，必须返回
// (nil, nil) 而非把 sql.ErrNoRows 当错误上抛——否则调用方 fail-closed 分支会把
// "正常缺行"误判为 DB 故障回 503（order.Pay / firewall CreateGroup 等）。
func TestQuotaGetByUserID_MissingRowReturnsNilNil(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewQuotaRepo(db)
	userID := seedUser(t, db) // 未插入 quotas 行

	q, err := repo.GetByUserID(context.Background(), userID)
	if err != nil {
		t.Fatalf("缺行不应返回错误，got err=%v", err)
	}
	if q != nil {
		t.Fatalf("缺行应返回 nil quota，got %+v", q)
	}
}

// 有行时正常返回配额记录。
func TestQuotaGetByUserID_ExistingRow(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewQuotaRepo(db)
	userID := seedUser(t, db)

	if _, err := db.Exec(
		`INSERT INTO quotas (user_id, max_vms, max_vcpus, max_ram_mb, max_disk_gb)
		 VALUES ($1, 5, 10, 8192, 100)`, userID); err != nil {
		t.Fatalf("seed quota: %v", err)
	}

	q, err := repo.GetByUserID(context.Background(), userID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if q == nil {
		t.Fatal("有行应返回非 nil quota")
	}
	want := model.Quota{UserID: userID, MaxVMs: 5, MaxVCPUs: 10, MaxRAMMB: 8192, MaxDiskGB: 100}
	if q.UserID != want.UserID || q.MaxVMs != want.MaxVMs || q.MaxVCPUs != want.MaxVCPUs ||
		q.MaxRAMMB != want.MaxRAMMB || q.MaxDiskGB != want.MaxDiskGB {
		t.Fatalf("quota mismatch: got %+v, want %+v", *q, want)
	}
}
