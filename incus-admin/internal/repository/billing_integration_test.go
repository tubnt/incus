//go:build integration

package repository_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/testhelper"
)

// PLAN-054 / INFRA-013 + PLAN-053 / INFRA-012 skeleton 阶段 schema 验证测试：
//
// 业务流（订单 hook / billing worker / idempotency middleware）由 L3-E / L3-F /
// L3-H 接入；本组测试只验证 028 / 029 migration 真的落了表 + 约束 + 索引，
// 与 repo skeleton 的接口签名能跑通。

// seedSubFixtures 准备一组 user / cluster / product / vm，返回 ID 让订阅测试用。
func seedSubFixtures(t *testing.T, db *sql.DB) (userID, productID, clusterID, vmID int64) {
	t.Helper()
	ctx := context.Background()
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, role, balance) VALUES ($1,$2,'customer',100) RETURNING id`,
		"sub@test", "sub").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO clusters (name, api_url) VALUES ('c-sub','https://x') RETURNING id`).Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO products (name, price_monthly, cpu, memory_mb, disk_gb) VALUES ('p-sub',10,1,1024,10) RETURNING id`).Scan(&productID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO vms (name, cluster_id, user_id, status, cpu, memory_mb, disk_gb, os_image, node)
		 VALUES ('vm-sub', $1, $2, 'running', 1, 1024, 10, 'noop', 'noop') RETURNING id`,
		clusterID, userID).Scan(&vmID); err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	return
}

// TestSubscriptionRepo_InsertGet 验证 vm_subscriptions 表能写入 + 查询，
// 且 period 字段 + status 默认值都按 CHECK 落到 'monthly' / 'active'。
func TestSubscriptionRepo_InsertGet(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewSubscriptionRepo(db)
	userID, productID, _, vmID := seedSubFixtures(t, db)

	monthly := 10.0
	in := &model.VMSubscription{
		VMID:        vmID,
		ProductID:   productID,
		UserID:      userID,
		Period:      model.BillingPeriodMonthly,
		MonthlyRate: &monthly,
		PaidUntil:   time.Now().Add(30 * 24 * time.Hour),
		Status:      model.SubscriptionStatusActive,
	}
	out, err := repo.Insert(context.Background(), in)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if out.ID == 0 || out.Status != model.SubscriptionStatusActive || out.Period != model.BillingPeriodMonthly {
		t.Fatalf("returned row not normalized: %+v", out)
	}

	got, err := repo.GetByVM(context.Background(), vmID)
	if err != nil {
		t.Fatalf("GetByVM: %v", err)
	}
	if got == nil || got.ID != out.ID {
		t.Fatalf("GetByVM mismatch: %+v", got)
	}
}

