package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIsNotFound 校验结构化错误的状态码判断（T7 幂等删除依赖）。
func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"404 APIError", &APIError{StatusCode: http.StatusNotFound}, true},
		{"409 APIError", &APIError{StatusCode: http.StatusConflict}, false},
		{"wrapped 404", errWrap(&APIError{StatusCode: http.StatusNotFound}), true},
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsNotFound(c.err); got != c.want {
				t.Fatalf("IsNotFound(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func errWrap(err error) error { return errWrapper{err} }

type errWrapper struct{ err error }

func (e errWrapper) Error() string { return "wrap: " + e.err.Error() }
func (e errWrapper) Unwrap() error { return e.err }

// TestDoReturnsStructuredStatus 校验 Do 把非 2xx 响应转成携带 StatusCode 的 *APIError。
func TestDoReturnsStructuredStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"floating_ip not found"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	err := c.Do(context.Background(), "DELETE", "/api/admin/floating-ips/1", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
	if !IsNotFound(err) {
		t.Fatal("IsNotFound should be true for 404")
	}
}

// TestStepUpErrorPreserved 校验 step_up_required 仍映射到标志错误（非 APIError）。
func TestStepUpErrorPreserved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"step_up_required"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	err := c.Do(context.Background(), "POST", "/api/admin/floating-ips", nil, nil)
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("expected ErrStepUpRequired, got %v", err)
	}
}

// TestFloatingIPBackendKeys 锁定 client.FloatingIP 与后端 model.FloatingIP 的响应键对齐：
// 后端绑定字段为 bound_vm_id（非 vm_id），并含 cluster_id。
func TestFloatingIPBackendKeys(t *testing.T) {
	payload := `{"id":7,"cluster_id":3,"ip":"203.0.113.9","bound_vm_id":42,"status":"attached","description":"web"}`
	var f FloatingIP
	if err := json.Unmarshal([]byte(payload), &f); err != nil {
		t.Fatal(err)
	}
	if f.ID != 7 || f.ClusterID != 3 || f.IP != "203.0.113.9" || f.Status != "attached" || f.Description != "web" {
		t.Fatalf("unexpected decode: %+v", f)
	}
	if f.BoundVMID == nil || *f.BoundVMID != 42 {
		t.Fatalf("bound_vm_id not decoded: %+v", f.BoundVMID)
	}
}

// TestVMDBKeys 锁定 client.VM 能解析后端 Read 的 "db" 行结构。
func TestVMDBKeys(t *testing.T) {
	payload := `{"db":{"id":11,"name":"vm-abc123","cluster_id":1,"user_id":2,"ip":"10.0.0.5","status":"running","cpu":4,"memory_mb":8192,"disk_gb":80,"os_image":"ubuntu-22.04","node":"node3"}}`
	var out struct {
		DB *VM `json:"db"`
	}
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		t.Fatal(err)
	}
	if out.DB == nil {
		t.Fatal("db not decoded")
	}
	db := out.DB
	if db.ID != 11 || db.Name != "vm-abc123" || db.CPU != 4 || db.MemoryMB != 8192 || db.DiskGB != 80 ||
		db.OSImage != "ubuntu-22.04" || db.Status != "running" || db.Node != "node3" {
		t.Fatalf("unexpected decode: %+v", db)
	}
	if db.IP == nil || *db.IP != "10.0.0.5" {
		t.Fatalf("ip not decoded: %+v", db.IP)
	}
}

// TestVMCreateResponseKeys 锁定 VM Create 202 响应键 { status, job_id, vm_id, vm_name, ip }。
func TestVMCreateResponseKeys(t *testing.T) {
	payload := `{"status":"provisioning","job_id":99,"vm_id":11,"vm_name":"vm-abc123","ip":"10.0.0.5"}`
	var out struct {
		Status string `json:"status"`
		JobID  int64  `json:"job_id"`
		VMID   int64  `json:"vm_id"`
		VMName string `json:"vm_name"`
		IP     string `json:"ip"`
	}
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "provisioning" || out.JobID != 99 || out.VMID != 11 || out.VMName != "vm-abc123" || out.IP != "10.0.0.5" {
		t.Fatalf("unexpected decode: %+v", out)
	}
}

// TestSSHKeyBackendKeys 锁定 ssh_key 单条 "key" / 列表 "keys" 响应键。
func TestSSHKeyBackendKeys(t *testing.T) {
	single := `{"key":{"id":5,"name":"laptop","public_key":"ssh-ed25519 AAAA laptop"}}`
	var one struct {
		Key SSHKey `json:"key"`
	}
	if err := json.Unmarshal([]byte(single), &one); err != nil {
		t.Fatal(err)
	}
	if one.Key.ID != 5 || one.Key.Name != "laptop" || one.Key.PublicKey != "ssh-ed25519 AAAA laptop" {
		t.Fatalf("unexpected single decode: %+v", one.Key)
	}

	list := `{"keys":[{"id":5,"name":"laptop","public_key":"ssh-ed25519 AAAA laptop"}]}`
	var many struct {
		Keys []SSHKey `json:"keys"`
	}
	if err := json.Unmarshal([]byte(list), &many); err != nil {
		t.Fatal(err)
	}
	if len(many.Keys) != 1 || many.Keys[0].ID != 5 {
		t.Fatalf("unexpected list decode: %+v", many.Keys)
	}
}
