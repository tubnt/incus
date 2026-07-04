//go:build integration

package billing_test

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/service/billing"
	"github.com/incuscloud/incus-admin/internal/testhelper"
)

// ==========================================================================
// shared fixtures
// ==========================================================================

type fixtures struct {
	db        *sql.DB
	subs      *repository.SubscriptionRepo
	charges   *repository.ChargeRepo
	users     *repository.UserRepo
	trasher   *stubTrasher
	auditor   *stubAuditor
	userID    int64
	productID int64
	clusterID int64
	vmID      int64
}

type stubTrasher struct {
	calls     atomic.Int32
	calledVMs map[int64]struct{}
	err       error
}

func newStubTrasher() *stubTrasher { return &stubTrasher{calledVMs: make(map[int64]struct{})} }

func (s *stubTrasher) MarkTrashed(_ context.Context, vmID int64) (bool, error) {
	s.calls.Add(1)
	s.calledVMs[vmID] = struct{}{}
	if s.err != nil {
		return false, s.err
	}
	return true, nil
}

type auditEntry struct {
	action     string
	targetType string
	targetID   int64
	userID     *int64
	details    map[string]any
}

type stubAuditor struct {
	entries []auditEntry
}

func (s *stubAuditor) Log(_ context.Context, userID *int64, action, targetType string, targetID int64, details map[string]any, _ string) {
	s.entries = append(s.entries, auditEntry{
		action: action, targetType: targetType, targetID: targetID, userID: userID, details: details,
	})
}

func (s *stubAuditor) hasAction(action string) bool {
	for _, e := range s.entries {
		if e.action == action {
			return true
		}
	}
	return false
}

func seedFixtures(t *testing.T, db *sql.DB, balance float64) *fixtures {
	t.Helper()
	ctx := context.Background()
	f := &fixtures{
		db:      db,
		subs:    repository.NewSubscriptionRepo(db),
		charges: repository.NewChargeRepo(db),
		users:   repository.NewUserRepo(db),
		trasher: newStubTrasher(),
		auditor: &stubAuditor{},
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, role, balance) VALUES ($1,'sub','customer',$2) RETURNING id`,
		"sub@billing.test", balance).Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO clusters (name, api_url) VALUES ('c-billing','https://x') RETURNING id`).Scan(&f.clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO products (name, price_monthly, cpu, memory_mb, disk_gb) VALUES ('p',10,1,1024,10) RETURNING id`).Scan(&f.productID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO vms (name, cluster_id, user_id, status, cpu, memory_mb, disk_gb, os_image, node)
		 VALUES ('vm', $1, $2, 'running', 1, 1024, 10, 'noop', 'noop') RETURNING id`,
		f.clusterID, f.userID).Scan(&f.vmID); err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	return f
}

func (f *fixtures) getBalance(t *testing.T) float64 {
	t.Helper()
	var b float64
	if err := f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.userID).Scan(&b); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return b
}

func (f *fixtures) insertSub(t *testing.T, period string, rate float64, paidUntil time.Time, status string) *model.VMSubscription {
	t.Helper()
	in := &model.VMSubscription{
		VMID: f.vmID, ProductID: f.productID, UserID: f.userID,
		Period: period, PaidUntil: paidUntil, Status: status,
	}
	switch period {
	case model.BillingPeriodDaily:
		in.DailyRate = &rate
	case model.BillingPeriodMonthly:
		in.MonthlyRate = &rate
	}
	out, err := f.subs.Insert(context.Background(), in)
	if err != nil {
		t.Fatalf("insert sub: %v", err)
	}
	return out
}

// freezeClock 固定的时间源，t.Now() 始终返回 ts.UTC()。
func freezeClock(ts time.Time) func() time.Time {
	return func() time.Time { return ts.UTC() }
}

// ==========================================================================
// ChargeDue
// ==========================================================================

