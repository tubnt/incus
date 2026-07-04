package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// StepUpLookup returns the user's last step-up auth completion time, or nil
// if the user has never completed a step-up re-authentication.
type StepUpLookup func(ctx context.Context, userID int64) (*time.Time, error)

// sensitiveRoute matches a single admin endpoint that must be step-up gated.
// The request's full URL path (including the /api/admin prefix) is matched
// against the regex; method is compared exactly.
type sensitiveRoute struct {
	method string
	path   *regexp.Regexp
}

// sensitiveRoutes enumerates the admin operations that must be re-auth gated
// before the handler runs. Updating this list is the single source of truth
// for step-up coverage — keep it aligned with PLAN-019 scope.
//
// Path IDs: VMs use names (not numeric ids) — e.g. /vms/vm-aa6862. Node
// evacuate/restore is registered under *both* a cluster-scoped path (used by
// the current frontend) and a legacy top-level path (clustermgmt.go still
// registers it); we cover both so a frontend rollback doesn't accidentally
// bypass step-up.
var sensitiveRoutes = []sensitiveRoute{
	{method: http.MethodDelete, path: regexp.MustCompile(`^/api/admin/vms/[^/]+$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/vms/[^/]+/migrate$`)},
	// PLAN-023: batch operations are gated wholesale (step-up is per-session,
	// not per-action; even start/stop batch requires recent reauth as it's
	// admin-only and high blast radius).
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/vms:batch$`)},
	// UX-007 / PLAN-051 follow-up: portal 用户重看初始密码（创建时生成的 root 密码）。
	// vms.password 解密后明文外发，按 OWASP 高敏分类强制 step-up；audit 全记。
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/portal/services/\d+/initial-credentials$`)},
	// PLAN-055 / OPS-052 §1：portal 支付接口。扣款前强制 step-up（本中间件已上提到
	// portal+admin 公共 Group，故 /api/portal 也生效）；同时受 shadow 拒绝保护。
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/portal/orders/\d+/pay$`)},
	// PLAN-055 / OPS-052 §2：单条改用户角色（提权 / 降权）是高敏动作，强制 step-up。
	// 批量改角色 /api/admin/users:batch 已在下方覆盖；此处补单条 PUT。
	{method: http.MethodPut, path: regexp.MustCompile(`^/api/admin/users/\d+/role$`)},
	// PLAN-037: 批量冷迁移；destructive（停机迁移）+ 高 blast radius
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/vms:migrate-batch$`)},
	// PLAN-039 / OPS-043: enable-stateful 涉及重启 VM（用户感知停机）
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/vms/[^/]+/enable-stateful$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/vms:enable-stateful-batch$`)},
	// PLAN-039 / OPS-044: dismiss alert（admin-only写）
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/system-alerts/\d+/dismiss$`)},
	// PLAN-038 / OPS-041: AI 调用付费 + 拉取系统数据，按高敏处理
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/ai-suggest$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/jobs/\d+/ai-diagnose$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/floating-ips:batch$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/users:batch$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/[^/]+/evacuate$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/[^/]+/restore$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/nodes/[^/]+/evacuate$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/nodes/[^/]+/restore$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/users/\d+/balance$`)},
	// PLAN-026 / INFRA-002 节点 add/remove —— 物理 SSH 编排，step-up 必须
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes$`)},
	{method: http.MethodDelete, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/[^/]+$`)},
	// OPS-024 D2 maintenance + C2 env-script 暴露集群拓扑，step-up 必须
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/[^/]+/maintenance$`)},
	{method: http.MethodGet, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/env-script$`)},
	// PLAN-033 / OPS-039：SSH 凭据 CRUD + 节点探测（含密码 / inline private key），全部高敏
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/node-credentials$`)},
	{method: http.MethodDelete, path: regexp.MustCompile(`^/api/admin/node-credentials/\d+$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/probe$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/probe-host-key$`)},
	// PLAN-041 / INFRA-009：通道 CRUD + 测试发送 + 告警规则 CRUD（含 webhook secret /
	// 钉钉签名 / SMTP 密码等敏感凭据）。删除规则 / 删通道 / 测试发送都要 step-up。
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/notify-channels$`)},
	{method: http.MethodPut, path: regexp.MustCompile(`^/api/admin/notify-channels/\d+$`)},
	{method: http.MethodDelete, path: regexp.MustCompile(`^/api/admin/notify-channels/\d+$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/notify-channels/\d+/test$`)},
	// CR P1 #11 修复：alert-rules POST/PUT 也加 step-up（改阈值 / 改 channel_ids
	// 是高敏动作，影响告警发不发出去；PATCH /enabled 启停 toggle 不加，运维高频）。
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/alert-rules$`)},
	{method: http.MethodPut, path: regexp.MustCompile(`^/api/admin/alert-rules/\d+$`)},
	{method: http.MethodDelete, path: regexp.MustCompile(`^/api/admin/alert-rules/\d+$`)},
}

