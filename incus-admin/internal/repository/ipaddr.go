package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"time"
)

type IPAddrRepo struct {
	db *sql.DB
}

func NewIPAddrRepo(db *sql.DB) *IPAddrRepo {
	return &IPAddrRepo{db: db}
}

type AllocatedIP struct {
	ID     int64  `json:"id"`
	PoolID int64  `json:"pool_id"`
	IP     string `json:"ip"`
	VMID   *int64 `json:"vm_id"`
	Status string `json:"status"`
}

func (r *IPAddrRepo) AllocateNext(ctx context.Context, poolID int64, vmID int64, ipRange string) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var ip string
	err = tx.QueryRowContext(ctx,
		`SELECT ip::text FROM ip_addresses WHERE pool_id = $1 AND status = 'available' ORDER BY ip LIMIT 1 FOR UPDATE SKIP LOCKED`,
		poolID,
	).Scan(&ip)

	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no available IPs in pool %d", poolID)
	}
	if err != nil {
		return "", fmt.Errorf("select IP: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE ip_addresses SET vm_id = $1, status = 'assigned', updated_at = $2 WHERE pool_id = $3 AND ip = $4::inet`,
		vmID, time.Now(), poolID, ip,
	)
	if err != nil {
		return "", fmt.Errorf("assign IP: %w", err)
	}

	return ip, tx.Commit()
}

func (r *IPAddrRepo) Release(ctx context.Context, ip string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE ip_addresses SET vm_id = NULL, status = 'cooldown', cooldown_until = $1, updated_at = $2 WHERE ip = $3::inet`,
		time.Now().Add(5*time.Minute), time.Now(), ip,
	)
	return err
}

func (r *IPAddrRepo) SeedPool(ctx context.Context, poolID int64, ipRange string) (int, error) {
	parts := strings.Split(ipRange, "-")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid range: %s", ipRange)
	}

	startIP := net.ParseIP(strings.TrimSpace(parts[0])).To4()
	endIP := net.ParseIP(strings.TrimSpace(parts[1])).To4()
	if startIP == nil || endIP == nil {
		return 0, fmt.Errorf("invalid IPs in range")
	}

	count := 0
	for ip := copyIP(startIP); !ip.Equal(endIP); incIP(ip) {
		_, err := r.db.ExecContext(ctx,
			`INSERT INTO ip_addresses (pool_id, ip, status) VALUES ($1, $2::inet, 'available') ON CONFLICT (ip) DO NOTHING`,
			poolID, ip.String(),
		)
		if err != nil {
			return count, err
		}
		count++
	}
	// Include the end IP
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO ip_addresses (pool_id, ip, status) VALUES ($1, $2::inet, 'available') ON CONFLICT (ip) DO NOTHING`,
		poolID, endIP.String(),
	)
	if err != nil {
		return count, err
	}
	count++
	return count, nil
}

func (r *IPAddrRepo) RecoverCooldowns(ctx context.Context) (int, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE ip_addresses SET status = 'available', cooldown_until = NULL WHERE status = 'cooldown' AND cooldown_until < $1`,
		time.Now(),
	)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

func (r *IPAddrRepo) GetPoolID(ctx context.Context, cidr string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM ip_pools WHERE cidr = $1`, cidr).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (r *IPAddrRepo) EnsurePool(ctx context.Context, clusterID int64, cidr, gateway string, vlanID int) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO ip_pools (cluster_id, cidr, gateway, vlan_id) VALUES ($1, $2::cidr, $3::inet, $4)
		 ON CONFLICT (cidr) DO UPDATE SET gateway = EXCLUDED.gateway
		 RETURNING id`,
		clusterID, cidr, gateway, vlanID,
	).Scan(&id)
	return id, err
}

func copyIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}
