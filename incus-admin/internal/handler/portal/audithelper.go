package portal

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

var auditRepo *repository.AuditRepo
var userRepo *repository.UserRepo

func SetAuditRepo(repo *repository.AuditRepo) {
	auditRepo = repo
}

// SetUserRepo 注入 UserRepo 给 handler 内部辅助函数使用（订单失败回滚退款等）。
func SetUserRepo(repo *repository.UserRepo) {
	userRepo = repo
}

func audit(ctx context.Context, r *http.Request, action, targetType string, targetID int64, details any) {
	if auditRepo == nil {
		return
	}
	userID, _ := ctx.Value(middleware.CtxUserID).(int64)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
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
