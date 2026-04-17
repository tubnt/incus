//go:build integration

package portal_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/incuscloud/incus-admin/internal/handler/portal"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/testhelper"
)

func seedPaidOrderAndIP(t *testing.T, db *sql.DB, amount float64) (userID, orderID, poolID int64, ip string) {
	t.Helper()
	ctx := context.Background()

	// balance already deducted (simulating successful PayWithBalance before rollback)
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, role, balance) VALUES ($1,$2,'customer',$3) RETURNING id`,
		"u@rollback.test", "u", 0.0).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var clusterID, productID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO clusters (name, endpoint) VALUES ('c1','https://x') RETURNING id`).Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO products (name, price, cpu, memory_mb, disk_gb) VALUES ('p',$1,1,1024,10) RETURNING id`,
		amount).Scan(&productID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO orders (user_id, product_id, cluster_id, amount, currency, status)
		 VALUES ($1,$2,$3,$4,'USD','paid') RETURNING id`,
		userID, productID, clusterID, amount).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	if err := db.QueryRowContext(ctx,
		`INSERT INTO ip_pools (cluster_id, cidr, gateway) VALUES ($1,'10.99.0.0/24','10.99.0.1') RETURNING id`,
		clusterID).Scan(&poolID); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	ip = "10.99.0.42"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO ip_addresses (pool_id, ip, status) VALUES ($1,$2::inet,'assigned')`,
		poolID, ip); err != nil {
		t.Fatalf("seed ip: %v", err)
	}
	return
}

// TestPay_RollbackOnIPAllocFail proves the compensation chain in OrderHandler.rollbackPayment:
// after a successful payment whose subsequent IP allocation (or provisioning) fails, the user is
// refunded, the IP cooled down, and the order is marked cancelled. Driven through exported
// RollbackPaymentForTest to keep internal state accessible without building a full cluster stack.
func TestPay_RollbackOnIPAllocFail(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	amount := 50.0
	userID, orderID, _, ip := seedPaidOrderAndIP(t, db, amount)

	portal.SetUserRepo(repository.NewUserRepo(db))
	portal.SetIPAddrRepo(repository.NewIPAddrRepo(db))
	t.Cleanup(func() {
		portal.SetUserRepo(nil)
		portal.SetIPAddrRepo(nil)
	})

	h := portal.NewOrderHandler(repository.NewOrderRepo(db), nil, nil, nil, nil, nil)

	order := &model.Order{ID: orderID, UserID: userID, Amount: amount}
	portal.RollbackPaymentForTest(h, context.Background(), order, ip, "ip allocation failed: no pools")

	var balance float64
	if err := db.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&balance); err != nil {
		t.Fatalf("load balance: %v", err)
	}
	if balance != amount {
		t.Errorf("balance = %v, want %v (refund)", balance, amount)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id=$1`, orderID).Scan(&status); err != nil {
		t.Fatalf("load order: %v", err)
	}
	if status != model.OrderCancelled {
		t.Errorf("order.status = %q, want cancelled", status)
	}

	var ipStatus string
	var vmID sql.NullInt64
	if err := db.QueryRow(`SELECT status, vm_id FROM ip_addresses WHERE ip=$1::inet`, ip).Scan(&ipStatus, &vmID); err != nil {
		t.Fatalf("load ip: %v", err)
	}
	if ipStatus != "cooldown" {
		t.Errorf("ip.status = %q, want cooldown", ipStatus)
	}
	if vmID.Valid {
		t.Errorf("ip.vm_id = %v, want NULL", vmID.Int64)
	}

	var refundAmount float64
	var txType string
	if err := db.QueryRow(
		`SELECT amount, type FROM transactions WHERE user_id=$1 AND order_id=$2 ORDER BY id DESC LIMIT 1`,
		userID, orderID).Scan(&refundAmount, &txType); err != nil {
		t.Fatalf("load refund tx: %v", err)
	}
	if refundAmount != amount {
		t.Errorf("refund.amount = %v, want %v", refundAmount, amount)
	}
	if txType != "refund" {
		t.Errorf("refund.type = %q, want refund", txType)
	}
}
