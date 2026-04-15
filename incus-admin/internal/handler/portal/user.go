package portal

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/repository"
)

type UserHandler struct {
	repo *repository.UserRepo
}

func NewUserHandler(repo *repository.UserRepo) *UserHandler {
	return &UserHandler{repo: repo}
}

func (h *UserHandler) AdminRoutes(r chi.Router) {
	r.Get("/users", h.ListUsers)
	r.Get("/users/{id}", h.GetUser)
	r.Put("/users/{id}/role", h.UpdateRole)
	r.Post("/users/{id}/balance", h.TopUpBalance)
}

func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.repo.ListAll(r.Context())
	if err != nil {
		slog.Error("list users failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list users"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	user, err := h.repo.GetByID(r.Context(), id)
	if err != nil || user == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *UserHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.Role != "admin" && req.Role != "customer" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "role must be admin or customer"})
		return
	}
	if err := h.repo.UpdateRole(r.Context(), id, req.Role); err != nil {
		slog.Error("update role failed", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to update role"})
		return
	}
	slog.Info("user role updated", "user_id", id, "role", req.Role)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *UserHandler) TopUpBalance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Amount      float64 `json:"amount"`
		Description string  `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "amount must be positive"})
		return
	}
	if err := h.repo.AdjustBalance(r.Context(), id, req.Amount, "deposit", req.Description, nil); err != nil {
		slog.Error("top up balance failed", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	slog.Info("balance topped up", "user_id", id, "amount", req.Amount)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