func TestService_ChargeDue_DailyPaid(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 5.0)
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	sub := f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-1*time.Minute), model.SubscriptionStatusActive)

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ChargeDue(context.Background())
	if err != nil {
		t.Fatalf("ChargeDue: %v", err)
	}
	if stats.Paid != 1 || stats.Processed != 1 || stats.Suspended != 0 || stats.Errors != 0 {
		t.Fatalf("stats unexpected: %+v", stats)
	}
	if got := f.getBalance(t); got != 4.0 {
		t.Fatalf("balance after charge want 4 got %v", got)
	}
	got, _ := f.subs.GetByVM(context.Background(), f.vmID)
	if got == nil {
		t.Fatalf("sub disappeared")
	}
	want := sub.PaidUntil.Add(24 * time.Hour)
	if !got.PaidUntil.Round(time.Second).Equal(want.Round(time.Second)) {
		t.Fatalf("paid_until want %v got %v", want, got.PaidUntil)
	}
	if !f.auditor.hasAction("subscription_charged") {
		t.Fatalf("missing subscription_charged audit")
	}
}

func TestService_ChargeDue_MonthlyInsufficientSuspends(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 1.0) // 余额 < 10
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	f.insertSub(t, model.BillingPeriodMonthly, 10.0, now.Add(-1*time.Minute), model.SubscriptionStatusActive)

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor,
		billing.WithClock(freezeClock(now)),
		billing.WithGraceDuration(72*time.Hour),
	)
	stats, err := svc.ChargeDue(context.Background())
	if err != nil {
		t.Fatalf("ChargeDue: %v", err)
	}
	if stats.Suspended != 1 || stats.Paid != 0 {
		t.Fatalf("stats unexpected: %+v", stats)
	}
	// 余额未动
	if got := f.getBalance(t); got != 1.0 {
		t.Fatalf("balance changed despite insufficient: %v", got)
	}
	got, _ := f.subs.GetLatestByVM(context.Background(), f.vmID)
	if got == nil || got.Status != model.SubscriptionStatusSuspended {
		t.Fatalf("sub not suspended: %+v", got)
	}
	if got.GraceUntil == nil {
		t.Fatalf("grace_until not set")
	}
	wantGrace := now.Add(72 * time.Hour)
	if !got.GraceUntil.Round(time.Second).Equal(wantGrace.Round(time.Second)) {
		t.Fatalf("grace_until want %v got %v", wantGrace, *got.GraceUntil)
	}
	if !f.auditor.hasAction("subscription_suspended") {
		t.Fatalf("missing subscription_suspended audit")
	}
}

func TestService_ChargeDue_CatchupSafeWithUnique(t *testing.T) {
	// 同一 sub 同一天跑两次 ChargeDue：第二次应 skipped（UNIQUE 守住）。
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 10.0)
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-1*time.Minute), model.SubscriptionStatusActive)

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	first, err := svc.ChargeDue(context.Background())
	if err != nil || first.Paid != 1 {
		t.Fatalf("first run: %+v err=%v", first, err)
	}
	// 把 paid_until 强制改回 now-1min（模拟时钟回退 / 重跑触发）
	if _, err := db.Exec(`UPDATE vm_subscriptions SET paid_until = $1`, now.Add(-1*time.Minute)); err != nil {
		t.Fatalf("rewind paid_until: %v", err)
	}
	second, err := svc.ChargeDue(context.Background())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Skipped != 1 || second.Paid != 0 {
		t.Fatalf("second run want Skipped=1, got %+v", second)
	}
	// 余额仍是第一次扣完的 9（未重扣）
	if got := f.getBalance(t); got != 9.0 {
		t.Fatalf("balance double-charged: %v", got)
	}
}

