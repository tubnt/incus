package portal

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// dangerousCloudInitTopKeys 列出禁止 admin / AI 写入的顶层字段。这些字段
// 一旦出现就会覆盖 BuildCloudInit 的系统强制行为（统一 root 登录 + sshd
// PermitRootLogin + openssh-server 必装）。OPS-051 / PLAN-052 §E。
//
// 设计取舍：黑名单 vs 白名单
//   - 黑名单覆盖窄、易理解、不限制 admin/AI 创造（packages / runcmd / write_files
//     / users 等"追加型"字段都不禁）
//   - 白名单则需要随 cloud-init 上游字段表演进，维护代价高
//
// 黑名单 = 任何"反向移除系统默认"的字段：
//   - disable_root: true             → 与 OPS-051 root 化冲突
//   - ssh_pwauth: false              → 破坏初始密码登录
//   - chpasswd 顶层 expire/list       → 干扰 BuildCloudInit 的 root 密码注入
//
// 注：users[] 顶层即使被覆盖，cloud-init 上游行为是合并而非替换；ExtraYAML
// 中追加 user 不会移除 root，所以不进黑名单。
var dangerousCloudInitTopKeys = map[string]string{
	"disable_root": "禁止：会覆盖 OPS-051 默认 root 登录策略",
	"ssh_pwauth":   "禁止：会覆盖密码登录开关（用户拿到的初始密码会失效）",
}

// validateCloudInitTemplate 校验 admin / AI 写入的 os_templates.cloud_init_template。
// 空字符串放行（默认行为 == 走 BuildCloudInit 系统模板）。
//
// OPS-051 / PLAN-052：扩展自 PLAN-051 §2-B 仅 yaml 解析校验 → 加 dangerous
// 顶层字段黑名单 + 首行 #cloud-config 提示（cloud-init datasource 要求）。
func validateCloudInitTemplate(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	for k := range doc {
		if reason, hit := dangerousCloudInitTopKeys[k]; hit {
			return fmt.Errorf("字段 %q 被禁用：%s", k, reason)
		}
	}
	return nil
}

type OSTemplateHandler struct {
	repo *repository.OSTemplateRepo
}

func NewOSTemplateHandler(repo *repository.OSTemplateRepo) *OSTemplateHandler {
	return &OSTemplateHandler{repo: repo}
}

func (h *OSTemplateHandler) PortalRoutes(r chi.Router) {
	r.Get("/os-templates", h.ListEnabled)
}

func (h *OSTemplateHandler) AdminRoutes(r chi.Router) {
	r.Get("/os-templates", h.ListAll)
	r.Get("/os-templates/{id}", h.GetByID)
	r.Post("/os-templates", h.Create)
	r.Put("/os-templates/{id}", h.Update)
	r.Delete("/os-templates/{id}", h.Delete)
}

func (h *OSTemplateHandler) ListEnabled(w http.ResponseWriter, r *http.Request) {
	templates, err := h.repo.ListEnabled(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list templates"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (h *OSTemplateHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	templates, err := h.repo.ListAll(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list templates"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (h *OSTemplateHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	t, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if t == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type createOSTemplateReq struct {
	Slug              string `json:"slug"                validate:"required,safename,max=64"`
	Name              string `json:"name"                validate:"required,min=1,max=128"`
	Source            string `json:"source"              validate:"required,min=1,max=256"`
	Protocol          string `json:"protocol"            validate:"omitempty,oneof=simplestreams incus"`
	ServerURL         string `json:"server_url"          validate:"omitempty,url,max=512"`
	DefaultUser       string `json:"default_user"        validate:"omitempty,max=64"`
	CloudInitTemplate string `json:"cloud_init_template" validate:"omitempty,max=8192"`
	SupportsRescue    bool   `json:"supports_rescue"`
	Enabled           bool   `json:"enabled"`
	SortOrder         int    `json:"sort_order"          validate:"gte=0,lte=100000"`
}

func (h *OSTemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createOSTemplateReq
	if !decodeAndValidate(w, r, &req) {
		return
	}
	// Session-1 O6：cloud-init 模板 YAML 解析校验
	if err := validateCloudInitTemplate(req.CloudInitTemplate); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cloud_init_template: " + err.Error()})
		return
	}
	t := model.OSTemplate{
		Slug:              req.Slug,
		Name:              req.Name,
		Source:            req.Source,
		Protocol:          req.Protocol,
		ServerURL:         req.ServerURL,
		DefaultUser:       req.DefaultUser,
		CloudInitTemplate: req.CloudInitTemplate,
		SupportsRescue:    req.SupportsRescue,
		Enabled:           req.Enabled,
		SortOrder:         req.SortOrder,
	}
	created, err := h.repo.Create(r.Context(), &t)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	audit(r.Context(), r, "os_template.create", "os_template", created.ID, map[string]any{
		"slug":    created.Slug,
		"name":    created.Name,
		"source":  created.Source,
		"enabled": created.Enabled,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"template": created})
}

type updateOSTemplateReq struct {
	Slug              *string `json:"slug"                validate:"omitempty,safename,max=64"`
	Name              *string `json:"name"                validate:"omitempty,min=1,max=128"`
	Source            *string `json:"source"              validate:"omitempty,min=1,max=256"`
	Protocol          *string `json:"protocol"            validate:"omitempty,oneof=simplestreams incus"`
	ServerURL         *string `json:"server_url"          validate:"omitempty,url,max=512"`
	DefaultUser       *string `json:"default_user"        validate:"omitempty,max=64"`
	CloudInitTemplate *string `json:"cloud_init_template" validate:"omitempty,max=8192"`
	SupportsRescue    *bool   `json:"supports_rescue"`
	Enabled           *bool   `json:"enabled"`
	SortOrder         *int    `json:"sort_order"          validate:"omitempty,gte=0,lte=100000"`
}

func applyOSTemplatePatch(t *model.OSTemplate, req updateOSTemplateReq) {
	if req.Slug != nil {
		t.Slug = *req.Slug
	}
	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.Source != nil {
		t.Source = *req.Source
	}
	if req.Protocol != nil {
		t.Protocol = *req.Protocol
	}
	if req.ServerURL != nil {
		t.ServerURL = *req.ServerURL
	}
	if req.DefaultUser != nil {
		t.DefaultUser = *req.DefaultUser
	}
	if req.CloudInitTemplate != nil {
		t.CloudInitTemplate = *req.CloudInitTemplate
	}
	if req.SupportsRescue != nil {
		t.SupportsRescue = *req.SupportsRescue
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.SortOrder != nil {
		t.SortOrder = *req.SortOrder
	}
}

func (h *OSTemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	existing, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}

	var req updateOSTemplateReq
	if !decodeAndValidate(w, r, &req) {
		return
	}
	// Session-1 O6：cloud-init 模板 YAML 解析校验（Update 路径同样防御）
	if req.CloudInitTemplate != nil {
		if err := validateCloudInitTemplate(*req.CloudInitTemplate); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cloud_init_template: " + err.Error()})
			return
		}
	}
	applyOSTemplatePatch(existing, req)
	existing.ID = id
	if err := h.repo.Update(r.Context(), existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	audit(r.Context(), r, "os_template.update", "os_template", id, map[string]any{
		"slug":    existing.Slug,
		"name":    existing.Name,
		"enabled": existing.Enabled,
	})
	writeJSON(w, http.StatusOK, map[string]any{"template": existing})
}

func (h *OSTemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	existing, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	if err := h.repo.Delete(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	audit(r.Context(), r, "os_template.delete", "os_template", id, map[string]any{
		"slug": existing.Slug,
		"name": existing.Name,
	})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}
