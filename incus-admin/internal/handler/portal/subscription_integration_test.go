//go:build integration

package portal_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/handler/portal"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/testhelper"
)

// seedSubFlowFixtures 准备一组 user / cluster / product (含 daily 单价) / vm /
// 已 paid 订单，给 PLAN-054 Phase G hook 测试用。
func seedSubFlowFixtures(t *testing.T, db *sql.DB, period string, amount float64) (userID, orderID, vmID, productID int64) {
	t.Helper()
	ctx := context.Background()

	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, role, balance) VALUES ($1,$2,'customer',0) RETURNING id`,
		"sub-flow@test", "u").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var clusterID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO clusters (name, api_url) VALUES ('c-subflow','https://x') RETURNING id`).Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	// product 同时支持 monthly + daily，monthly=10，daily=0.5
	if err := db.QueryRowContext(ctx,
		`INSERT INTO products (name, price_monthly, price_daily, period_supported, cpu, memory_mb, disk_gb)
		 VALUES ('p-flow', 10, 0.5, ARRAY['monthly','daily'], 1, 1024, 10) RETURNING id`).Scan(&productID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO orders (user_id, product_id, cluster_id, amount, currency, period, status)
		 VALUES ($1,$2,$3,$4,'USD',$5,'paid') RETURNING id`,
		userID, productID, clusterID, amount, period).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO vms (name, cluster_id, user_id, order_id, status, cpu, memory_mb, disk_gb, os_image, node)
		 VALUES ('vm-subflow', $1, $2, $3, 'running', 1, 1024, 10, 'noop', 'n1') RETURNING id`,
		clusterID, userID, orderID).Scan(&vmID); err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	return
}

// TestPayHook_CreatesMonthlySubscription 验证 PLAN-054 Phase G 订单 pay →
// vm_subscriptions 行写入：monthly 周期默认走 PriceMonthly + paid_until ~ NOW+30d。
func TestPayHook_CreatesMonthlySubscription(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	userID, orderID, vmID, productID := seedSubFlowFixtures(t, db, model.BillingPeriodMonthly, 10.0)

	subRepo := repository.NewSubscriptionRepo(db)
	h := portal.NewOrderHandler(repository.NewOrderRepo(db), repository.NewProductRepo(db), nil, nil, nil, nil).
		WithSubscriptions(subRepo)

	order := &model.Order{
		ID: orderID, UserID: userID, ProductID: productID,
		Period: model.BillingPeriodMonthly, Amount: 10.0,
	}
	monthly := 10.0
	product := &model.Product{ID: productID, PriceMonthly: monthly, PeriodSupported: []string{"monthly"}}

	r := httptest.NewRequest("POST", "/portal/orders/1/pay", nil)
	if err := portal.CreateSubscriptionForOrderForTest(h, context.Background(), r, order, product, vmID); err != nil {
		t.Fatalf("CreateSubscriptionForOrderForTest: %v", err)
	}

	got, err := subRepo.GetByVM(context.Background(), vmID)
	if err != nil || got == nil {
		t.Fatalf("expected active sub, got %+v err=%v", got, err)
	}
	if got.Period != model.BillingPeriodMonthly {
		t.Errorf("sub.Period = %q, want monthly", got.Period)
	}
	if got.MonthlyRate == nil || *got.MonthlyRate != monthly {
		t.Errorf("sub.MonthlyRate = %v, want %v", got.MonthlyRate, monthly)
	}
	if got.DailyRate != nil {
		t.Errorf("sub.DailyRate = %v, want nil for monthly", *got.DailyRate)
	}
	wantWindow := 30 * 24 * time.Hour
	if delta := time.Until(got.PaidUntil); delta < wantWindow-time.Minute || delta > wantWindow+time.Minute {
		t.Errorf("paid_until %v not within 30d ± 1min from now (delta=%v)", got.PaidUntil, delta)
	}
}

// TestPayHook_CreatesDailySubscription 验证 daily 周期走 PriceDaily + paid_until
// ~ NOW + 24h。daily_rate 写入，monthly_rate 保 nil。
func TestPayHook_CreatesDailySubscription(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	userID, orderID, vmID, productID := seedSubFlowFixtures(t, db, model.BillingPeriodDaily, 0.5)

	subRepo := repository.NewSubscriptionRepo(db)
	h := portal.NewOrderHandler(repository.NewOrderRepo(db), repository.NewProductRepo(db), nil, nil, nil, nil).
		WithSubscriptions(subRepo)

	order := &model.Order{
		ID: orderID, UserID: userID, ProductID: productID,
		Period: model.BillingPeriodDaily, Amount: 0.5,
	}
	daily := 0.5
	product := &model.Product{ID: productID, PriceDaily: &daily, PeriodSupported: []string{"monthly", "daily"}}

	r := httptest.NewRequest("POST", "/portal/orders/1/pay", nil)
	if err := portal.CreateSubscriptionForOrderForTest(h, context.Background(), r, order, product, vmID); err != nil {
		t.Fatalf("CreateSubscriptionForOrderForTest: %v", err)
	}

	got, err := subRepo.GetByVM(context.Background(), vmID)
	if err != nil || got == nil {
		t.Fatalf("expected active sub, got %+v err=%v", got, err)
	}
	if got.Period != model.BillingPeriodDaily {
		t.Errorf("sub.Period = %q, want daily", got.Period)
	}
	if got.DailyRate == nil || *got.DailyRate != daily {
		t.Errorf("sub.DailyRate = %v, want %v", got.DailyRate, daily)
	}
	if got.MonthlyRate != nil {
		t.Errorf("sub.MonthlyRate = %v, want nil for daily", *got.MonthlyRate)
	}
	wantWindow := 24 * time.Hour
	if delta := time.Until(got.PaidUntil); delta < wantWindow-time.Minute || delta > wantWindow+time.Minute {
		t.Errorf("paid_until %v not within 24h ± 1min from now (delta=%v)", got.PaidUntil, delta)
	}
}