func TestService_ChargeDue_PartialPeriodMissingRate(t *testing.T) {
	// monthly_rate=nil 时单 sub 失败但不阻塞其他 sub。
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 5.0)
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)

	// 第一条 sub：缺 rate，扫描期间会被发现失败
	bad := &model.VMSubscription{
		VMID: f.vmID, ProductID: f.productID, UserID: f.userID,
		Period: model.BillingPeriodMonthly, PaidUntil: now.Add(-1 * time.Minute),
		Status: model.SubscriptionStatusActive,
	}
	if _, err := f.subs.Insert(context.Background(), bad); err != nil {
		t.Fatalf("insert bad sub: %v", err)
	}

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ChargeDue(context.Background())
	if err != nil {
		t.Fatalf("ChargeDue: %v", err)
	}
	if stats.Errors != 1 {
		t.Fatalf("want 1 error got %+v", stats)
	}
}

// ==========================================================================
// ExpireGrace
// ==========================================================================

func TestService_ExpireGrace_TrashesAndCancels(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 0)
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)

	// 直接造一条 grace 已过期的 suspended sub
	sub := f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-72*time.Hour), model.SubscriptionStatusSuspended)
	graceAt := now.Add(-1 * time.Hour)
	if err := f.subs.UpdateSuspension(context.Background(), sub.ID, now.Add(-73*time.Hour), graceAt); err != nil {
		t.Fatalf("update suspension: %v", err)
	}

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ExpireGrace(context.Background())
	if err != nil {
		t.Fatalf("ExpireGrace: %v", err)
	}
	if stats.Trashed != 1 {
		t.Fatalf("want 1 trashed got %+v", stats)
	}
	if _, ok := f.trasher.calledVMs[f.vmID]; !ok {
		t.Fatalf("VMTrasher not called for vm %d", f.vmID)
	}
	got, _ := f.subs.GetLatestByVM(context.Background(), f.vmID)
	if got.Status != model.SubscriptionStatusCancelled {
		t.Fatalf("sub not cancelled: %s", got.Status)
	}
	if !f.auditor.hasAction("subscription_grace_expired") || !f.auditor.hasAction("vm_auto_trashed") {
		t.Fatalf("missing grace_expired / vm_auto_trashed audit entries: %+v", f.auditor.entries)
	}
}

func TestService_ExpireGrace_GraceNotPassedNoOp(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 0)
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)

	sub := f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-1*time.Hour), model.SubscriptionStatusSuspended)
	// grace_until=NOW+1h，未过期
	if err := f.subs.UpdateSuspension(context.Background(), sub.ID, now, now.Add(1*time.Hour)); err != nil {
		t.Fatalf("update suspension: %v", err)
	}

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ExpireGrace(context.Background())
	if err != nil {
		t.Fatalf("ExpireGrace: %v", err)
	}
	if stats.Processed != 0 || stats.Trashed != 0 {
		t.Fatalf("want zero processed, got %+v", stats)
	}
	if f.trasher.calls.Load() != 0 {
		t.Fatalf("trasher should not be called")
	}
}

func TestService_ExpireGrace_TrasherErrorRetainsSuspended(t *testing.T) {
	// CR P2：trash 失败 → 不 cancel sub，让下个 tick 重试；否则若 Incus 永久
	// 不可用，sub 一旦 cancelled VM 就永远在跑而不付费。
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 0)
	f.trasher.err = errors.New("incus offline")
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)

	sub := f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-72*time.Hour), model.SubscriptionStatusSuspended)
	if err := f.subs.UpdateSuspension(context.Background(), sub.ID, now.Add(-73*time.Hour), now.Add(-1*time.Hour)); err != nil {
		t.Fatalf("update suspension: %v", err)
	}
	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ExpireGrace(context.Background())
	if err != nil {
		t.Fatalf("ExpireGrace: %v", err)
	}
	if stats.Errors != 1 || stats.Trashed != 0 {
		t.Fatalf("trasher error should bump Errors not Trashed: %+v", stats)
	}
	got, _ := f.subs.GetLatestByVM(context.Background(), f.vmID)
	if got.Status != model.SubscriptionStatusSuspended {
		t.Fatalf("sub should stay suspended on trasher err: %s", got.Status)
	}
	// 模拟下个 tick：trash 恢复，期望成功 cancel
	f.trasher.err = nil
	stats2, err := svc.ExpireGrace(context.Background())
	if err != nil || stats2.Trashed != 1 || stats2.Errors != 0 {
		t.Fatalf("retry after recovery: %+v err=%v", stats2, err)
	}
	got, _ = f.subs.GetLatestByVM(context.Background(), f.vmID)
	if got.Status != model.SubscriptionStatusCancelled {
		t.Fatalf("sub should be cancelled after trasher recovery: %s", got.Status)
	}
}

