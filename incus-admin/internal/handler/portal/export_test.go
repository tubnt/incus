package portal

import (
	"context"

	"github.com/incuscloud/incus-admin/internal/model"
)

// RollbackPaymentForTest exposes the unexported rollbackPayment compensation chain
// so the integration test can drive it without a full cluster.Manager + VMService stack.
func RollbackPaymentForTest(h *OrderHandler, ctx context.Context, order *model.Order, ip, reason string) {
	h.rollbackPayment(ctx, order, ip, reason)
}
