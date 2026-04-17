package portal

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// 工单状态与优先级枚举（防止前端/第三方传入非法值）。
var (
	validTicketStatuses = map[string]bool{
		"open": true, "pending": true, "closed": true,
	}
	validTicketPriorities = map[string]bool{
		"low": true, "normal": true, "high": true, "urgent": true,
	}
)

type TicketHandler struct {
	repo *repository.TicketRepo
}

func NewTicketHandler(repo *repository.TicketRepo) *TicketHandler {
	return &TicketHandler{repo: repo}
}

func (h *TicketHandler) PortalRoutes(r chi.Router) {
	r.Get("/tickets", h.ListMine)
	r.Post("/tickets", h.Create)
	r.Get("/tickets/{id}", h.GetDetail)
	r.Post("/tickets/{id}/messages", h.Reply)
}

func (h *TicketHandler) AdminRoutes(r chi.Router) {
	r.Get("/tickets", h.ListAll)
	r.Get("/tickets/{id}", h.GetDetail)
	r.Post("/tickets/{id}/messages", h.AdminReply)
	r.Put("/tickets/{id}/status", h.UpdateStatus)
}

func (h *TicketHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	tickets, err := h.repo.ListByUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list tickets"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (h *TicketHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	p := ParsePageParams(r)
	tickets, total, err := h.repo.ListPaged(r.Context(), p.Limit, p.Offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list tickets"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tickets": tickets,
		"total":   total,
		"limit":   p.Limit,
		"offset":  p.Offset,
	})
}

func (h *TicketHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)

	var req struct {
		Subject  string `json:"subject"`
		Body     string `json:"body"`
		Priority string `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.Subject == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "subject required"})
		return
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}
	if !validTicketPriorities[req.Priority] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid priority"})
		return
	}

	ticket, err := h.repo.Create(r.Context(), userID, req.Subject, req.Priority)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if req.Body != "" {
		h.repo.AddMessage(r.Context(), ticket.ID, userID, req.Body, false)
	}

	writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket})
}

func (h *TicketHandler) GetDetail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	ticket, err := h.repo.GetByID(r.Context(), id)
	if err != nil || ticket == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket not found"})
		return
	}

	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	role, _ := r.Context().Value(middleware.CtxUserRole).(string)
	if role != "admin" && ticket.UserID != userID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "access denied"})
		return
	}

	messages, _ := h.repo.ListMessages(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "messages": messages})
}

func (h *TicketHandler) Reply(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	ticket, err := h.repo.GetByID(r.Context(), ticketID)
	if err != nil || ticket == nil || ticket.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket not found"})
		return
	}
	if ticket.Status == "closed" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "ticket is closed"})
		return
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body required"})
		return
	}

	msg, err := h.repo.AddMessage(r.Context(), ticketID, userID, req.Body, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

func (h *TicketHandler) AdminReply(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body required"})
		return
	}

	msg, err := h.repo.AddMessage(r.Context(), ticketID, userID, req.Body, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

func (h *TicketHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status required"})
		return
	}
	if !validTicketStatuses[req.Status] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid status"})
		return
	}

	if err := h.repo.UpdateStatus(r.Context(), ticketID, req.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": req.Status})
}
