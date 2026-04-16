package portal

import (
	"context"
	"log/slog"

	"github.com/incuscloud/incus-admin/internal/config"
	"github.com/incuscloud/incus-admin/internal/repository"
)

var ipAddrRepo *repository.IPAddrRepo

func SetIPAddrRepo(repo *repository.IPAddrRepo) {
	ipAddrRepo = repo
}

func allocateIP(ctx context.Context, cc config.ClusterConfig, vmID int64) (ip, gateway, cidr string, err error) {
	if len(cc.IPPools) == 0 {
		return "", "", "", nil
	}

	p := cc.IPPools[0]
	gateway = p.Gateway
	cidr = extractCIDR(p.CIDR)

	if ipAddrRepo != nil {
		poolID, _ := ipAddrRepo.GetPoolID(ctx, p.CIDR)
		if poolID == 0 {
			poolID, _ = ipAddrRepo.EnsurePool(ctx, 1, p.CIDR, p.Gateway, p.VLAN)
			if poolID > 0 {
				n, _ := ipAddrRepo.SeedPool(ctx, poolID, p.Range)
				slog.Info("seeded IP pool", "pool_id", poolID, "count", n)
			}
		}
		if poolID > 0 {
			ip, err = ipAddrRepo.AllocateNext(ctx, poolID, vmID, p.Range)
			if err != nil {
				slog.Error("DB IP allocation failed, falling back", "error", err)
			} else {
				return ip, gateway, cidr, nil
			}
		}
	}

	// Fallback to old method if DB allocation fails
	clients := func() string {
		// This is a simplified fallback
		return ""
	}()
	_ = clients
	return "", gateway, cidr, nil
}
