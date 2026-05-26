package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/incuscloud/incus-admin/internal/handler/portal"
	"github.com/incuscloud/incus-admin/internal/model"
)

// fakeClusterByName / fakeOSTemplateBySlug / fakeProductBySlug / fakeSSHKeyOwner：
// Phase D 用 Deps 新增的 5 个接口的最小 fake；与 read-only 测试套并存，
// 不复用 fakeProductRepo etc.（那批 fake 类型签名固定）。

type fakeClusterByName struct {
	byName map[string]*model.Cluster
	err    error
}

func (f *fakeClusterByName) GetByName(_ context.Context, name string) (*model.Cluster, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byName[name], nil
}

type fakeOSTemplateBySlug struct {
	bySlug map[string]*model.OSTemplate
	err    error
}

func (f *fakeOSTemplateBySlug) GetBySlug(_ context.Context, slug string) (*model.OSTemplate, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.bySlug[slug], nil
}

type fakeSSHKeyOwner struct {
	byUser map[int64][]model.SSHKey
	err    error
}

func (f *fakeSSHKeyOwner) ListByUser(_ context.Context, userID int64) ([]model.SSHKey, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byUser[userID], nil
}

// fakeOrderProvision 捕获最后一次入参，并按 cfg 决定返回值/错误，
// 测试不同失败分支无需各自起一份 *portal.OrderHandler。
type fakeOrderProvision struct {
	last     portal.V1ProvisionRequest
	called   int
	result   *portal.V1ProvisionResult
	provErr  *portal.V1ProvisionError
}

func (f *fakeOrderProvision) CreatePayProvision(_ *http.Request, req portal.V1ProvisionRequest) (*portal.V1ProvisionResult, *portal.V1ProvisionError) {
	f.called++
	f.last = req
	if f.provErr != nil {
		return nil, f.provErr
	}
	return f.result, nil
}

type fakeVMTrasher struct {
	last struct {
		userID, vmID int64
	}
	called int
	err    *portal.V1ProvisionError
}

func (f *fakeVMTrasher) V1TrashByID(_ *http.Request, userID, vmID int64) *portal.V1ProvisionError {
	f.called++
	f.last.userID = userID
	f.last.vmID = vmID
	return f.err
}

type fakeVMActioner struct {
	last struct {
		userID, vmID int64
		action       string
	}
	called int
	err    *portal.V1ProvisionError
}

func (f *fakeVMActioner) V1ActionByID(_ *http.Request, userID, vmID int64, action string) *portal.V1ProvisionError {
	f.called++
	f.last.userID = userID
	f.last.vmID = vmID
	f.last.action = action
	return f.err
}

// makeWriteDeps 给 POST/DELETE/actions 测试统一构造一份"全绿"的 Deps；
// 各 test 在上面打点改一两个字段以测异常分支。
func makeWriteDeps() (Deps, *fakeOrderProvision, *fakeVMTrasher, *fakeVMActioner) {
	op := &fakeOrderProvision{
		result: &portal.V1ProvisionResult{
			VMID: 42, OrderID: 7, JobID: 11, VMName: "test-vm", IP: "10.0.0.5",
		},
	}
	vt := &fakeVMTrasher{}
	va := &fakeVMActioner{}
	return Deps{
		Users:    &fakeUserRepo{users: map[int64]*model.User{1: {ID: 1, Email: "u@e", Balance: 100}}},
		Products: &fakeProductRepo{products: []model.Product{
			{ID: 1, Slug: "nano", Active: true, PriceMonthly: 5, PeriodSupported: []string{"daily", "monthly"}, PriceDaily: ptr(0.2)},
		}},
		ClustersByName: &fakeClusterByName{byName: map[string]*model.Cluster{
			"hkg-1": {ID: 1, Name: "hkg-1", RegionStatus: model.RegionStatusAvailable},
		}},
		OSTemplatesBySlug: &fakeOSTemplateBySlug{bySlug: map[string]*model.OSTemplate{
			"ubuntu-24": {Slug: "ubuntu-24", Source: "ubuntu/24.04/cloud", Enabled: true},
		}},
		ProductsBySlug: &fakeProductRepo{products: []model.Product{
			{ID: 1, Slug: "nano", Active: true, PriceMonthly: 5, PeriodSupported: []string{"daily", "monthly"}, PriceDaily: ptr(0.2)},
		}},
		SSHKeysOwner: &fakeSSHKeyOwner{byUser: map[int64][]model.SSHKey{
			1: {{ID: 100, UserID: 1, PublicKey: "ssh-rsa AAA u@h"}},
		}},
		OrderProvision: op,
		VMTrash:        vt,
		VMAction:       va,
	}, op, vt, va
}

