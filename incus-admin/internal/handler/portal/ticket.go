package portal

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// 工单状态/优先级枚举以 validator 的 oneof 约束表达，定义在各 handler 的 req 结构体上。
// 这两个常量曾作为手动校验的查找表，validator 迁移后不再使用。

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
	r.Post("/tickets/{id}/close", h.CloseMine)
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
		Subject  string `json:"subject"  validate:"required,min=1,max=200"`
		Body     string `json:"body"     validate:"omitempty,max=10000"`
		Priority string `json:"priority" validate:"omitempty,oneof=low normal high urgent"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}

	ticket, err := h.repo.Create(r.Context(), userID, req.Subject, req.Priority)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if req.Body != "" {
		_, _ = h.repo.AddMessage(r.Context(), ticket.ID, userID, req.Body, false)
	}

	audit(r.Context(), r, "ticket.create", "ticket", ticket.ID, map[string]any{
		"subject":  ticket.Subject,
		"priority": ticket.Priority,
	})
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
		Body string `json:"body" validate:"required,min=1,max=10000"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}

	msg, err := h.repo.AddMessage(r.Context(), ticketID, userID, req.Body, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "ticket.reply", "ticket", ticketID, map[string]any{
		"message_id": msg.ID,
		"is_staff":   false,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

func (h *TicketHandler) AdminReply(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var req struct {
		Body string `json:"body" validate:"required,min=1,max=10000"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}

	msg, err := h.repo.AddMessage(r.Context(), ticketID, userID, req.Body, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "ticket.reply", "ticket", ticketID, map[string]any{
		"message_id": msg.ID,
		"is_staff":   true,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

func (h *TicketHandler) CloseMine(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	ticket, err := h.repo.GetByID(r.Context(), ticketID)
	if err != nil || ticket == nil || ticket.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket not found"})
		return
	}

	// 幂等：已 closed 时直接返回 200，不报错。
	if ticket.Status == "closed" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "closed"})
		return
	}

	if _, err := h.repo.CloseByOwner(r.Context(), ticketID, userID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "ticket.close", "ticket", ticketID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "closed"})
}

func (h *TicketHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var req struct {
		Status string `json:"status" validate:"required,oneof=open pending closed"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}

	if err := h.repo.UpdateStatus(r.Context(), ticketID, req.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "ticket.update_status", "ticket", ticketID, map[string]any{
		"status": req.Status,
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": req.Status})
}
