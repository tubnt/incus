//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/testhelper"
)

func seedUser(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO users (email, name, role) VALUES ($1,$2,'customer') RETURNING id`,
		"u@quota", "u",
	).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func insertTxn(t *testing.T, db *sql.DB, userID int64, amount float64, typ string, ago time.Duration) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO transactions (user_id, amount, type, created_at) VALUES ($1,$2,$3,$4)`,
		userID, amount, typ, time.Now().Add(-ago),
	)
	if err != nil {
		t.Fatalf("insert txn: %v", err)
	}
}

func TestSumDepositsSince_WindowAndTypeFilter(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewUserRepo(db)
	userID := seedUser(t, db)

	// 窗口内 deposit 两笔：1000 + 2500
	insertTxn(t, db, userID, 1000, "deposit", 1*time.Hour)
	insertTxn(t, db, userID, 2500, "deposit", 10*time.Hour)
	// 窗口外 deposit：不计入
	insertTxn(t, db, userID, 9999, "deposit", 48*time.Hour)
	// 窗口内但 type=payment：不计入
	insertTxn(t, db, userID, 500, "payment", 2*time.Hour)

	sum, err := repo.SumDepositsSince(context.Background(), userID, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SumDepositsSince: %v", err)
	}
	if sum != 3500 {
		t.Fatalf("sum want 3500 got %v", sum)
	}
}

func TestSumDepositsSince_NoRows(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewUserRepo(db)
	userID := seedUser(t, db)

	sum, err := repo.SumDepositsSince(context.Background(), userID, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SumDepositsSince: %v", err)
	}
	if sum != 0 {
		t.Fatalf("empty sum want 0 got %v", sum)
	}
}

// TestTopUpWithDailyCap_ConcurrentRespectsCap 并发打 10 个 $400 的请求，
// cap=1000 → 期望至多 2 笔成功（总额不超过 cap）。失败原因必须为额度不足
// 而非错误。这是 D.2 修复的并发越限 bug 的回归测试。
func TestTopUpWithDailyCap_ConcurrentRespectsCap(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewUserRepo(db)
	userID := seedUser(t, db)

	const (
		perCall = 400.0
		cap     = 1000.0
		window  = 24 * time.Hour
		n       = 10
	)

	var okCount, quotaCount int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, ok, err := repo.TopUpWithDailyCap(
				context.Background(), userID, perCall, "concurrent", nil, window, cap,
			)
			if err != nil {
				t.Errorf("TopUpWithDailyCap error: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&okCount, 1)
			} else {
				atomic.AddInt32(&quotaCount, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if okCount+quotaCount != n {
		t.Fatalf("accounted calls want %d got ok=%d quota=%d", n, okCount, quotaCount)
	}
	if okCount > 2 {
		t.Fatalf("more successes than cap allows: ok=%d (cap=%.0f / per=%.0f)", okCount, cap, perCall)
	}

	var balance float64
	if err := db.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance > cap {
		t.Fatalf("balance %.2f exceeds cap %.2f", balance, cap)
	}
	want := float64(okCount) * perCall
	if balance != want {
		t.Fatalf("balance want %.2f got %.2f", want, balance)
	}
}

func TestTopUpWithDailyCap_QuotaExceededReturnsOkFalse(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewUserRepo(db)
	userID := seedUser(t, db)

	ctx := context.Background()
	if _, _, ok, err := repo.TopUpWithDailyCap(ctx, userID, 800, "first", nil, 24*time.Hour, 1000); err != nil || !ok {
		t.Fatalf("first call: ok=%v err=%v", ok, err)
	}
	used, newBal, ok, err := repo.TopUpWithDailyCap(ctx, userID, 300, "second", nil, 24*time.Hour, 1000)
	if err != nil {
		t.Fatalf("second call err: %v", err)
	}
	if ok {
		t.Fatalf("second call should fail cap: used=%.2f newBal=%.2f", used, newBal)
	}
	if used != 800 {
		t.Fatalf("used want 800 got %.2f", used)
	}
	if newBal != 0 {
		t.Fatalf("failed call must return zero newBalance, got %.2f", newBal)
	}
}