func ptr[T any](v T) *T { return &v }

// postInstance 发送一个 POST /v1/instances 请求并断言 status。
func postInstance(t *testing.T, h *Handler, userID int64, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := newRouterWithUser(h, userID)
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/instances", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// TestCreateInstance_Happy：daily + 余额够 + 全字段合法 → 201 + Location +
// instance DTO（status=pending）+ order_id + job_id。
func TestCreateInstance_Happy(t *testing.T) {
	deps, op, _, _ := makeWriteDeps()
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region":   "hkg-1",
		"type":     "nano",
		"image":    "ubuntu-24",
		"label":    "my-vm",
		"period":   "daily",
		"ssh_keys": []int64{100},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/v1/instances/42" {
		t.Errorf("Location = %q want /v1/instances/42", loc)
	}
	var resp createInstanceResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != 42 || resp.Status != "pending" {
		t.Errorf("response = %+v", resp)
	}
	if resp.OrderID != 7 || resp.JobID != 11 {
		t.Errorf("order/job = %d/%d", resp.OrderID, resp.JobID)
	}
	if resp.IP4 != "10.0.0.5" {
		t.Errorf("ip4 = %q", resp.IP4)
	}
	if resp.Region != "hkg-1" {
		t.Errorf("region = %q", resp.Region)
	}
	if resp.Image != "ubuntu/24.04/cloud" {
		t.Errorf("image = %q (want resolved from slug to source)", resp.Image)
	}
	if op.last.Period != "daily" {
		t.Errorf("period passed to OrderProvision = %q", op.last.Period)
	}
	if len(op.last.SSHKeys) != 1 || op.last.SSHKeys[0] != "ssh-rsa AAA u@h" {
		t.Errorf("ssh keys = %+v", op.last.SSHKeys)
	}
}

// TestCreateInstance_NumericType：type 可以是 JSON number（不止 slug 字符串），
// product.id=1 → product 解析成功。
func TestCreateInstance_NumericType(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1",
		"type":   1, // numeric
		"image":  "ubuntu-24",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateInstance_RegionUnavailable：region 行存在但 region_status='unavailable'
// → 422 reason=region_unavailable。
func TestCreateInstance_RegionUnavailable(t *testing.T) {
	deps, op, _, _ := makeWriteDeps()
	deps.ClustersByName = &fakeClusterByName{byName: map[string]*model.Cluster{
		"hkg-1": {ID: 1, Name: "hkg-1", RegionStatus: model.RegionStatusMaintenance},
	}}
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", rr.Code)
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if errs[0].Field != "region" || errs[0].Reason != "region_unavailable" {
		t.Errorf("err = %+v", errs)
	}
	if op.called != 0 {
		t.Errorf("OrderProvision should not be called on validation fail (called=%d)", op.called)
	}
}

// TestCreateInstance_RegionNotFound：region 不存在 → 422 reason=region_unavailable
// （与 maintenance 同 reason，避免泄露 region 存在性）。
func TestCreateInstance_RegionNotFound(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "no-such", "type": "nano", "image": "ubuntu-24",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", rr.Code)
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if errs[0].Field != "region" || errs[0].Reason != "region_unavailable" {
		t.Errorf("err = %+v", errs)
	}
}

// TestCreateInstance_UnsupportedPeriod：product 只支持 monthly，请求 period=daily → 422。
func TestCreateInstance_UnsupportedPeriod(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	deps.Products = &fakeProductRepo{products: []model.Product{
		{ID: 1, Slug: "nano", Active: true, PriceMonthly: 5, PeriodSupported: []string{"monthly"}},
	}}
	deps.ProductsBySlug = deps.Products.(*fakeProductRepo)

	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24", "period": "daily",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if errs[0].Field != "period" || errs[0].Reason != "unsupported_period" {
		t.Errorf("err = %+v", errs)
	}
}

