package portal

import (
	"context"
	"net/http"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

var auditRepo *repository.AuditRepo

func SetAuditRepo(repo *repository.AuditRepo) {
	auditRepo = repo
}

func audit(ctx context.Context, r *http.Request, action, targetType string, targetID int64, details any) {
	if auditRepo == nil {
		return
	}
	userID, _ := ctx.Value(middleware.CtxUserID).(int64)
	ip := r.RemoteAddr
	var uid *int64
	if userID > 0 {
		uid = &userID
	}
	go auditRepo.Log(ctx, uid, action, targetType, targetID, details, ip)
}
