package portal

import (
	"context"
	"net/http"
	"time"

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
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		auditRepo.Log(bgCtx, uid, action, targetType, targetID, details, ip)
	}()
}
