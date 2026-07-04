package v1

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/handler/portal"
	"github.com/incuscloud/incus-admin/internal/model"
)

// 安全上限：避免 cloud-gateway 客户端发超大 payload 把内存打满。
// 与 portal middleware decodeAndValidate 的 1 MiB 取齐。
const maxV1InstanceBodyBytes = 1 << 20

// createInstanceRequest 是 POST /v1/instances 的标准化输入。type / ssh_keys
// 兼容 cloud-gateway 客户端把 numeric id 当字符串发；用 json.RawMessage 让
// handler 自行解析（支持 string 与 number 两种形式）。
//
// label 是 cloud-gateway 词汇里的 VM 显示名（→ portal model.VM.Name）。
// period 可空，缺省 monthly。
type createInstanceRequest struct {
	Region   string          `json:"region"`
	Type     json.RawMessage `json:"type"`
	Image    string          `json:"image"`
	Label    string          `json:"label"`
	RootPass string          `json:"root_pass"`
	SSHKeys  json.RawMessage `json:"ssh_keys"`
	Tags     []string        `json:"tags"`
	UserData string          `json:"user_data"`
	Period   string          `json:"period"`
}

// createInstanceResponse 与 InstanceDTO 同 shape，便于 client 复用解析逻辑。
// 多带 order_id / job_id 是 cloud-gateway 标准扩展（status=pending 时方便客户端
// 后续轮询）。
type createInstanceResponse struct {
	InstanceDTO
	OrderID int64 `json:"order_id"`
	JobID   int64 `json:"job_id"`
}

// CreateInstance POST /v1/instances 实现 cloud-gateway 一步购买。
//
// 校验顺序（与 PLAN-053 §1 一致；任一失败立即 422）：
//  1. region → ClusterRepo.GetByName + region_status='available'
//  2. type → ProductRepo（numeric id 走 GetByID；字符串 slug 走 ListActive 内存查）
//  3. period 在 product.period_supported（缺省 monthly）
//  4. image → OSTemplateRepo.GetBySlug + enabled
//  5. ssh_keys → 所有 id ∈ user 自己的 keys
//
// 业务流程委托给 portal.OrderHandler.CreatePayProvision；返 201 + Location +
// body 是 InstanceDTO + order_id + job_id（status=pending）。
func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromCtx(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "", "unauthorized")
		return
	}
	if h.deps.OrderProvision == nil || h.deps.ClustersByName == nil ||
		h.deps.OSTemplatesBySlug == nil || h.deps.ProductsBySlug == nil ||
		h.deps.Products == nil || h.deps.SSHKeysOwner == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}

	req, perr := decodeCreateInstanceRequest(r)
	if perr != nil {
		writeFieldErr(w, http.StatusUnprocessableEntity, perr.field, perr.reason)
		return
	}

	// label 合法性 —— 与 portal safename 规则同：只接受 [A-Za-z0-9-]，1..63 字符。
	// 空值由后端走 GenerateVMName 兜底，与 portal 行为对齐。
	if req.Label != "" {
		if rerr := validateInstanceLabel(req.Label); rerr != "" {
			writeFieldErr(w, http.StatusUnprocessableEntity, "label", rerr)
			return
		}
	}

	// 1. region → cluster 行（必须 region_status='available'）
	// 不存在 / 非 available 都返同一 reason=region_unavailable，与 PLAN-053 §1 一致
	// （也避免给 cloud-gateway 客户端泄露 region 列表存在性）。
	cluster, err := h.deps.ClustersByName.GetByName(r.Context(), req.Region)
	if err != nil {
		slog.Error("v1 CreateInstance: clusters.GetByName", "region", req.Region, "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	if cluster == nil || cluster.RegionStatus != model.RegionStatusAvailable {
		writeFieldErr(w, http.StatusUnprocessableEntity, "region", "region_unavailable")
		return
	}

	// 2. type → product
	product, perrField := h.resolveProduct(r, req.Type)
	if perrField != "" {
		writeFieldErr(w, http.StatusUnprocessableEntity, "type", perrField)
		return
	}

	// 3. period 在 product.period_supported
	period := req.Period
	if period == "" {
		period = model.BillingPeriodMonthly
	}
	if !containsString(product.PeriodSupported, period) {
		writeFieldErr(w, http.StatusUnprocessableEntity, "period", "unsupported_period")
		return
	}

	// 4. image → os template（slug 必须 enabled）
	template, err := h.deps.OSTemplatesBySlug.GetBySlug(r.Context(), req.Image)
	if err != nil {
		slog.Error("v1 CreateInstance: ostemplates.GetBySlug", "image", req.Image, "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	if template == nil || !template.Enabled {
		writeFieldErr(w, http.StatusUnprocessableEntity, "image", "image_not_found")
		return
	}

	// 5. ssh_keys 越权校验
	sshKeyStrings, sshErr := h.resolveSSHKeys(r, uid, req.SSHKeys)
	if sshErr != "" {
		writeFieldErr(w, http.StatusUnprocessableEntity, "ssh_keys", sshErr)
		return
	}

	// 6. root_pass 长度校验（与 openapi minLength: 8 一致；空 → 服务端随机生成）。
	if req.RootPass != "" && len(req.RootPass) < 8 {
		writeFieldErr(w, http.StatusUnprocessableEntity, "root_pass", "invalid_root_pass")
		return
	}

	// 委托订单流（一步购买）
	result, provErr := h.deps.OrderProvision.CreatePayProvision(r, portal.V1ProvisionRequest{
		UserID:      uid,
		ProductID:   product.ID,
		ClusterID:   cluster.ID,
		ClusterName: cluster.Name,
		OSImage:     template.Source,
		VMName:      req.Label,
		Period:      period,
		SSHKeys:     sshKeyStrings,
		// WP-I1：真正透传 root_pass / user_data / tags（不再声明支持却静默丢弃）。
		RootPass: req.RootPass,
		UserData: req.UserData,
		Tags:     req.Tags,
	})
	if provErr != nil {
		writeProvisionErr(w, r, provErr)
		return
	}

	// 同步返 201：body 是 InstanceDTO（status=pending）+ Location header。
	// tags 回显请求值（openapi 要求 tags 必填，nil → 空数组）。
	respTags := req.Tags
	if respTags == nil {
		respTags = []string{}
	}
	dto := InstanceDTO{
		ID:        result.VMID,
		Label:     result.VMName,
		Type:      product.ID,
		Region:    cluster.Name,
		Image:     template.Source,
		Status:    "pending",
		IP4:       result.IP,
		IP6:       "",
		CreatedAt: nowUTC(),
		Tags:      respTags,
	}
	resp := createInstanceResponse{
		InstanceDTO: dto,
		OrderID:     result.OrderID,
		JobID:       result.JobID,
	}
	w.Header().Set("Location", "/v1/instances/"+strconv.FormatInt(result.VMID, 10))
	writeJSON(w, http.StatusCreated, resp)
}

// DeleteInstance DELETE /v1/instances/{id} 走 trash + 30s undo 路径（PLAN-053 §3 决策）。
// 返 202 + {status:"deleting"} 让 cloud-gateway 客户端轮询 GET /v1/instances/{id}
// 看到 trashed_at 标记。
func (h *Handler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromCtx(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "", "unauthorized")
		return
	}
	if h.deps.VMTrash == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}
	id, parseErr := parseInstanceID(chi.URLParam(r, "id"))
	if parseErr {
		writeErr(w, http.StatusNotFound, "", "not_found")
		return
	}
	if pErr := h.deps.VMTrash.V1TrashByID(r, uid, id); pErr != nil {
		writeProvisionErr(w, r, pErr)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":     id,
		"status": "deleting",
	})
}