// TestChargeRepo_UniqueGuard 验证 billing_charges UNIQUE(subscription_id, charge_date)
// 真的把 worker 重扣防御落到 DB 层；第二次 INSERT 同 (sub_id, date) 必须返
// ErrChargeDuplicate（worker 据此 idempotent 跳过）。
func TestChargeRepo_UniqueGuard(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	subRepo := repository.NewSubscriptionRepo(db)
	chargeRepo := repository.NewChargeRepo(db)
	userID, productID, _, vmID := seedSubFixtures(t, db)

	monthly := 10.0
	sub, err := subRepo.Insert(context.Background(), &model.VMSubscription{
		VMID:        vmID,
		ProductID:   productID,
		UserID:      userID,
		Period:      model.BillingPeriodMonthly,
		MonthlyRate: &monthly,
		PaidUntil:   time.Now().Add(30 * 24 * time.Hour),
		Status:      model.SubscriptionStatusActive,
	})
	if err != nil {
		t.Fatalf("Insert sub: %v", err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	first, err := chargeRepo.Insert(context.Background(), &model.BillingCharge{
		SubscriptionID: sub.ID,
		ChargeDate:     today,
		Amount:         10,
		Status:         model.BillingChargePaid,
	})
	if err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	if first.ID == 0 {
		t.Fatalf("expected returned id")
	}

	_, err = chargeRepo.Insert(context.Background(), &model.BillingCharge{
		SubscriptionID: sub.ID,
		ChargeDate:     today,
		Amount:         10,
		Status:         model.BillingChargePaid,
	})
	if !errors.Is(err, repository.ErrChargeDuplicate) {
		t.Fatalf("expected ErrChargeDuplicate on duplicate (sub, date), got %v", err)
	}
}

// TestIdempotencyRepo_RoundTrip 验证 idempotency_keys 表能写入 / 读回，且
// BYTEA 字段（response_body）能 byte-for-byte 还原；middleware 重放靠这一点。
func TestIdempotencyRepo_RoundTrip(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewIdempotencyRepo(db)
	userID, _, _, _ := seedSubFixtures(t, db)

	body := []byte(`{"id":1,"status":"pending"}`)
	in := &model.IdempotencyKey{
		Key:          "test-key-001",
		UserID:       userID,
		Method:       "POST",
		Path:         "/v1/instances",
		StatusCode:   201,
		ResponseBody: body,
		RequestHash:  "deadbeef",
	}
	if err := repo.Put(context.Background(), *in); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := repo.Get(context.Background(), "test-key-001", userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatalf("Get returned nil")
	}
	if !bytes.Equal(got.ResponseBody, body) {
		t.Fatalf("response_body byte mismatch: got %q want %q", got.ResponseBody, body)
	}
	if got.StatusCode != 201 || got.Method != "POST" {
		t.Fatalf("status/method mismatch: %+v", got)
	}

	// 跨用户读必须 miss——Get scoped by user_id 防 cross-user replay。
	other, err := repo.Get(context.Background(), "test-key-001", userID+9999)
	if err != nil {
		t.Fatalf("cross-user Get: %v", err)
	}
	if other != nil {
		t.Fatalf("cross-user Get should miss, got %+v", other)
	}

	// 再 Put 一次相同 key：ON CONFLICT DO NOTHING 不抛错。
	if err := repo.Put(context.Background(), *in); err != nil {
		t.Fatalf("Put on conflict (DO NOTHING) should not error: %v", err)
	}

	// cleanup 也跑一遍，cutoff 是未来时间所以应该删掉这一行
	n, err := repo.DeleteOlderThan(context.Background(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row deleted, got %d", n)
	}
}

// TestIdempotencyRepo_CompositeKeyCrossUser 验证迁移 030 复合主键 (key, user_id)：
// 两个不同用户使用**相同** Idempotency-Key 时互不碰撞，各自都能写入 + 读回
// 自己的缓存行；同一用户 + 同 key 二次 Put 仍走 ON CONFLICT DO NOTHING 不覆盖。
func TestIdempotencyRepo_CompositeKeyCrossUser(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewIdempotencyRepo(db)
	ctx := context.Background()

	// 两个独立用户
	var userA, userB int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, role, balance) VALUES ('a@test','a','customer',0) RETURNING id`).Scan(&userA); err != nil {
		t.Fatalf("seed userA: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, role, balance) VALUES ('b@test','b','customer',0) RETURNING id`).Scan(&userB); err != nil {
		t.Fatalf("seed userB: %v", err)
	}

	const sameKey = "shared-key-across-users-1"
	mk := func(uid int64, body string) model.IdempotencyKey {
		return model.IdempotencyKey{
			Key: sameKey, UserID: uid, Method: "POST", Path: "/v1/instances",
			StatusCode: 201, ResponseBody: []byte(body), RequestHash: "h",
		}
	}

	// A、B 用相同 key 各写一条——旧 (key) 单主键下第二条会被 ON CONFLICT 吞掉，
	// 复合主键下两条都应落库。
	if err := repo.Put(ctx, mk(userA, `{"u":"a"}`)); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if err := repo.Put(ctx, mk(userB, `{"u":"b"}`)); err != nil {
		t.Fatalf("Put B (same key, different user) must not collide: %v", err)
	}

	gotA, err := repo.Get(ctx, sameKey, userA)
	if err != nil || gotA == nil {
		t.Fatalf("Get A: %v (nil=%v)", err, gotA == nil)
	}
	gotB, err := repo.Get(ctx, sameKey, userB)
	if err != nil || gotB == nil {
		t.Fatalf("Get B: %v (nil=%v)", err, gotB == nil)
	}
	if !bytes.Equal(gotA.ResponseBody, []byte(`{"u":"a"}`)) {
		t.Fatalf("user A read wrong row: %q", gotA.ResponseBody)
	}
	if !bytes.Equal(gotB.ResponseBody, []byte(`{"u":"b"}`)) {
		t.Fatalf("user B read wrong row: %q", gotB.ResponseBody)
	}

	// 同一用户 + 同 key 二次 Put：ON CONFLICT (key, user_id) DO NOTHING，不覆盖。
	if err := repo.Put(ctx, mk(userA, `{"u":"a-overwritten"}`)); err != nil {
		t.Fatalf("re-Put A: %v", err)
	}
	reA, err := repo.Get(ctx, sameKey, userA)
	if err != nil || reA == nil {
		t.Fatalf("re-Get A: %v", err)
	}
	if !bytes.Equal(reA.ResponseBody, []byte(`{"u":"a"}`)) {
		t.Fatalf("second Put must not overwrite; got %q", reA.ResponseBody)
	}
}

// TestProductRepo_NewColumnsScan 回归测试：products 加 price_daily +
// period_supported 后 ListActive / GetByID 必须能完整扫描，扫描偏移错位的话
// PriceMonthly 会读到 price_daily 的 NULL，整个 Scan 会失败。
func TestProductRepo_NewColumnsScan(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewProductRepo(db)

	ctx := context.Background()
	// 直接 SQL 插入：测试 ALTER 加的字段 + DEFAULT 都正确生效
	var id int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO products (name, price_monthly, cpu, memory_mb, disk_gb, active)
		 VALUES ('p-default', 10, 1, 1024, 10, true) RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("seed product: %v", err)
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("GetByID: %v / %+v", err, got)
	}
	if got.PriceMonthly != 10 {
		t.Fatalf("scan offset issue: PriceMonthly=%v want 10", got.PriceMonthly)
	}
	if got.PriceDaily != nil {
		t.Fatalf("PriceDaily should default to NULL, got %v", *got.PriceDaily)
	}
	if len(got.PeriodSupported) != 1 || got.PeriodSupported[0] != model.BillingPeriodMonthly {
		t.Fatalf("period_supported default broken: %v", got.PeriodSupported)
	}

	// 走 Update 路径写 daily 套餐
	daily := 0.5
	got.PriceDaily = &daily
	got.PeriodSupported = []string{model.BillingPeriodMonthly, model.BillingPeriodDaily}
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reread, _ := repo.GetByID(ctx, id)
	if reread.PriceDaily == nil || *reread.PriceDaily != 0.5 {
		t.Fatalf("PriceDaily round-trip broken: %+v", reread.PriceDaily)
	}
	if len(reread.PeriodSupported) != 2 {
		t.Fatalf("PeriodSupported round-trip lost values: %v", reread.PeriodSupported)
	}
}