// TestCreateInstance_TypeNotFound：type=unknown slug → 422 type_not_found。
func TestCreateInstance_TypeNotFound(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "no-such", "image": "ubuntu-24",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", rr.Code)
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if errs[0].Field != "type" || errs[0].Reason != "type_not_found" {
		t.Errorf("err = %+v", errs)
	}
}

// TestCreateInstance_ImageDisabled：os template enabled=false → 422 image_not_found。
func TestCreateInstance_ImageDisabled(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	deps.OSTemplatesBySlug = &fakeOSTemplateBySlug{bySlug: map[string]*model.OSTemplate{
		"ubuntu-24": {Slug: "ubuntu-24", Source: "ubuntu/24.04/cloud", Enabled: false},
	}}
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", rr.Code)
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if errs[0].Field != "image" || errs[0].Reason != "image_not_found" {
		t.Errorf("err = %+v", errs)
	}
}

// TestCreateInstance_SSHKeyNotMine：ssh_keys 里包含不属于当前 user 的 id → 422 ssh_key_not_found。
func TestCreateInstance_SSHKeyNotMine(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
		"ssh_keys": []int64{999}, // 不存在
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if errs[0].Field != "ssh_keys" || errs[0].Reason != "ssh_key_not_found" {
		t.Errorf("err = %+v", errs)
	}
}

// TestCreateInstance_InsufficientBalance：OrderProvision 返 402 → handler 透传。
// 模拟 fakeOrderProvision 主动返 V1ProvisionError 而不是真跑订单逻辑。
func TestCreateInstance_InsufficientBalance(t *testing.T) {
	deps, op, _, _ := makeWriteDeps()
	op.provErr = &portal.V1ProvisionError{
		Status: http.StatusPaymentRequired, Field: "balance", Reason: "insufficient_balance",
	}
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
	})
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status=%d", rr.Code)
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if errs[0].Field != "balance" || errs[0].Reason != "insufficient_balance" {
		t.Errorf("err = %+v", errs)
	}
}

// TestCreateInstance_MissingFields：region / type / image 任一缺失 → 422 required。
func TestCreateInstance_MissingFields(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	cases := []struct {
		field string
		body  map[string]any
	}{
		{"region", map[string]any{"type": "nano", "image": "ubuntu-24"}},
		{"type", map[string]any{"region": "hkg-1", "image": "ubuntu-24"}},
		{"image", map[string]any{"region": "hkg-1", "type": "nano"}},
	}
	for _, tc := range cases {
		rr := postInstance(t, New(deps), 1, tc.body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status=%d", tc.field, rr.Code)
			continue
		}
		errs := decodeErr(t, rr.Body.Bytes())
		if errs[0].Field != tc.field || errs[0].Reason != "required" {
			t.Errorf("%s: err=%+v", tc.field, errs)
		}
	}
}

// TestCreateInstance_LabelInvalid：label 含非法字符 → 422 invalid_label。
func TestCreateInstance_LabelInvalid(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
		"label": "bad name",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", rr.Code)
	}
	errs := decodeErr(t, rr.Body.Bytes())
	if errs[0].Field != "label" || errs[0].Reason != "invalid_label" {
		t.Errorf("err=%+v", errs)
	}
}