// ActionInstance POST /v1/instances/{id}/{action} 实现 boot / reboot / shutdown。
// 路由层已 by-path 收敛 action 三选一，但服务方法仍二次校验防止误传。
func (h *Handler) ActionInstance(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := userIDFromCtx(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "", "unauthorized")
			return
		}
		if h.deps.VMAction == nil {
			internalIfMissingDeps(w, r.URL.Path)
			return
		}
		id, parseErr := parseInstanceID(chi.URLParam(r, "id"))
		if parseErr {
			writeErr(w, http.StatusNotFound, "", "not_found")
			return
		}
		if pErr := h.deps.VMAction.V1ActionByID(r, uid, id, action); pErr != nil {
			writeProvisionErr(w, r, pErr)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"id":     id,
			"status": action + "ing",
		})
	}
}

// resolveProduct 把 type 字段解析到 Product。支持 JSON number / 数字字符串 /
// slug 三种形态；非法或不存在 / 非 active 返字段级错误 reason。
func (h *Handler) resolveProduct(r *http.Request, typeRaw json.RawMessage) (*model.Product, string) {
	if len(typeRaw) == 0 || string(typeRaw) == "null" {
		return nil, "required"
	}
	raw := strings.TrimSpace(string(typeRaw))
	// 优先按数字解析。
	if idVal, err := parseJSONNumberOrString(typeRaw); err == nil && idVal > 0 {
		p, perr := h.deps.Products.GetByID(r.Context(), idVal)
		if perr != nil {
			slog.Error("v1 CreateInstance: products.GetByID", "id", idVal, "error", perr)
			return nil, "type_not_found"
		}
		if p == nil || !p.Active {
			return nil, "type_not_found"
		}
		return p, ""
	}
	// 字符串 slug：走 ListActive 内存匹配（products N<50）。
	if raw == "" || raw == `""` {
		return nil, "required"
	}
	slug := strings.Trim(raw, `"`)
	products, err := h.deps.ProductsBySlug.ListActive(r.Context())
	if err != nil {
		slog.Error("v1 CreateInstance: products.ListActive", "error", err)
		return nil, "type_not_found"
	}
	for i := range products {
		if products[i].Slug == slug {
			return &products[i], ""
		}
	}
	return nil, "type_not_found"
}

