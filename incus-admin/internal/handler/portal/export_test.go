package portal

import (
	"context"
	"net/http"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// RollbackPaymentForTest exposes the unexported rollbackPayment compensation chain
// so the integration test can drive it without a full cluster.Manager + VMService stack.
func RollbackPaymentForTest(h *OrderHandler, ctx context.Context, order *model.Order, ip, reason string) {
	h.rollbackPayment(ctx, order, ip, reason)
}

// CreateSubscriptionForOrderForTest drives the unexported subscription hook
// directly so PLAN-054 integration tests can verify Pay-time sub write without
// building the full cluster + jobs runtime stack.
func CreateSubscriptionForOrderForTest(h *OrderHandler, ctx context.Context, r *http.Request, order *model.Order, product *model.Product, vmID int64) error {
	return h.createSubscriptionForOrder(ctx, r, order, product, vmID)
}

// CancelSubscriptionOnTrashForTest / ReactivateSubscriptionOnRestoreForTest 暴
// 露 trash/restore hook 给集成测试。subRepo 直接传入，避免 handler 嵌套。
func CancelSubscriptionOnTrashForTest(ctx context.Context, r *http.Request, subs *repository.SubscriptionRepo, vmID int64) {
	cancelSubscriptionOnTrash(ctx, r, subs, vmID)
}

func ReactivateSubscriptionOnRestoreForTest(ctx context.Context, r *http.Request, subs *repository.SubscriptionRepo, vmID int64) {
	reactivateSubscriptionOnRestore(ctx, r, subs, vmID)
}
