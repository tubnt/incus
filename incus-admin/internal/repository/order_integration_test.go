//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/testhelper"
)

// seedPayable 插入一条用户 + 产品 + 集群 + 待支付订单，返回各自 id。
func seedPayable(t *testing.T, db *sql.DB, balance, amount float64) (userID, orderID int64) {
	t.Helper()
	ctx := context.Background()
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, role, balance) VALUES ($1,$2,'customer',$3) RETURNING id`,
		"u@test", "u", balance).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var clusterID, productID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO clusters (name, endpoint) VALUES ('c','https://x') RETURNING id`).Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO products (name, price, cpu, memory_mb, disk_gb) VALUES ('p',$1,1,1024,10) RETURNING id`,
		amount).Scan(&productID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO orders (user_id, product_id, cluster_id, amount, currency, status)
		 VALUES ($1,$2,$3,$4,'USD','pending') RETURNING id`,
		userID, productID, clusterID, amount).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return
}

func TestPayWithBalance_HappyPath(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewOrderRepo(db)
	userID, orderID := seedPayable(t, db, 100, 50)

	if err := repo.PayWithBalance(context.Background(), orderID); err != nil {
		t.Fatalf("PayWithBalance: %v", err)
	}

	var balance float64
	var status string
	var invoiceCnt, txnCnt int
	must := func(q string, args []any, dst ...any) {
		if err := db.QueryRow(q, args...).Scan(dst...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(`SELECT balance FROM users WHERE id=$1`, []any{userID}, &balance)
	must(`SELECT status FROM orders WHERE id=$1`, []any{orderID}, &status)
	must(`SELECT COUNT(*) FROM invoices WHERE order_id=$1`, []any{orderID}, &invoiceCnt)
	must(`SELECT COUNT(*) FROM transactions WHERE user_id=$1`, []any{userID}, &txnCnt)

	if balance != 50 {
		t.Fatalf("balance want 50 got %v", balance)
	}
	if status != model.OrderPaid {
		t.Fatalf("status want paid got %s", status)
	}
	if invoiceCnt != 1 {
		t.Fatalf("invoiceCnt want 1 got %d", invoiceCnt)
	}
	if txnCnt != 1 {
		t.Fatalf("txnCnt want 1 got %d", txnCnt)
	}
}

func TestPayWithBalance_InsufficientBalance(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewOrderRepo(db)
	userID, orderID := seedPayable(t, db, 10, 50)

	err := repo.PayWithBalance(context.Background(), orderID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var balance float64
	var status string
	var invoiceCnt, txnCnt int
	_ = db.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance)
	_ = db.QueryRow(`SELECT status FROM orders WHERE id=$1`, orderID).Scan(&status)
	_ = db.QueryRow(`SELECT COUNT(*) FROM invoices WHERE order_id=$1`, orderID).Scan(&invoiceCnt)
	_ = db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE user_id=$1`, userID).Scan(&txnCnt)

	if balance != 10 {
		t.Fatalf("balance must stay 10 on rollback, got %v", balance)
	}
	if status != model.OrderPending {
		t.Fatalf("order status must stay pending on rollback, got %s", status)
	}
	if invoiceCnt != 0 || txnCnt != 0 {
		t.Fatalf("rollback must leave no invoice/txn, got inv=%d txn=%d", invoiceCnt, txnCnt)
	}
}

func TestPayWithBalance_OrderNotPending(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewOrderRepo(db)
	userID, orderID := seedPayable(t, db, 100, 50)
	if _, err := db.Exec(`UPDATE orders SET status='paid' WHERE id=$1`, orderID); err != nil {
		t.Fatalf("seed paid: %v", err)
	}

	if err := repo.PayWithBalance(context.Background(), orderID); err == nil {
		t.Fatal("expected error for non-pending order")
	}

	var balance float64
	var txnCnt int
	_ = db.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance)
	_ = db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE user_id=$1`, userID).Scan(&txnCnt)
	if balance != 100 || txnCnt != 0 {
		t.Fatalf("non-pending must not mutate state, balance=%v txn=%d", balance, txnCnt)
	}
}

// TestPayWithBalance_ConcurrentPay 并发两次对同一订单发起支付，验证 FOR UPDATE
// 串行化：恰好一次成功、一次失败，balance 只被扣除一次。
func TestPayWithBalance_ConcurrentPay(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewOrderRepo(db)
	userID, orderID := seedPayable(t, db, 100, 50)

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = repo.PayWithBalance(context.Background(), orderID)
		}(i)
	}
	wg.Wait()

	var successCnt int
	for _, e := range results {
		if e == nil {
			successCnt++
		}
	}
	if successCnt != 1 {
		t.Fatalf("expected exactly 1 success, got %d; errs=%v", successCnt, results)
	}

	var balance float64
	var txnCnt int
	_ = db.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance)
	_ = db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE user_id=$1`, userID).Scan(&txnCnt)
	if balance != 50 {
		t.Fatalf("balance must be deducted exactly once, got %v", balance)
	}
	if txnCnt != 1 {
		t.Fatalf("transactions must be written exactly once, got %d", txnCnt)
	}
}
