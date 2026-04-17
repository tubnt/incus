package portal

import (
	"testing"

	"github.com/incuscloud/incus-admin/internal/model"
)

func ptr[T any](v T) *T { return &v }

func TestApplyUpdateProductReq_PriceOnly(t *testing.T) {
	existing := model.Product{
		ID: 7, Name: "Basic", Slug: "basic", CPU: 2, MemoryMB: 4096, DiskGB: 40,
		PriceMonthly: 10.0, Currency: "USD", Active: true, SortOrder: 3,
	}
	req := UpdateProductReq{PriceMonthly: ptr(15.5)}
	applyUpdateProductReq(&existing, req)

	if existing.PriceMonthly != 15.5 {
		t.Errorf("PriceMonthly = %v, want 15.5", existing.PriceMonthly)
	}
	if existing.Name != "Basic" || existing.CPU != 2 || existing.MemoryMB != 4096 {
		t.Errorf("unset fields mutated: %+v", existing)
	}
	if !existing.Active || existing.SortOrder != 3 {
		t.Errorf("Active/SortOrder were clobbered: active=%v sort=%d", existing.Active, existing.SortOrder)
	}
}

func TestApplyUpdateProductReq_ExplicitZero(t *testing.T) {
	existing := model.Product{ID: 1, CPU: 4, SortOrder: 9, Active: true}
	req := UpdateProductReq{CPU: ptr(0), Active: ptr(false)}
	applyUpdateProductReq(&existing, req)
	if existing.CPU != 0 {
		t.Errorf("explicit zero CPU not applied: %d", existing.CPU)
	}
	if existing.Active {
		t.Errorf("explicit false Active not applied")
	}
	if existing.SortOrder != 9 {
		t.Errorf("SortOrder clobbered to %d, want 9", existing.SortOrder)
	}
}

func TestApplyUpdateProductReq_MultipleFields(t *testing.T) {
	existing := model.Product{ID: 1, Name: "old", CPU: 1, MemoryMB: 1024}
	req := UpdateProductReq{Name: ptr("new"), MemoryMB: ptr(2048)}
	applyUpdateProductReq(&existing, req)
	if existing.Name != "new" {
		t.Errorf("Name = %q, want new", existing.Name)
	}
	if existing.MemoryMB != 2048 {
		t.Errorf("MemoryMB = %d, want 2048", existing.MemoryMB)
	}
	if existing.CPU != 1 {
		t.Errorf("CPU clobbered to %d, want 1", existing.CPU)
	}
}