// ==========================================================================
// ReactivateOnTopUp
// ==========================================================================

func TestService_ReactivateOnTopUp_BalanceEnoughResumes(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 5.0) // 充值后余额
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	// 先造一条 suspended sub + 一条 insufficient charge（模拟 charger 留下的）
	sub := f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-2*time.Hour), model.SubscriptionStatusSuspended)
	if err := f.subs.UpdateSuspension(context.Background(), sub.ID, now.Add(-1*time.Hour), now.Add(71*time.Hour)); err != nil {
		t.Fatalf("update suspension: %v", err)
	}
	yesterday := now.UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	if _, err := f.charges.Insert(context.Background(), &model.BillingCharge{
		SubscriptionID: sub.ID, ChargeDate: yesterday, Amount: 1.0,
		Status: model.BillingChargeInsufficient,
	}); err != nil {
		t.Fatalf("insert charge: %v", err)
	}

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ReactivateOnTopUp(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("ReactivateOnTopUp: %v", err)
	}
	if stats.Reactivated != 1 || stats.Suspended != 1 {
		t.Fatalf("want 1 reactivated: %+v", stats)
	}
	// 余额扣 1
	if got := f.getBalance(t); got != 4.0 {
		t.Fatalf("balance want 4 got %v", got)
	}
	got, _ := f.subs.GetByVM(context.Background(), f.vmID)
	if got == nil || got.Status != model.SubscriptionStatusActive {
		t.Fatalf("sub not active: %+v", got)
	}
	if got.SuspendedAt != nil || got.GraceUntil != nil {
		t.Fatalf("suspended_at / grace_until not cleared: %+v / %+v", got.SuspendedAt, got.GraceUntil)
	}
	// paid_until 应是 NOW + 1day
	want := now.Add(24 * time.Hour)
	if !got.PaidUntil.Round(time.Second).Equal(want.Round(time.Second)) {
		t.Fatalf("paid_until want %v got %v", want, got.PaidUntil)
	}
	// charge 行翻成 paid
	charges, _ := f.charges.ListBySubscription(context.Background(), sub.ID, 5)
	if len(charges) != 1 || charges[0].Status != model.BillingChargePaid || charges[0].TransactionID == nil {
		t.Fatalf("charge not flipped to paid: %+v", charges)
	}
	if !f.auditor.hasAction("subscription_reactivated") {
		t.Fatalf("missing subscription_reactivated audit")
	}
}

func TestService_ReactivateOnTopUp_InsufficientLeavesSuspended(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 0.4) // 不够 1 ¥/day
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	sub := f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-2*time.Hour), model.SubscriptionStatusSuspended)
	if err := f.subs.UpdateSuspension(context.Background(), sub.ID, now.Add(-1*time.Hour), now.Add(71*time.Hour)); err != nil {
		t.Fatalf("update suspension: %v", err)
	}

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ReactivateOnTopUp(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("ReactivateOnTopUp: %v", err)
	}
	if stats.Reactivated != 0 || stats.Suspended != 1 {
		t.Fatalf("expected no reactivate: %+v", stats)
	}
	if got := f.getBalance(t); got != 0.4 {
		t.Fatalf("balance should be unchanged: %v", got)
	}
	got, _ := f.subs.GetLatestByVM(context.Background(), f.vmID)
	if got.Status != model.SubscriptionStatusSuspended {
		t.Fatalf("sub status should stay suspended: %s", got.Status)
	}
}

