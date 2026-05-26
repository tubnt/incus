package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/model"
)

// fakeUserRepo / fakeVMRepo / 等：单测注入的最小实现。Phase B 不引 sqlmock —— 7
// 个 endpoint 的依赖面都收敛到 model 层 + repo 接口，fake 已足够。

type fakeUserRepo struct {
	users map[int64]*model.User
	err   error
}

func (f *fakeUserRepo) GetByID(_ context.Context, id int64) (*model.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.users[id], nil
}

type fakeVMRepo struct {
	byID map[int64]*model.VM
	// byUser 索引 (userID → 整段 VM 列表，调用方分页时再切)
	byUser   map[int64][]model.VM
	getErr   error
	listErr  error
	lastCall struct {
		userID         int64
		limit, offset  int
	}
}

func (f *fakeVMRepo) GetByID(_ context.Context, id int64) (*model.VM, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.byID[id], nil
}

func (f *fakeVMRepo) ListByUserPaged(_ context.Context, userID int64, limit, offset int) ([]model.VM, int64, error) {
	f.lastCall.userID = userID
	f.lastCall.limit = limit
	f.lastCall.offset = offset
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	all := f.byUser[userID]
	total := int64(len(all))
	if limit <= 0 || offset >= len(all) {
		return []model.VM{}, total, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return append([]model.VM{}, all[offset:end]...), total, nil
}

type fakeProductRepo struct {
	products []model.Product
	err      error
}

func (f *fakeProductRepo) ListActive(_ context.Context) ([]model.Product, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.products, nil
}

type fakeClusterRepo struct {
	clusters []model.Cluster
	byID     map[int64]*model.Cluster
	listErr  error
	getErr   error
}

func (f *fakeClusterRepo) GetByID(_ context.Context, id int64) (*model.Cluster, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.byID[id], nil
}

func (f *fakeClusterRepo) List(_ context.Context) ([]model.Cluster, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.clusters, nil
}

type fakeOSTemplateRepo struct {
	templates []model.OSTemplate
	err       error
}

func (f *fakeOSTemplateRepo) ListEnabled(_ context.Context) ([]model.OSTemplate, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.templates, nil
}

type fakeSSHKeyRepo struct {
	byUser map[int64][]model.SSHKey
	err    error
}

func (f *fakeSSHKeyRepo) ListByUserPaged(_ context.Context, userID int64, limit, offset int) ([]model.SSHKey, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	all := f.byUser[userID]
	total := int64(len(all))
	if limit <= 0 || offset >= len(all) {
		return []model.SSHKey{}, total, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return append([]model.SSHKey{}, all[offset:end]...), total, nil
}

type fakeOrderRepo struct {
	orders map[int64]*model.Order
	err    error
}

func (f *fakeOrderRepo) GetByID(_ context.Context, id int64) (*model.Order, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.orders[id], nil
}

// newRouterWithUser 把 handler 挂到 chi router 并注入 ctx user_id，
// 模拟 RequireBearer 通过后的环境。userID=0 表示不注入（测未授权路径）。
func newRouterWithUser(h *Handler, userID int64) http.Handler {
	r := chi.NewRouter()
	if userID > 0 {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := context.WithValue(req.Context(), middleware.CtxUserID, userID)
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
	}
	r.Route("/v1", h.Routes)
	return r
}

// decodeData 用泛型把 writePage 响应里的 data 段反序列化到具体类型，避免
// 每个 endpoint 测试都重复同一段。
func decodeData[T any](t *testing.T, body []byte) (items []T, page, pageSize, pages, total int) {
	t.Helper()
	var resp struct {
		Data     []T `json:"data"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Pages    int `json:"pages"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, string(body))
	}
	return resp.Data, resp.Page, resp.PageSize, resp.Pages, resp.Total
}

func decodeErr(t *testing.T, body []byte) []FieldError {
	t.Helper()
	var resp struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode err: %v (raw=%s)", err, string(body))
	}
	return resp.Errors
}

// --- /v1/account ---

func TestAccount_Happy(t *testing.T) {
	h := New(Deps{
		Users: &fakeUserRepo{users: map[int64]*model.User{
			42: {ID: 42, Email: "alice@example.com", Balance: 12.34},
		}},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 42).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/account", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var dto AccountDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.ID != 42 || dto.Email != "alice@example.com" || dto.Balance != 12.34 || dto.Currency != "USD" {
		t.Fatalf("got %+v", dto)
	}
}

