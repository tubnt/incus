package portal

import (
	"context"
	"encoding/json"
	"fmt"
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
	p := ParsePageParams(r)
	orders, total, err := h.orders.ListPaged(r.Context(), p.Limit, p.Offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list orders"})
		return
	}
	if orders == nil {
		orders = []model.Order{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"orders": orders,
		"total":  total,
		"limit":  p.Limit,
		"offset": p.Offset,
	})
}

func (h *OrderHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)

	var req struct {
		ProductID   int64  `json:"product_id"`
		VMName      string `json:"vm_name"`
		OSImage     string `json:"os_image"`
		ClusterID   int64  `json:"cluster_id"`
		ClusterName string `json:"cluster_name"`
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
	clusterID := req.ClusterID
	if clusterID == 0 && req.ClusterName != "" {
		clusterID = h.clusters.IDByName(req.ClusterName)
	}
	if clusterID == 0 {
		// Fallback: first registered cluster. Keeps single-cluster deployments unchanged.
		clusterID = h.clusters.IDByName(clients[0].Name)
	}
	if clusterID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid cluster"})
		return
	}

	order, err := h.orders.Create(r.Context(), userID, req.ProductID, clusterID, product.PriceMonthly, product.Currency)
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

	// Resolve the order's cluster; fall back to first cluster for legacy orders
	// whose cluster_id may not be registered any more.
	client := clients[0]
	if order.ClusterID > 0 {
		if name := h.clusters.NameByID(order.ClusterID); name != "" {
			if c, ok := h.clusters.Get(name); ok {
				client = c
			}
		}
	}
	cc, _ := h.clusters.ConfigByName(client.Name)

	defProject := cc.DefaultProject
	if defProject == "" { defProject = "customers" }
	pool := cc.StoragePool
	if pool == "" { pool = "ceph-pool" }
	network := cc.Network
	if network == "" { network = "br-pub" }

	ip, gateway, cidr, ipErr := allocateIP(r.Context(), cc, 0)
	if ipErr != nil {
		slog.Error("allocate IP failed", "order", orderID, "error", ipErr)
		h.rollbackPayment(r.Context(), order, "", "ip allocation failed: "+ipErr.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "IP allocation failed, payment refunded"})
		return
	}

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
		h.rollbackPayment(r.Context(), order, ip, "vm provisioning failed: "+err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "VM provisioning failed, payment refunded"})
		return
	}

	h.orders.UpdateStatus(r.Context(), orderID, model.OrderActive)

	vm := &model.VM{
		Name:      result.VMName,
		ClusterID: h.clusters.IDByName(client.Name),
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
	if err := h.vmRepo.Create(r.Context(), vm); err != nil {
		slog.Error("vm row insert failed", "order", orderID, "name", result.VMName, "error", err)
	} else {
		attachIPToVM(r.Context(), result.IP, vm.ID)
	}

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

// rollbackPayment 在扣款成功但后续 VM 创建失败时调用：退款 + 释放 IP + 订单置 cancelled。
// 失败的每一步都只记日志，不 panic，以免破坏 HTTP 响应。
func (h *OrderHandler) rollbackPayment(ctx context.Context, order *model.Order, ip, reason string) {
	if userRepo != nil {
		desc := fmt.Sprintf("订单 #%d 失败退款: %s", order.ID, reason)
		if err := userRepo.AdjustBalance(ctx, order.UserID, order.Amount, "refund", desc, &order.ID); err != nil {
			slog.Error("payment refund failed", "order", order.ID, "error", err)
		}
	}
	if ip != "" && ipAddrRepo != nil {
		if err := ipAddrRepo.Release(ctx, ip); err != nil {
			slog.Error("release IP on rollback failed", "order", order.ID, "ip", ip, "error", err)
		}
	}
	if err := h.orders.UpdateStatus(ctx, order.ID, model.OrderCancelled); err != nil {
		slog.Error("mark order cancelled failed", "order", order.ID, "error", err)
	}
	slog.Warn("order payment rolled back", "order", order.ID, "reason", reason)
}