func isSensitive(method, path string) bool {
	for _, s := range sensitiveRoutes {
		if s.method == method && s.path.MatchString(path) {
			return true
		}
	}
	return false
}

// RequireRecentAuthOnSensitive mounts once at the portal+admin 公共 router group
// and only enforces step-up on requests matching sensitiveRoutes. Non-sensitive
// operations pass straight through.
//
// lookup == nil 表示 step-up 未就绪，此时行为由 failClosed 决定：
//
//   - failClosed == false：OIDC 根本没配置（新部署尚未 provisioning env）。
//     中间件降级为 no-op，敏感端点可达，保证 server 可启动。
//   - failClosed == true：OIDC 已配置但 discovery 失败（PLAN-055 / OPS-052 §5）。
//     此时不再 fail-open 静默放行，而是 fail-closed —— 所有敏感操作直接 503 拒绝，
//     避免 step-up 保护因 IdP discovery 短暂故障被绕过。运维修复 OIDC 后重启即恢复。
func RequireRecentAuthOnSensitive(lookup StepUpLookup, maxAge time.Duration, failClosed bool) func(http.Handler) http.Handler {
	if lookup == nil {
		if failClosed {
			return func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !isSensitive(r.Method, r.URL.Path) {
						next.ServeHTTP(w, r)
						return
					}
					slog.Error("step-up unavailable (OIDC discovery failed at startup); rejecting sensitive operation (fail-closed)",
						"method", r.Method, "path", r.URL.Path)
					writeStepUpUnavailable(w)
				})
			}
		}
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isSensitive(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Under a shadow session, the step-up check must run against the
			// admin's (actor) step-up timestamp, not the target user's. The
			// target never goes through the OIDC re-auth flow.
			userID, _ := r.Context().Value(CtxUserID).(int64)
			actorID, _ := r.Context().Value(CtxActorID).(int64)
			checkID := userID
			if actorID > 0 {
				checkID = actorID
			}
			if checkID == 0 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			at, err := lookup(r.Context(), checkID)
			if err != nil {
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
				return
			}

			if at == nil || time.Since(*at) > maxAge {
				writeStepUpRequired(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeStepUpRequired emits the standard 401 response the frontend interceptor
// listens for. The redirect URL is relative so oauth2-proxy forwards the user
// through its normal session check when the browser follows the redirect.
func writeStepUpRequired(w http.ResponseWriter, r *http.Request) {
	q := url.Values{}
	// Preserve the full original path+query so the flow returns to exactly
	// the same admin page after re-auth.
	rd := r.URL.RequestURI()
	q.Set("rd", rd)
	redirect := "/api/auth/stepup/start?" + q.Encode()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":    "step_up_required",
		"redirect": redirect,
	})
}

// writeStepUpUnavailable 在 fail-closed 模式下（OIDC 已配置但 discovery 失败）
// 拒绝敏感操作。返回 503 而非 401：这不是"你需要重新认证"，而是"服务端 step-up
// 子系统当前不可用"，前端不应据此发起 step-up 重定向（那样只会再失败一次）。
func writeStepUpUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":   "step_up_unavailable",
		"message": "Step-up authentication is temporarily unavailable; sensitive operations are blocked. Contact an operator.",
	})
}