func TestAccount_UserNotFound(t *testing.T) {
	h := New(Deps{Users: &fakeUserRepo{users: map[int64]*model.User{}}})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 99).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/account", nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if len(errs) != 1 || errs[0].Reason != "not_found" {
		t.Fatalf("got %+v", errs)
	}
}

func TestAccount_RepoError(t *testing.T) {
	h := New(Deps{Users: &fakeUserRepo{err: errors.New("boom")}})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 1).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/account", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

// --- /v1/instances list ---

func TestInstances_Pagination(t *testing.T) {
	ip := "10.0.0.5"
	vms := []model.VM{
		{ID: 3, Name: "vm-c", UserID: 7, ClusterID: 1, OSImage: "ubuntu/24.04/cloud", Status: "running", IP: &ip, CreatedAt: time.Now(), OrderID: ptrInt64(100)},
		{ID: 2, Name: "vm-b", UserID: 7, ClusterID: 2, OSImage: "debian/12/cloud", Status: "stopped", CreatedAt: time.Now()},
		{ID: 1, Name: "vm-a", UserID: 7, ClusterID: 1, OSImage: "ubuntu/22.04/cloud", Status: "running", CreatedAt: time.Now()},
	}
	h := New(Deps{
		VMs: &fakeVMRepo{byUser: map[int64][]model.VM{7: vms}},
		Clusters: &fakeClusterRepo{clusters: []model.Cluster{
			{ID: 1, Name: "cluster-a"},
			{ID: 2, Name: "cluster-b"},
		}},
		Orders: &fakeOrderRepo{orders: map[int64]*model.Order{
			100: {ID: 100, ProductID: 555},
		}},
	})

	// page 1 size 2 → 前两条
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/instances?page=1&page_size=2", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	items, page, pageSize, pages, total := decodeData[InstanceDTO](t, rr.Body.Bytes())
	if page != 1 || pageSize != 2 || pages != 2 || total != 3 {
		t.Fatalf("paging: page=%d size=%d pages=%d total=%d", page, pageSize, pages, total)
	}
	if len(items) != 2 || items[0].ID != 3 || items[1].ID != 2 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Region != "cluster-a" || items[1].Region != "cluster-b" {
		t.Fatalf("region resolution wrong: %+v / %+v", items[0], items[1])
	}
	if items[0].Type != 555 || items[1].Type != 0 {
		t.Fatalf("product_id resolution wrong: %d / %d", items[0].Type, items[1].Type)
	}
	if items[0].IP4 != "10.0.0.5" || items[1].IP4 != "" {
		t.Fatalf("ip4 mapping wrong: %q / %q", items[0].IP4, items[1].IP4)
	}
	if items[0].Tags == nil || len(items[0].Tags) != 0 {
		t.Fatalf("tags should be empty slice not nil: %+v", items[0].Tags)
	}
}