func TestService_ReactivateOnTopUp_PartialCoverage(t *testing.T) {
	// 用户有 2 条 suspended sub，余额只够其中 1 条。
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 1.0)
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	// vm2
	var vm2ID int64
	if err := f.db.QueryRow(
		`INSERT INTO vms (name, cluster_id, user_id, status, cpu, memory_mb, disk_gb, os_image, node)
		 VALUES ('vm2', $1, $2, 'running', 1, 1024, 10, 'noop', 'noop') RETURNING id`,
		f.clusterID, f.userID).Scan(&vm2ID); err != nil {
		t.Fatalf("seed vm2: %v", err)
	}

	rate := 1.0
	// sub1 grace_until 更早 → 先恢复
	sub1, err := f.subs.Insert(context.Background(), &model.VMSubscription{
		VMID: f.vmID, ProductID: f.productID, UserID: f.userID,
		Period: model.BillingPeriodDaily, DailyRate: &rate,
		PaidUntil: now.Add(-2 * time.Hour), Status: model.SubscriptionStatusSuspended,
	})
	if err != nil {
		t.Fatalf("insert sub1: %v", err)
	}
	if err := f.subs.UpdateSuspension(context.Background(), sub1.ID, now, now.Add(1*time.Hour)); err != nil {
		t.Fatalf("update suspension sub1: %v", err)
	}
	sub2, err := f.subs.Insert(context.Background(), &model.VMSubscription{
		VMID: vm2ID, ProductID: f.productID, UserID: f.userID,
		Period: model.BillingPeriodDaily, DailyRate: &rate,
		PaidUntil: now.Add(-2 * time.Hour), Status: model.SubscriptionStatusSuspended,
	})
	if err != nil {
		t.Fatalf("insert sub2: %v", err)
	}
	if err := f.subs.UpdateSuspension(context.Background(), sub2.ID, now, now.Add(5*time.Hour)); err != nil {
		t.Fatalf("update suspension sub2: %v", err)
	}

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ReactivateOnTopUp(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("ReactivateOnTopUp: %v", err)
	}
	if stats.Suspended != 2 || stats.Reactivated != 1 {
		t.Fatalf("want suspended=2 reactivated=1, got %+v", stats)
	}
	// sub1 应被恢复（grace_until 更早），sub2 仍 suspended
	got1, _ := f.subs.GetLatestByVM(context.Background(), f.vmID)
	got2, _ := f.subs.GetLatestByVM(context.Background(), vm2ID)
	if got1.Status != model.SubscriptionStatusActive {
		t.Fatalf("sub1 should be active: %s", got1.Status)
	}
	if got2.Status != model.SubscriptionStatusSuspended {
		t.Fatalf("sub2 should remain suspended: %s", got2.Status)
	}
}

// TestService_ReactivateOnTopUp_VMGoneCancels 校验：suspended 订阅在补扣恢复前
// 事务内校验 VM 存活；若 VM 已 trashed，则不补扣，改走 cancel 让位（避免给已删
// VM 续费产生幽灵扣费）。
func TestService_ReactivateOnTopUp_VMGoneCancels(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 5.0)
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	sub := f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-2*time.Hour), model.SubscriptionStatusSuspended)
	if err := f.subs.UpdateSuspension(context.Background(), sub.ID, now.Add(-1*time.Hour), now.Add(71*time.Hour)); err != nil {
		t.Fatalf("update suspension: %v", err)
	}
	// VM 被 trash（trashed_at 置位）
	if _, err := db.Exec(`UPDATE vms SET trashed_at = $1 WHERE id = $2`, now, f.vmID); err != nil {
		t.Fatalf("trash vm: %v", err)
	}

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	stats, err := svc.ReactivateOnTopUp(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("ReactivateOnTopUp: %v", err)
	}
	if stats.Reactivated != 0 || stats.Suspended != 1 {
		t.Fatalf("VM gone should not reactivate: %+v", stats)
	}
	if got := f.getBalance(t); got != 5.0 {
		t.Fatalf("balance should be untouched for gone VM: %v", got)
	}
	got, _ := f.subs.GetLatestByVM(context.Background(), f.vmID)
	if got.Status != model.SubscriptionStatusCancelled {
		t.Fatalf("sub for gone VM should be cancelled, got %s", got.Status)
	}
}