// TestSubscriptionRepo_ListSuspendedExpired_ListSuspendedByUser 验证 PLAN-054
// Phase H worker 用的两个新查询：
//   - ListSuspendedExpired 只返 status='suspended' AND grace_until<asOf
//   - ListSuspendedByUser 只返该用户的 suspended 行，按 grace_until ASC NULLS LAST
func TestSubscriptionRepo_ListSuspendedExpired_ListSuspendedByUser(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewSubscriptionRepo(db)
	userID, productID, _, _ := seedSubFixtures(t, db)

	// 多造几条 VM + sub，覆盖三个分支
	mkVM := func(name string) int64 {
		var id int64
		if err := db.QueryRow(`INSERT INTO vms (name, cluster_id, user_id, status, cpu, memory_mb, disk_gb, os_image, node)
			VALUES ($1, 1, $2, 'running', 1, 1024, 10, 'noop', 'noop') RETURNING id`, name, userID).Scan(&id); err != nil {
			t.Fatalf("seed vm %s: %v", name, err)
		}
		return id
	}
	vmA, vmB, vmC := mkVM("vm-a"), mkVM("vm-b"), mkVM("vm-c")

	ctx := context.Background()
	now := time.Now().UTC()
	monthly := 10.0
	subActive, _ := repo.Insert(ctx, &model.VMSubscription{VMID: vmA, ProductID: productID, UserID: userID,
		Period: model.BillingPeriodMonthly, MonthlyRate: &monthly,
		PaidUntil: now.Add(30 * 24 * time.Hour), Status: model.SubscriptionStatusActive})
	subSuspendedExpired, _ := repo.Insert(ctx, &model.VMSubscription{VMID: vmB, ProductID: productID, UserID: userID,
		Period: model.BillingPeriodMonthly, MonthlyRate: &monthly,
		PaidUntil: now.Add(-72 * time.Hour), Status: model.SubscriptionStatusSuspended})
	subSuspendedLive, _ := repo.Insert(ctx, &model.VMSubscription{VMID: vmC, ProductID: productID, UserID: userID,
		Period: model.BillingPeriodMonthly, MonthlyRate: &monthly,
		PaidUntil: now.Add(-1 * time.Hour), Status: model.SubscriptionStatusSuspended})
	// 把 suspended 行的 grace_until 分别置为已过 / 未过
	_ = repo.UpdateSuspension(ctx, subSuspendedExpired.ID, now.Add(-73*time.Hour), now.Add(-1*time.Hour))
	_ = repo.UpdateSuspension(ctx, subSuspendedLive.ID, now.Add(-2*time.Hour), now.Add(70*time.Hour))

	// ListSuspendedExpired 只挑 grace_until<now 的
	expired, err := repo.ListSuspendedExpired(ctx, now, 0)
	if err != nil {
		t.Fatalf("ListSuspendedExpired: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != subSuspendedExpired.ID {
		t.Fatalf("expected only subSuspendedExpired, got %+v", expired)
	}

	// ListSuspendedByUser 返该用户全部 suspended（2 条）；active 不在其中
	suspended, err := repo.ListSuspendedByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListSuspendedByUser: %v", err)
	}
	if len(suspended) != 2 {
		t.Fatalf("expected 2 suspended for user, got %d", len(suspended))
	}
	// 顺序：grace_until ASC → expired 在前
	if suspended[0].ID != subSuspendedExpired.ID || suspended[1].ID != subSuspendedLive.ID {
		t.Fatalf("order wrong: %+v / %+v", suspended[0].ID, suspended[1].ID)
	}
	_ = subActive
}

