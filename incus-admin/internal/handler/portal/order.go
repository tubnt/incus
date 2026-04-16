package portal

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/service"
)

type OrderHandler struct {
	orders   *repository.OrderRepo
	products *repository.ProductRepo
	vmSvc    *service.VMService
	vmRepo   *repository.VMRepo
	sshKeys  *repository.SSHKeyRepo
	clusters *cluster.Manager
}

func NewOrderHandler(orders *repository.OrderRepo, products *repository.ProductRepo, vmSvc *service.VMService, vmRepo *repository.VMRepo, sshKeys *repository.SSHKeyRepo, clusters *cluster.Manager) *OrderHandler {
	return &OrderHandler{orders: orders, products: products, vmSvc: vmSvc, vmRepo: vmRepo, sshKeys: sshKeys, clusters: clusters}
}

func (h *OrderHandler) PortalRoutes(r chi.Router) {
	r.Get("/orders", h.ListMine)
	r.Post("/orders", h.Create)
	r.Post("/orders/{id}/pay", h.Pay)
	r.Post("/orders/{id}/cancel", h.Cancel)
}

func (h *OrderHandler) AdminRoutes(r chi.Router) {
	r.Get("/orders", h.ListAll)
	r.Put("/orders/{id}/status", h.UpdateStatus)
}

func (h *OrderHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	orders, err := h.orders.ListByUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list orders"})
		return
	}
	if orders == nil {
		orders = []model.Order{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (h *OrderHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	orders, err := h.orders.ListAll(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list orders"})
		return
	}
	if orders == nil {
		orders = []model.Order{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (h *OrderHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)

	var req struct {
		ProductID int64  `json:"product_id"`
		VMName    string `json:"vm_name"`
		OSImage   string `json:"os_image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}

	product, err := h.products.GetByID(r.Context(), req.ProductID)
	if err != nil || product == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "product not found"})
		return
	}
	if req.OSImage == "" {
		req.OSImage = "images:ubuntu/24.04/cloud"
	}

	clients := h.clusters.List()
	if len(clients) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no clusters"})
		return
	}
	clusterID := int64(1)

	order, err := h.orders.Create(r.Context(), userID, req.ProductID, clusterID, product.PriceMonthly)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"order": order})
}

func (h *OrderHandler) Pay(w http.ResponseWriter, r *http.Request) {
	orderID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)

	var payReq struct {
		VMName  string `json:"vm_name"`
		OSImage string `json:"os_image"`
	}
	json.NewDecoder(r.Body).Decode(&payReq)

	order, err := h.orders.GetByID(r.Context(), orderID)
	if err != nil || order == nil || order.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "order not found"})
		return
	}

	if err := h.orders.PayWithBalance(r.Context(), orderID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	product, _ := h.products.GetByID(r.Context(), order.ProductID)
	if product == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "paid", "note": "product not found, VM not created"})
		return
	}

	clients := h.clusters.List()
	if len(clients) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "paid", "note": "no cluster, VM not created"})
		return
	}

	client := clients[0]
	cc, _ := h.clusters.ConfigByName(client.Name)

	defProject := cc.DefaultProject
	if defProject == "" { defProject = "customers" }
	pool := cc.StoragePool
	if pool == "" { pool = "ceph-pool" }
	network := cc.Network
	if network == "" { network = "br-pub" }

	ip, gateway, cidr, _ := allocateIP(r.Context(), cc, 0)

	sshKeys, _ := h.sshKeys.GetByUser(r.Context(), userID)

	osImage := payReq.OSImage
	if osImage == "" {
		osImage = "images:ubuntu/24.04/cloud"
	}

	result, err := h.vmSvc.Create(r.Context(), service.CreateVMParams{
		ClusterName: client.Name,
		Project:     defProject,
		UserID:      userID,
		VMName:      payReq.VMName,
		CPU:         product.CPU,
		MemoryMB:    product.MemoryMB,
		DiskGB:      product.DiskGB,
		OSImage:     osImage,
		SSHKeys:     sshKeys,
		IP:          ip,
		Gateway:     gateway,
		SubnetCIDR:  cidr,
		StoragePool: pool,
		Network:     network,
	})
	if err != nil {
		slog.Error("auto-provision VM failed after payment", "order", orderID, "error", err)
		h.orders.UpdateStatus(r.Context(), orderID, model.OrderPaid)
		writeJSON(w, http.StatusOK, map[string]any{"status": "paid", "error": "VM provisioning failed: " + err.Error()})
		return
	}

	h.orders.UpdateStatus(r.Context(), orderID, model.OrderActive)

	vm := &model.VM{
		Name:      result.VMName,
		ClusterID: 1,
		UserID:    userID,
		OrderID:   &orderID,
		Status:    model.VMStatusRunning,
		CPU:       product.CPU,
		MemoryMB:  product.MemoryMB,
		DiskGB:    product.DiskGB,
		OSImage:   osImage,
		Node:      result.Node,
		Password:  &result.Password,
	}
	if result.IP != "" {
		vm.IP = &result.IP
	}
	h.vmRepo.Create(r.Context(), vm)

	slog.Info("VM auto-provisioned after payment", "order", orderID, "vm", result.VMName)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "provisioned",
		"vm_name":  result.VMName,
		"ip":       result.IP,
		"password": result.Password,
		"username": result.Username,
	})
}

func (h *OrderHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	orderID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)

	order, err := h.orders.GetByID(r.Context(), orderID)
	if err != nil || order == nil || order.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "order not found"})
		return
	}
	if order.Status != model.OrderPending {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "only pending orders can be cancelled"})
		return
	}

	if err := h.orders.UpdateStatus(r.Context(), orderID, model.OrderCancelled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "order.cancel", "order", orderID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "cancelled"})
}

func (h *OrderHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	orderID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status required"})
		return
	}
	if err := h.orders.UpdateStatus(r.Context(), orderID, req.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": req.Status})
}