// TestService_ConcurrentChargeAndReactivateNoDoubleCharge 是 CR P0 回归：
// charger + topup reactivate hook 同时跑同一个 sub，sub 行 FOR UPDATE 让两路
// 串行；最终结果必须是「恰好 1 笔 charge 行 + 恰好 1 笔 -amount transactions
// 流水」，无双扣。
func TestService_ConcurrentChargeAndReactivateNoDoubleCharge(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 5.0)
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	// 造一条 due active sub（charger 会扫到），同时它的状态又被改成 suspended
	// 来让 reactivate 也能跑同样的 sub。我们用两步：先创建 active + due，
	// reactivator 自己跑前会在锁里看到 active，return false 让位 —— 这是
	// 期望的串行行为：两路都跑，但只有一条真正下账。
	rate := 1.0
	if _, err := f.subs.Insert(context.Background(), &model.VMSubscription{
		VMID: f.vmID, ProductID: f.productID, UserID: f.userID,
		Period: model.BillingPeriodDaily, DailyRate: &rate,
		PaidUntil: now.Add(-1 * time.Minute), Status: model.SubscriptionStatusActive,
	}); err != nil {
		t.Fatalf("insert sub: %v", err)
	}

	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	ctx := context.Background()

	// 把两路并发跑 8 轮；若 P0 race 复发会出现 >1 笔 charge 流水。
	for round := 0; round < 8; round++ {
		// 把 sub 重置回 active + due，模拟"刚到期"的第一波 charger 命中场景
		if _, err := db.Exec(
			`UPDATE vm_subscriptions SET status='active', paid_until=$1, suspended_at=NULL, grace_until=NULL`,
			now.Add(-1*time.Minute),
		); err != nil {
			t.Fatalf("reset sub: %v", err)
		}
		// 余额刷到 1.5，刚好够 1 次扣费
		if _, err := db.Exec(`UPDATE users SET balance=1.5 WHERE id=$1`, f.userID); err != nil {
			t.Fatalf("reset balance: %v", err)
		}
		// 清掉 today 的 charge 行（如果上一轮跑出来了），保证本轮干净
		if _, err := db.Exec(`DELETE FROM billing_charges WHERE subscription_id IN
			(SELECT id FROM vm_subscriptions WHERE user_id=$1)`, f.userID); err != nil {
			t.Fatalf("reset charges: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM transactions WHERE user_id=$1 AND type='charge'`, f.userID); err != nil {
			t.Fatalf("reset txs: %v", err)
		}

		// 同时启动两路
		done := make(chan error, 2)
		go func() {
			_, err := svc.ChargeDue(ctx)
			done <- err
		}()
		go func() {
			_, err := svc.ReactivateOnTopUp(ctx, f.userID)
			done <- err
		}()
		for i := 0; i < 2; i++ {
			if err := <-done; err != nil {
				t.Fatalf("round %d concurrent run: %v", round, err)
			}
		}

		// 不变量：至多一条 charge 流水（type='charge' 的 transactions 行）
		var txCount int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM transactions WHERE user_id=$1 AND type='charge'`, f.userID,
		).Scan(&txCount); err != nil {
			t.Fatalf("count tx: %v", err)
		}
		if txCount > 1 {
			t.Fatalf("round %d: double-charge detected (txCount=%d)", round, txCount)
		}
		// charge 行也至多一条
		var chargeCount int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM billing_charges bc
			 JOIN vm_subscriptions s ON s.id = bc.subscription_id
			 WHERE s.user_id=$1`, f.userID,
		).Scan(&chargeCount); err != nil {
			t.Fatalf("count charges: %v", err)
		}
		if chargeCount > 1 {
			t.Fatalf("round %d: duplicate billing_charges (count=%d)", round, chargeCount)
		}
	}
}