// TestCreateInstance_NoUserID：缺 Bearer middleware → 401 unauthorized。
func TestCreateInstance_NoUserID(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	rr := postInstance(t, New(deps), 0, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestCreateInstance_MissingDeps：Deps.OrderProvision == nil → 500 internal。
func TestCreateInstance_MissingDeps(t *testing.T) {
	rr := postInstance(t, New(Deps{}), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestDeleteInstance_Happy：owner 校验通过 → 202 + status=deleting。
func TestDeleteInstance_Happy(t *testing.T) {
	deps, _, vt, _ := makeWriteDeps()
	r := newRouterWithUser(New(deps), 1)
	req := httptest.NewRequest(http.MethodDelete, "/v1/instances/42", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "deleting" {
		t.Errorf("status = %v", resp["status"])
	}
	if vt.last.vmID != 42 || vt.last.userID != 1 {
		t.Errorf("call captured = %+v", vt.last)
	}
}

// TestDeleteInstance_NotMine：trash 服务返 not_found → handler 透传 404。
func TestDeleteInstance_NotMine(t *testing.T) {
	deps, _, vt, _ := makeWriteDeps()
	vt.err = &portal.V1ProvisionError{Status: http.StatusNotFound, Reason: "not_found"}
	r := newRouterWithUser(New(deps), 1)
	req := httptest.NewRequest(http.MethodDelete, "/v1/instances/42", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestDeleteInstance_BadID：非整数 id → 404 not_found（不暴露 id 解析细节）。
func TestDeleteInstance_BadID(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	r := newRouterWithUser(New(deps), 1)
	req := httptest.NewRequest(http.MethodDelete, "/v1/instances/abc", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestDeleteInstance_MissingDeps：VMTrash 未注入 → 500。
func TestDeleteInstance_MissingDeps(t *testing.T) {
	r := newRouterWithUser(New(Deps{}), 1)
	req := httptest.NewRequest(http.MethodDelete, "/v1/instances/42", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestActionInstance_Happy：reboot/shutdown/boot 各 happy + owner fail。
func TestActionInstance_Happy(t *testing.T) {
	actions := []string{"reboot", "shutdown", "boot"}
	for _, a := range actions {
		deps, _, _, va := makeWriteDeps()
		r := newRouterWithUser(New(deps), 1)
		req := httptest.NewRequest(http.MethodPost, "/v1/instances/42/"+a, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Errorf("%s: status=%d body=%s", a, rr.Code, rr.Body.String())
			continue
		}
		var resp map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp["status"] != a+"ing" {
			t.Errorf("%s: status = %v", a, resp["status"])
		}
		if va.last.vmID != 42 || va.last.userID != 1 || va.last.action != a {
			t.Errorf("%s: call captured = %+v", a, va.last)
		}
	}
}

// TestActionInstance_NotMine：action 服务返 404 → handler 透传 404。
func TestActionInstance_NotMine(t *testing.T) {
	deps, _, _, va := makeWriteDeps()
	va.err = &portal.V1ProvisionError{Status: http.StatusNotFound, Reason: "not_found"}
	r := newRouterWithUser(New(deps), 1)
	req := httptest.NewRequest(http.MethodPost, "/v1/instances/42/reboot", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestActionInstance_MissingDeps：VMAction 未注入 → 500。
func TestActionInstance_MissingDeps(t *testing.T) {
	r := newRouterWithUser(New(Deps{}), 1)
	req := httptest.NewRequest(http.MethodPost, "/v1/instances/42/reboot", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestParseInstanceID 单元覆盖：负数、零、非整数都视为 not_found 触发。
func TestParseInstanceID(t *testing.T) {
	cases := []struct {
		in       string
		wantErr  bool
	}{
		{"42", false},
		{"0", true},
		{"-1", true},
		{"abc", true},
		{"", true},
	}
	for _, c := range cases {
		_, badErr := parseInstanceID(c.in)
		if badErr != c.wantErr {
			t.Errorf("parseInstanceID(%q) badErr=%v want %v", c.in, badErr, c.wantErr)
		}
	}
}

// TestValidateInstanceLabel 单元覆盖：长度边界 + 字符集 + 起始字符。
func TestValidateInstanceLabel(t *testing.T) {
	good := []string{"a", "vm1", "my-vm", "ABC-123"}
	bad := []string{"-leading", "has space", "underscore_", "dotted.name", ""}
	// 64 chars exceeds limit
	tooLong := ""
	for i := 0; i < 64; i++ {
		tooLong += "a"
	}
	bad = append(bad, tooLong)
	for _, g := range good {
		if r := validateInstanceLabel(g); r != "" {
			t.Errorf("good %q rejected: %s", g, r)
		}
	}
	for _, b := range bad {
		if r := validateInstanceLabel(b); r == "" {
			t.Errorf("bad %q accepted", b)
		}
	}
}

// TestResolveSSHKeys_StringIDs：ssh_keys 也接受数字字符串数组。
func TestResolveSSHKeys_StringIDs(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
		"ssh_keys": []string{"100"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreateInstance_DBError_Cluster：ClustersByName.GetByName 报错 → 500。
func TestCreateInstance_DBError_Cluster(t *testing.T) {
	deps, _, _, _ := makeWriteDeps()
	deps.ClustersByName = &fakeClusterByName{err: errors.New("db down")}
	rr := postInstance(t, New(deps), 1, map[string]any{
		"region": "hkg-1", "type": "nano", "image": "ubuntu-24",
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rr.Code)
	}
}