// TestTrashHook_CancelsActiveSub VM trash → sub 切 cancelled，paid_until 不动。
func TestTrashHook_CancelsActiveSub(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	userID, _, vmID, productID := seedSubFlowFixtures(t, db, model.BillingPeriodMonthly, 10.0)

	subRepo := repository.NewSubscriptionRepo(db)
	monthly := 10.0
	origPaidUntil := time.Now().Add(15 * 24 * time.Hour).Round(time.Second)
	if _, err := subRepo.Insert(context.Background(), &model.VMSubscription{
		VMID: vmID, ProductID: productID, UserID: userID,
		Period: model.BillingPeriodMonthly, MonthlyRate: &monthly,
		PaidUntil: origPaidUntil,
		Status:    model.SubscriptionStatusActive,
	}); err != nil {
		t.Fatalf("seed sub: %v", err)
	}

	r := httptest.NewRequest("DELETE", "/portal/services/1", nil)
	portal.CancelSubscriptionOnTrashForTest(context.Background(), r, subRepo, vmID)

	latest, _ := subRepo.GetLatestByVM(context.Background(), vmID)
	if latest == nil || latest.Status != model.SubscriptionStatusCancelled {
		t.Fatalf("expected cancelled, got %+v", latest)
	}
	if !latest.PaidUntil.Round(time.Second).Equal(origPaidUntil) {
		t.Errorf("trash should not change paid_until: got %v want %v", latest.PaidUntil, origPaidUntil)
	}
}

// TestRestoreHook_ReactivatesCancelledSub VM restore → sub 切 active +
// paid_until 重置到 NOW + 周期（monthly 30d / daily 24h），免费"恢复"语义。
func TestRestoreHook_ReactivatesCancelledSub(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	userID, _, vmID, productID := seedSubFlowFixtures(t, db, model.BillingPeriodDaily, 0.5)

	subRepo := repository.NewSubscriptionRepo(db)
	daily := 0.5
	// 制造一行 cancelled 行，模拟 trash 后状态
	pastPaidUntil := time.Now().Add(2 * time.Hour).Round(time.Second)
	sub, err := subRepo.Insert(context.Background(), &model.VMSubscription{
		VMID: vmID, ProductID: productID, UserID: userID,
		Period: model.BillingPeriodDaily, DailyRate: &daily,
		PaidUntil: pastPaidUntil,
		Status:    model.SubscriptionStatusActive,
	})
	if err != nil {
		t.Fatalf("seed sub: %v", err)
	}
	if _, err := subRepo.CancelByVM(context.Background(), vmID); err != nil {
		t.Fatalf("seed cancel: %v", err)
	}

	r := httptest.NewRequest("POST", "/portal/services/1/restore", nil)
	portal.ReactivateSubscriptionOnRestoreForTest(context.Background(), r, subRepo, vmID)

	got, err := subRepo.GetByVM(context.Background(), vmID)
	if err != nil || got == nil {
		t.Fatalf("expected active sub, got %+v err=%v", got, err)
	}
	if got.ID != sub.ID {
		t.Errorf("reactivated wrong row: got %d want %d", got.ID, sub.ID)
	}
	if got.Status != model.SubscriptionStatusActive {
		t.Errorf("status = %q, want active", got.Status)
	}
	wantWindow := 24 * time.Hour
	if delta := time.Until(got.PaidUntil); delta < wantWindow-time.Minute || delta > wantWindow+time.Minute {
		t.Errorf("paid_until %v not reset to ~ NOW+24h (delta=%v)", got.PaidUntil, delta)
	}
	if got.PaidUntil.Equal(pastPaidUntil) {
		t.Errorf("paid_until not reset (still %v)", got.PaidUntil)
	}
}

// TestPayHook_NilRepoIsNoop subs 未注入时 hook 应安静 return nil（不 panic、不报
// 错），保证测试 / 旧部署兼容。
func TestPayHook_NilRepoIsNoop(t *testing.T) {
	h := portal.NewOrderHandler(nil, nil, nil, nil, nil, nil)
	order := &model.Order{ID: 1, UserID: 1, ProductID: 1, Period: model.BillingPeriodMonthly, Amount: 10}
	product := &model.Product{ID: 1, PriceMonthly: 10}
	r := httptest.NewRequest("POST", "/portal/orders/1/pay", nil)
	if err := portal.CreateSubscriptionForOrderForTest(h, context.Background(), r, order, product, 999); err != nil {
		t.Errorf("nil subs should be noop, got err: %v", err)
	}
}