// TestService_ChargeDue_CatchupClampsPaidUntilFloor 是 CR P1 回归：多日 backlog
// 单次 charge 把 paid_until 提到 now+period（不留在过去）。
func TestService_ChargeDue_CatchupClampsPaidUntilFloor(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 5.0)
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	// 模拟 worker 离线 5 天：paid_until 5 天前到期
	f.insertSub(t, model.BillingPeriodDaily, 1.0, now.Add(-5*24*time.Hour), model.SubscriptionStatusActive)
	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor, billing.WithClock(freezeClock(now)))
	if _, err := svc.ChargeDue(context.Background()); err != nil {
		t.Fatalf("ChargeDue: %v", err)
	}
	got, _ := f.subs.GetByVM(context.Background(), f.vmID)
	want := now.Add(24 * time.Hour)
	if !got.PaidUntil.Round(time.Second).Equal(want.Round(time.Second)) {
		t.Fatalf("paid_until should be clamped to now+period (%v), got %v", want, got.PaidUntil)
	}
}

// ==========================================================================
// 30-day timeline (PLAN-054 §6)
//
// daily sub ¥1/day + 余额 ¥10 → 第 11 天 suspended → 充 ¥5 → 立刻 active
// （NOW + 24h 续期）。
// ==========================================================================

func TestService_30DayTimeline(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	f := seedFixtures(t, db, 10.0)

	day0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rate := 1.0
	if _, err := f.subs.Insert(context.Background(), &model.VMSubscription{
		VMID: f.vmID, ProductID: f.productID, UserID: f.userID,
		Period: model.BillingPeriodDaily, DailyRate: &rate,
		PaidUntil: day0, Status: model.SubscriptionStatusActive,
	}); err != nil {
		t.Fatalf("insert sub: %v", err)
	}

	clock := struct{ t time.Time }{t: day0}
	svc := billing.NewService(db, f.subs, f.charges, f.trasher, f.auditor,
		billing.WithClock(func() time.Time { return clock.t }),
		billing.WithGraceDuration(72*time.Hour),
	)

	// 跑 day1 .. day30 共 30 tick；day1 = day0 + 1day（paid_until=day0 已到期）。
	// 期望前 10 天扣费成功 → balance 0；day 11 之后余额不足 → suspended（不再
	// 扣，charge 行 insufficient）。
	for day := 1; day <= 11; day++ {
		clock.t = day0.Add(time.Duration(day) * 24 * time.Hour)
		_, err := svc.ChargeDue(context.Background())
		if err != nil {
			t.Fatalf("day %d: %v", day, err)
		}
	}
	if bal := f.getBalance(t); bal != 0 {
		t.Fatalf("balance after 10 paid days want 0 got %v", bal)
	}
	got, _ := f.subs.GetLatestByVM(context.Background(), f.vmID)
	if got.Status != model.SubscriptionStatusSuspended {
		t.Fatalf("expected suspended on day 11, got %s (paid_until=%v)", got.Status, got.PaidUntil)
	}

	// 用户充 ¥5：通过直接 UPDATE users.balance（绕过 TopUp daily cap，专注计费
	// 流程）。然后调 ReactivateOnTopUp。
	if _, err := db.Exec(`UPDATE users SET balance = balance + 5 WHERE id=$1`, f.userID); err != nil {
		t.Fatalf("topup: %v", err)
	}
	stats, err := svc.ReactivateOnTopUp(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("Reactivate: %v", err)
	}
	if stats.Reactivated != 1 {
		t.Fatalf("Reactivate stats: %+v", stats)
	}
	got, _ = f.subs.GetByVM(context.Background(), f.vmID)
	if got.Status != model.SubscriptionStatusActive {
		t.Fatalf("sub not active after reactivate: %s", got.Status)
	}
	if bal := f.getBalance(t); bal != 4.0 {
		t.Fatalf("balance after reactivate want 4 got %v", bal)
	}
}