func TestInstances_EmptyList(t *testing.T) {
	h := New(Deps{
		VMs:      &fakeVMRepo{byUser: map[int64][]model.VM{}},
		Clusters: &fakeClusterRepo{},
		Orders:   &fakeOrderRepo{},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/instances", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	items, _, _, pages, total := decodeData[InstanceDTO](t, rr.Body.Bytes())
	if len(items) != 0 || pages != 0 || total != 0 {
		t.Fatalf("expected empty list got len=%d pages=%d total=%d", len(items), pages, total)
	}
}

func TestInstances_BadPagination(t *testing.T) {
	h := New(Deps{
		VMs: &fakeVMRepo{}, Clusters: &fakeClusterRepo{}, Orders: &fakeOrderRepo{},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/instances?page_size=999", nil))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", rr.Code, rr.Body.String())
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if len(errs) != 1 || errs[0].Field != "page_size" || errs[0].Reason != "exceeds_max" {
		t.Fatalf("got %+v", errs)
	}
}

// --- /v1/instances/{id} ---

func TestInstanceByID_Owner(t *testing.T) {
	now := time.Now()
	h := New(Deps{
		VMs: &fakeVMRepo{byID: map[int64]*model.VM{
			10: {ID: 10, Name: "vm-x", UserID: 7, ClusterID: 1, OSImage: "ubuntu/24.04/cloud", Status: "running", CreatedAt: now, OrderID: ptrInt64(100)},
		}},
		Clusters: &fakeClusterRepo{byID: map[int64]*model.Cluster{
			1: {ID: 1, Name: "cluster-a"},
		}},
		Orders: &fakeOrderRepo{orders: map[int64]*model.Order{
			100: {ID: 100, ProductID: 7},
		}},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/instances/10", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var dto InstanceDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.ID != 10 || dto.Label != "vm-x" || dto.Region != "cluster-a" || dto.Type != 7 || dto.Image != "ubuntu/24.04/cloud" {
		t.Fatalf("got %+v", dto)
	}
}

func TestInstanceByID_NotOwner_Returns404(t *testing.T) {
	// 不同用户的 VM → 应该 404，不能 403（避免泄露存在性）
	h := New(Deps{
		VMs: &fakeVMRepo{byID: map[int64]*model.VM{
			10: {ID: 10, UserID: 99 /* not us */, Status: "running"},
		}},
		Clusters: &fakeClusterRepo{},
		Orders:   &fakeOrderRepo{},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/instances/10", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (don't leak existence)", rr.Code)
	}
}

func TestInstanceByID_Trashed_Returns404(t *testing.T) {
	// 回收站内 VM 也按 404 处理，与 ListByUserPaged 过滤口径一致
	trashedAt := time.Now()
	h := New(Deps{
		VMs: &fakeVMRepo{byID: map[int64]*model.VM{
			10: {ID: 10, UserID: 7, TrashedAt: &trashedAt, Status: "running"},
		}},
		Clusters: &fakeClusterRepo{},
		Orders:   &fakeOrderRepo{},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/instances/10", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestInstanceByID_BadID(t *testing.T) {
	h := New(Deps{VMs: &fakeVMRepo{}, Clusters: &fakeClusterRepo{}, Orders: &fakeOrderRepo{}})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/instances/abc", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// --- /v1/types ---

func TestTypes_Happy(t *testing.T) {
	dailyA := 0.50
	h := New(Deps{
		Products: &fakeProductRepo{products: []model.Product{
			{ID: 1, Name: "Small", Slug: "small", CPU: 1, MemoryMB: 1024, DiskGB: 20, BandwidthTB: 1, PriceMonthly: 5.0, PriceDaily: &dailyA},
			{ID: 2, Name: "Medium", Slug: "medium", CPU: 2, MemoryMB: 2048, DiskGB: 40, BandwidthTB: 2, PriceMonthly: 10.0},
		}},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 1).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/types?page=1&page_size=10", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	items, _, _, _, total := decodeData[TypeDTO](t, rr.Body.Bytes())
	if total != 2 || len(items) != 2 {
		t.Fatalf("got total=%d len=%d", total, len(items))
	}
	if items[0].ID != 1 || items[0].Label != "Small" || items[0].Prices.Monthly != 5.0 || items[0].Prices.Daily == nil || *items[0].Prices.Daily != 0.5 {
		t.Fatalf("dto[0] = %+v", items[0])
	}
	if items[1].Prices.Daily != nil {
		t.Fatalf("daily nil should omitempty, got %v", items[1].Prices.Daily)
	}
}

func TestTypes_PageOutOfRange(t *testing.T) {
	h := New(Deps{Products: &fakeProductRepo{products: []model.Product{{ID: 1, Slug: "s"}}}})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 1).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/types?page=10&page_size=10", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	items, _, _, _, total := decodeData[TypeDTO](t, rr.Body.Bytes())
	if len(items) != 0 || total != 1 {
		t.Fatalf("got items=%d total=%d (want 0/1)", len(items), total)
	}
}

func TestTypes_RepoError(t *testing.T) {
	h := New(Deps{Products: &fakeProductRepo{err: errors.New("db down")}})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 1).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/types", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

// --- /v1/regions ---

func TestRegions_Happy(t *testing.T) {
	h := New(Deps{
		Clusters: &fakeClusterRepo{clusters: []model.Cluster{
			{ID: 1, Name: "cluster-a", Country: "US", City: "NYC", RegionStatus: "available", Capabilities: []string{"instances"}},
			{ID: 2, Name: "cluster-b", RegionStatus: "maintenance", Capabilities: []string{"instances"}},
		}},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 1).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/regions", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	items, _, _, _, total := decodeData[RegionDTO](t, rr.Body.Bytes())
	if total != 2 || len(items) != 2 {
		t.Fatalf("total=%d len=%d", total, len(items))
	}
	if items[0].ID != "cluster-a" || items[0].Country != "US" || items[0].City != "NYC" || items[0].Status != "available" {
		t.Fatalf("got %+v", items[0])
	}
	if items[1].Status != "maintenance" || len(items[1].Capabilities) != 1 || items[1].Capabilities[0] != "instances" {
		t.Fatalf("got %+v", items[1])
	}
}

// --- /v1/images ---

func TestImages_SourceParsing(t *testing.T) {
	h := New(Deps{
		OSTemplates: &fakeOSTemplateRepo{templates: []model.OSTemplate{
			{ID: 1, Slug: "ubuntu-24-04", Name: "Ubuntu 24.04 LTS", Source: "ubuntu/24.04/cloud"},
			{ID: 2, Slug: "windows", Name: "Windows", Source: "windows"},
		}},
	})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 1).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/images", nil))
	items, _, _, _, total := decodeData[ImageDTO](t, rr.Body.Bytes())
	if total != 2 {
		t.Fatalf("total=%d", total)
	}
	if items[0].ID != "ubuntu-24-04" || items[0].OS != "ubuntu" || items[0].Version != "24.04" {
		t.Fatalf("items[0] = %+v", items[0])
	}
	if items[1].OS != "windows" || items[1].Version != "" {
		t.Fatalf("items[1] = %+v", items[1])
	}
}

// --- /v1/ssh-keys ---

func TestSSHKeys_FiltersByUser(t *testing.T) {
	h := New(Deps{
		SSHKeys: &fakeSSHKeyRepo{byUser: map[int64][]model.SSHKey{
			7: {
				{ID: 1, UserID: 7, Name: "laptop", Fingerprint: "SHA256:abc", PublicKey: "ssh-rsa AAAA..."},
				{ID: 2, UserID: 7, Name: "desktop", Fingerprint: "SHA256:def", PublicKey: "ssh-rsa BBBB..."},
			},
			99: {{ID: 3, UserID: 99, Name: "other"}},
		}},
	})

	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/ssh-keys", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	items, _, _, _, total := decodeData[SSHKeyDTO](t, rr.Body.Bytes())
	if total != 2 || len(items) != 2 {
		t.Fatalf("total=%d len=%d", total, len(items))
	}
	if items[0].Label != "laptop" || items[0].Fingerprint != "SHA256:abc" || items[0].PublicKey != "ssh-rsa AAAA..." {
		t.Fatalf("items[0] = %+v", items[0])
	}
}

func TestSSHKeys_EmptyList(t *testing.T) {
	h := New(Deps{SSHKeys: &fakeSSHKeyRepo{byUser: map[int64][]model.SSHKey{}}})
	rr := httptest.NewRecorder()
	newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/ssh-keys", nil))
	items, _, _, pages, total := decodeData[SSHKeyDTO](t, rr.Body.Bytes())
	if len(items) != 0 || total != 0 || pages != 0 {
		t.Fatalf("expected empty got items=%d total=%d pages=%d", len(items), total, pages)
	}
}

// --- 通用 unauthorized ---

func TestEndpoints_NoUserID_Unauthorized(t *testing.T) {
	// userID=0 → 不注入 CtxUserID。Account / Instances / SSHKeys 这种依赖 user 的
	// 端点会返 401 unauthorized；其余依然能跑出 200（依赖 user 的只有这三个）。
	h := New(Deps{
		Users:    &fakeUserRepo{},
		VMs:      &fakeVMRepo{}, Clusters: &fakeClusterRepo{}, Orders: &fakeOrderRepo{},
		SSHKeys: &fakeSSHKeyRepo{},
	})
	paths := []string{"/v1/account", "/v1/instances", "/v1/instances/1", "/v1/ssh-keys"}
	for _, p := range paths {
		rr := httptest.NewRecorder()
		newRouterWithUser(h, 0).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want 401", p, rr.Code)
		}
	}
}

// --- missing deps → 500 ---

func TestEndpoints_MissingDeps_500(t *testing.T) {
	// 空 Deps 时所有 endpoint 都应返 500 internal —— main.go 装配错时不要 nil-deref panic
	h := New(Deps{})
	paths := []string{"/v1/account", "/v1/instances", "/v1/instances/1", "/v1/types", "/v1/regions", "/v1/images", "/v1/ssh-keys"}
	for _, p := range paths {
		rr := httptest.NewRecorder()
		newRouterWithUser(h, 7).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("%s status = %d, want 500", p, rr.Code)
		}
	}
}

// --- DTO 辅助 ---

func ptrInt64(v int64) *int64 { return &v }
