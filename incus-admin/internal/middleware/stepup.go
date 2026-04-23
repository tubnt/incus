package middleware

import (
	"context"
	"encoding/json"
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
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/[^/]+/evacuate$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/clusters/[^/]+/nodes/[^/]+/restore$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/nodes/[^/]+/evacuate$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/nodes/[^/]+/restore$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/users/\d+/balance$`)},
}

func isSensitive(method, path string) bool {
	for _, s := range sensitiveRoutes {
		if s.method == method && s.path.MatchString(path) {
			return true
		}
	}
	return false
}

// RequireRecentAuthOnSensitive mounts once at the /api/admin router group and
// only enforces step-up on requests matching sensitiveRoutes. Non-sensitive
// admin operations pass straight through.
//
// If lookup is nil (step-up not configured at startup), the middleware
// becomes a no-op and logs nothing on each request — sensitive endpoints
// remain reachable. This keeps the server bootable before OIDC env vars are
// provisioned on new deployments.
func RequireRecentAuthOnSensitive(lookup StepUpLookup, maxAge time.Duration) func(http.Handler) http.Handler {
	if lookup == nil {
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