// TestChargeRepo_UpdatePaid 验证翻 status + 写 transaction_id。
func TestChargeRepo_UpdatePaid(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	subRepo := repository.NewSubscriptionRepo(db)
	chargeRepo := repository.NewChargeRepo(db)
	userID, productID, _, vmID := seedSubFixtures(t, db)

	ctx := context.Background()
	rate := 1.0
	sub, _ := subRepo.Insert(ctx, &model.VMSubscription{
		VMID: vmID, ProductID: productID, UserID: userID,
		Period: model.BillingPeriodDaily, DailyRate: &rate,
		PaidUntil: time.Now(), Status: model.SubscriptionStatusActive,
	})
	c, err := chargeRepo.Insert(ctx, &model.BillingCharge{
		SubscriptionID: sub.ID, ChargeDate: time.Now().UTC().Truncate(24 * time.Hour),
		Amount: 1.0, Status: model.BillingChargeInsufficient,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// 模拟 transactions 行（FK 兜底）
	var txID int64
	if err := db.QueryRow(`INSERT INTO transactions (user_id, amount, type, description)
		VALUES ($1, $2, 'charge', 'test') RETURNING id`, userID, -1.0).Scan(&txID); err != nil {
		t.Fatalf("insert tx: %v", err)
	}

	if err := chargeRepo.UpdatePaid(ctx, c.ID, txID); err != nil {
		t.Fatalf("UpdatePaid: %v", err)
	}
	got, _ := chargeRepo.GetByDate(ctx, sub.ID, c.ChargeDate)
	if got.Status != model.BillingChargePaid || got.TransactionID == nil || *got.TransactionID != txID {
		t.Fatalf("UpdatePaid round-trip: %+v", got)
	}
}

// TestSubscriptionRepo_CancelAndReactivateByVM 验证 PLAN-054 Phase G 的 trash/
// restore 联动：CancelByVM 只动 active 行，且 ReactivateByVM 把最新 cancelled
// 行拉回 active 并写新 paid_until。
func TestSubscriptionRepo_CancelAndReactivateByVM(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewSubscriptionRepo(db)
	userID, productID, _, vmID := seedSubFixtures(t, db)

	ctx := context.Background()
	monthly := 10.0
	now := time.Now()
	sub, err := repo.Insert(ctx, &model.VMSubscription{
		VMID: vmID, ProductID: productID, UserID: userID,
		Period: model.BillingPeriodMonthly, MonthlyRate: &monthly,
		PaidUntil: now.Add(30 * 24 * time.Hour),
		Status:    model.SubscriptionStatusActive,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// trash 联动：CancelByVM 把 active → cancelled
	n, err := repo.CancelByVM(ctx, vmID)
	if err != nil || n != 1 {
		t.Fatalf("CancelByVM rows=%d err=%v", n, err)
	}
	got, err := repo.GetLatestByVM(ctx, vmID)
	if err != nil || got == nil || got.Status != model.SubscriptionStatusCancelled {
		t.Fatalf("after cancel: %+v err=%v", got, err)
	}
	// GetByVM (active only) 应返 nil
	active, _ := repo.GetByVM(ctx, vmID)
	if active != nil {
		t.Fatalf("GetByVM after cancel should be nil, got %+v", active)
	}

	// 二次 cancel 应是 no-op（0 rows）
	n2, err := repo.CancelByVM(ctx, vmID)
	if err != nil || n2 != 0 {
		t.Fatalf("idempotent CancelByVM rows=%d err=%v", n2, err)
	}

	// restore 联动：ReactivateByVM 拉回 active + 写新 paid_until
	newPaidUntil := now.Add(48 * time.Hour)
	n3, err := repo.ReactivateByVM(ctx, vmID, newPaidUntil)
	if err != nil || n3 != 1 {
		t.Fatalf("ReactivateByVM rows=%d err=%v", n3, err)
	}
	got2, err := repo.GetByVM(ctx, vmID)
	if err != nil || got2 == nil || got2.Status != model.SubscriptionStatusActive {
		t.Fatalf("after reactivate: %+v err=%v", got2, err)
	}
	if !got2.PaidUntil.Round(time.Second).Equal(newPaidUntil.Round(time.Second)) {
		t.Fatalf("paid_until not updated: got %v want %v", got2.PaidUntil, newPaidUntil)
	}
	if got2.ID != sub.ID {
		t.Fatalf("reactivated different row: got %d want %d", got2.ID, sub.ID)
	}

	// 二次 reactivate 应是 no-op（无 cancelled 行了）
	n4, err := repo.ReactivateByVM(ctx, vmID, newPaidUntil)
	if err != nil || n4 != 0 {
		t.Fatalf("idempotent ReactivateByVM rows=%d err=%v", n4, err)
	}
}

// TestOrderRepo_PeriodDefault 回归测试：orders 加 period 后 Create 走默认
// 'monthly'；新 CreateWithPeriod 能显式写 'daily'。SELECT 列扫描错位会让
// ExpiresAt 错位到 period 字段（string→*time.Time scan 会报错）。
func TestOrderRepo_PeriodDefault(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewOrderRepo(db)
	userID, _ := seedPayable(t, db, 100, 50)

	// 老签名 Create → period='monthly'
	o, err := repo.Create(context.Background(), userID, 1, 1, 25, "USD")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if o.Period != model.BillingPeriodMonthly {
		t.Fatalf("default period want monthly got %s", o.Period)
	}

	// 新签名 CreateWithPeriod → period='daily'
	o2, err := repo.CreateWithPeriod(context.Background(), userID, 1, 1, 1, "USD", model.BillingPeriodDaily)
	if err != nil {
		t.Fatalf("CreateWithPeriod: %v", err)
	}
	if o2.Period != model.BillingPeriodDaily {
		t.Fatalf("explicit period want daily got %s", o2.Period)
	}

	// GetByID 回读：也要能取到正确 period
	got, err := repo.GetByID(context.Background(), o2.ID)
	if err != nil || got == nil {
		t.Fatalf("GetByID: %v / %+v", err, got)
	}
	if got.Period != model.BillingPeriodDaily {
		t.Fatalf("GetByID period want daily got %s", got.Period)
	}
}
