package portal

import (
	"crypto/md5" //nolint:gosec // G501: SSH 公钥指纹（RFC 4716）以 MD5 展示，属协议约定值而非密码哈希
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

type SSHKeyHandler struct {
	repo *repository.SSHKeyRepo
}

func NewSSHKeyHandler(repo *repository.SSHKeyRepo) *SSHKeyHandler {
	return &SSHKeyHandler{repo: repo}
}

func (h *SSHKeyHandler) Routes(r chi.Router) {
	r.Get("/ssh-keys", h.List)
	r.Post("/ssh-keys", h.Create)
	r.Delete("/ssh-keys/{id}", h.Delete)
}

func (h *SSHKeyHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	keys, err := h.repo.ListByUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list keys"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (h *SSHKeyHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)

	var req struct {
		Name      string `json:"name"       validate:"omitempty,max=100"`
		PublicKey string `json:"public_key" validate:"required,min=1,max=16384"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}

	req.PublicKey = strings.TrimSpace(req.PublicKey)
	if req.PublicKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "public_key required"})
		return
	}

	if req.Name == "" {
		parts := strings.Fields(req.PublicKey)
		if len(parts) >= 3 {
			req.Name = parts[2]
		} else {
			req.Name = "key"
		}
	}

	fp, err := sshFingerprint(req.PublicKey)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid SSH public key"})
		return
	}

	key, err := h.repo.Create(r.Context(), userID, req.Name, req.PublicKey, fp)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "ssh_key.create", "ssh_key", key.ID, map[string]any{
		"name":        key.Name,
		"fingerprint": fp,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"key": key})
}

func (h *SSHKeyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}

	if err := h.repo.Delete(r.Context(), id, userID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "ssh_key.delete", "ssh_key", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func sshFingerprint(pubKey string) (string, error) {
	parts := strings.Fields(pubKey)
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid key format")
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	hash := md5.Sum(decoded) //nolint:gosec // G401: 同上 —— SSH 指纹展示用，非安全哈希用途
	fp := make([]string, len(hash))
	for i, b := range hash {
		fp[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(fp, ":"), nil
}
