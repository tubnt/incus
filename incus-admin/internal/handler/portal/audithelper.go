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
var healingRepo *repository.HealingEventRepo

// appEnv gates destructive-but-recoverable ops (chaos drill). Defaults to
// "production" so the safe path is the default; staging/dev must opt in.
var appEnv = "production"

// SetAppEnv wires the deployment env marker. Call from main after config load.
func SetAppEnv(env string) {
	if env != "" {
		appEnv = env
	}
}

func SetAuditRepo(repo *repository.AuditRepo) {
	auditRepo = repo
}

// SetUserRepo 注入 UserRepo 给 handler 内部辅助函数使用（订单失败回滚退款等）。
func SetUserRepo(repo *repository.UserRepo) {
	userRepo = repo
}

// SetHealingRepo 注入 HealingEventRepo 供节点 evacuate handler 双写 PLAN-020
// healing_events 表。nil 时 evacuate 仍正常工作，只是不记 healing 事件。
func SetHealingRepo(repo *repository.HealingEventRepo) {
	healingRepo = repo
}

func audit(ctx context.Context, r *http.Request, action, targetType string, targetID int64, details any) {
	if auditRepo == nil {
		return
	}
	userID, _ := ctx.Value(middleware.CtxUserID).(int64)
	actorID, _ := ctx.Value(middleware.CtxActorID).(int64)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}

	// When the request runs under a shadow session, record the acting admin
	// (actorID) as the audit row's user_id so "who did it" is unambiguous;
	// the target user is added to details as acting_as_user_id for context.
	effectiveUserID := userID
	if actorID > 0 {
		effectiveUserID = actorID
		details = mergeActingAs(details, userID)
	}

	var uid *int64
	if effectiveUserID > 0 {
		uid = &effectiveUserID
	}
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		auditRepo.Log(bgCtx, uid, action, targetType, targetID, details, ip)
	}()
}

// mergeActingAs decorates audit details with the shadowed target user id so a
// reader can see both the operator (user_id) and whose account was touched.
// Wraps non-map details to preserve the original payload shape.
func mergeActingAs(details any, targetUserID int64) any {
	if details == nil {
		return map[string]any{"acting_as_user_id": targetUserID}
	}
	if m, ok := details.(map[string]any); ok {
		m["acting_as_user_id"] = targetUserID
		return m
	}
	return map[string]any{
		"acting_as_user_id": targetUserID,
		"details":           details,
	}
}
