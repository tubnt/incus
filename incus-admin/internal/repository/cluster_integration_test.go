//go:build integration

package repository_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/testhelper"
)

// PLAN-053 Phase C：migration 027 加 region metadata 字段，要求所有"旧行"
// （INSERT 时不带新列）都拿到 migration 设的默认值：
//   region_status='available' / capabilities=["instances"]
// 同时验证 GetByID / List / ListFull 三条 SELECT 路径都会正确反序列化 JSONB。
func TestClusterRepo_RegionMetadataDefaults(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewClusterRepo(db)
	ctx := context.Background()

	// 模拟"老行"：只插基础列，不带 country/city/region_status/capabilities。
	var id int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO clusters (name, display_name, api_url, status)
		 VALUES ($1,$2,$3,'active') RETURNING id`,
		"r1", "Region One", "https://r1.test").Scan(&id); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	wantCaps := []string{"instances"}

	check := func(label string, c *model.Cluster) {
		t.Helper()
		if c == nil {
			t.Fatalf("%s: cluster nil", label)
		}
		if c.RegionStatus != model.RegionStatusAvailable {
			t.Errorf("%s: RegionStatus=%q, want %q", label, c.RegionStatus, model.RegionStatusAvailable)
		}
		if !reflect.DeepEqual(c.Capabilities, wantCaps) {
			t.Errorf("%s: Capabilities=%v, want %v", label, c.Capabilities, wantCaps)
		}
		if c.Country != "" {
			t.Errorf("%s: Country=%q, want empty", label, c.Country)
		}
		if c.City != "" {
			t.Errorf("%s: City=%q, want empty", label, c.City)
		}
	}

	byID, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	check("GetByID", byID)

	byName, err := repo.GetByName(ctx, "r1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	check("GetByName", byName)

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len=%d, want 1", len(list))
	}
	check("List", &list[0])

	full, err := repo.ListFull(ctx)
	if err != nil {
		t.Fatalf("ListFull: %v", err)
	}
	if len(full) != 1 {
		t.Fatalf("ListFull len=%d, want 1", len(full))
	}
	check("ListFull", &full[0])
}

// 显式写过 region metadata 的行：能原样读回（country/city 非空，capabilities 多元素）。
func TestClusterRepo_RegionMetadataExplicit(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	repo := repository.NewClusterRepo(db)
	ctx := context.Background()

	var id int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO clusters (name, display_name, api_url, status,
		                       country, city, region_status, capabilities)
		 VALUES ($1,$2,$3,'active', $4, $5, $6, $7::jsonb)
		 RETURNING id`,
		"r2", "Region Two", "https://r2.test",
		"CN", "Shenzhen", "maintenance", `["instances","load-balancers"]`,
	).Scan(&id); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatalf("cluster nil")
	}
	if got.Country != "CN" || got.City != "Shenzhen" {
		t.Errorf("country/city: got %q/%q want CN/Shenzhen", got.Country, got.City)
	}
	if got.RegionStatus != model.RegionStatusMaintenance {
		t.Errorf("RegionStatus=%q want %q", got.RegionStatus, model.RegionStatusMaintenance)
	}
	want := []string{"instances", "load-balancers"}
	if !reflect.DeepEqual(got.Capabilities, want) {
		t.Errorf("Capabilities=%v want %v", got.Capabilities, want)
	}
}

// region_status CHECK 约束应拒绝非法值。
func TestClusterRepo_RegionStatusCheckConstraint(t *testing.T) {
	db := testhelper.NewTestDB(t, "")
	ctx := context.Background()

	_, err := db.ExecContext(ctx,
		`INSERT INTO clusters (name, api_url, region_status)
		 VALUES ('r3', 'https://r3.test', 'bogus')`)
	if err == nil {
		t.Fatalf("expected CHECK constraint to reject region_status='bogus'")
	}
}