// resolveSSHKeys 把 ssh_keys 数组解析到 []public_key 字符串；要求每个 id 都
// 属于当前 user，否则返 "ssh_key_not_found"。
// 接受形态：[1, 2, 3] / ["1","2","3"]（数字字符串）。
func (h *Handler) resolveSSHKeys(r *http.Request, userID int64, raw json.RawMessage) ([]string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{}, ""
	}
	var ids []int64
	// 1) try [int64]
	var nums []json.Number
	if err := json.Unmarshal(raw, &nums); err == nil {
		for _, n := range nums {
			v, perr := n.Int64()
			if perr != nil || v <= 0 {
				return nil, "ssh_key_invalid"
			}
			ids = append(ids, v)
		}
	} else {
		// 2) try [string] then ParseInt each
		var strs []string
		if serr := json.Unmarshal(raw, &strs); serr != nil {
			return nil, "ssh_key_invalid"
		}
		for _, s := range strs {
			v, perr := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if perr != nil || v <= 0 {
				return nil, "ssh_key_invalid"
			}
			ids = append(ids, v)
		}
	}
	if len(ids) == 0 {
		return []string{}, ""
	}

	// 拉用户所有 key，集合校验（避免对每个 id 单独查询；用户 key 通常 < 10）。
	keys, err := h.deps.SSHKeysOwner.ListByUser(r.Context(), userID)
	if err != nil {
		slog.Error("v1 CreateInstance: sshkeys.ListByUser", "user_id", userID, "error", err)
		return nil, "ssh_key_not_found"
	}
	have := make(map[int64]string, len(keys))
	for _, k := range keys {
		have[k.ID] = k.PublicKey
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		pk, found := have[id]
		if !found || pk == "" {
			return nil, "ssh_key_not_found"
		}
		out = append(out, pk)
	}
	return out, ""
}

// fieldDecodeErr 把 decode 过程的字段级失败折出来，供 handler 直接转 422。
type fieldDecodeErr struct {
	field  string
	reason string
}

func (e *fieldDecodeErr) Error() string { return e.field + ":" + e.reason }

// decodeCreateInstanceRequest 解析 + 必填字段校验。region / image 必填；
// type 必填（具体解析在 resolveProduct）。
func decodeCreateInstanceRequest(r *http.Request) (*createInstanceRequest, *fieldDecodeErr) {
	limited := http.MaxBytesReader(nil, r.Body, maxV1InstanceBodyBytes)
	defer limited.Close()
	var req createInstanceRequest
	dec := json.NewDecoder(limited)
	if err := dec.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, &fieldDecodeErr{field: "", reason: "empty_body"}
		}
		return nil, &fieldDecodeErr{field: "", reason: "invalid_json"}
	}
	if strings.TrimSpace(req.Region) == "" {
		return nil, &fieldDecodeErr{field: "region", reason: "required"}
	}
	if len(req.Type) == 0 {
		return nil, &fieldDecodeErr{field: "type", reason: "required"}
	}
	if strings.TrimSpace(req.Image) == "" {
		return nil, &fieldDecodeErr{field: "image", reason: "required"}
	}
	return &req, nil
}

// validateInstanceLabel 与 portal safename 等价：1..63 字符 [a-zA-Z0-9-]，
// 不能以 '-' 开头。返回非空字符串即错误 reason。
func validateInstanceLabel(label string) string {
	if l := len(label); l < 1 || l > 63 {
		return "invalid_label"
	}
	if label[0] == '-' {
		return "invalid_label"
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		if !isAlpha && !isDigit && c != '-' {
			return "invalid_label"
		}
	}
	return ""
}

// parseInstanceID 解析 {id} URL 参数；非整数 / 非正 → not_found（与 read-only InstanceByID 同口径）。
func parseInstanceID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, true
	}
	return id, false
}

// parseJSONNumberOrString 把 json.RawMessage 解析为 int64（接受 JSON number 与
// 数字字符串两种形态）。任何失败返 (0, err) 让 caller 退化到 slug 路径。
func parseJSONNumberOrString(raw json.RawMessage) (int64, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, errors.New("empty")
	}
	// 数字字符串：去掉引号。
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		inner := strings.Trim(s, `"`)
		if inner == "" {
			return 0, errors.New("empty string")
		}
		return strconv.ParseInt(inner, 10, 64)
	}
	return strconv.ParseInt(s, 10, 64)
}

// containsString 是 slices.Contains 的 string 特化简写；保持包内零依赖 slices。
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// writeFieldErr 单字段级 422 错误。
func writeFieldErr(w http.ResponseWriter, status int, field, reason string) {
	writeErr(w, status, field, reason)
}

// writeProvisionErr 把 portal.V1ProvisionError 折成 StructuredError 响应。
func writeProvisionErr(w http.ResponseWriter, r *http.Request, e *portal.V1ProvisionError) {
	if e == nil {
		return
	}
	if e.Msg != "" {
		slog.Warn("v1 provision error", "status", e.Status, "reason", e.Reason, "field", e.Field, "msg", e.Msg, "path", r.URL.Path)
	}
	writeErr(w, e.Status, e.Field, e.Reason)
}

// nowUTC 单点封装时间获取，方便测试替换。
var nowUTC = func() time.Time { return time.Now().UTC() }
