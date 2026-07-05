package portal

import (
	"testing"

	"github.com/incuscloud/incus-admin/internal/model"
)

// TestBillingPeriodDuration 锁定 daily/monthly 的周期长度（24h / 30d）。
// L3-F billing worker + restore 重置 paid_until 都依赖此函数，被改动应直接
// 触发回归。
func TestBillingPeriodDuration(t *testing.T) {
	if got := model.BillingPeriodDuration(model.BillingPeriodDaily); got.Hours() != 24 {
		t.Errorf("daily duration = %v hours, want 24", got.Hours())
	}
	if got := model.BillingPeriodDuration(model.BillingPeriodMonthly); got.Hours() != 24*30 {
		t.Errorf("monthly duration = %v hours, want 720", got.Hours())
	}
	if got := model.BillingPeriodDuration("invalid"); got != 0 {
		t.Errorf("invalid period duration = %v, want 0", got)
	}
}
