package openapi

import (
	"bytes"
	"strings"
	"testing"
)

// TestSpec_ContainsCloudGatewayEndpoints PLAN-053 Phase F：grep-level 校验
// embedded openapi.yaml 含 12 个 cloud-gateway /v1 operation。
// 不引 yaml 解析依赖（与 Handler.ServeYAML 保持 zero-dep 一致），
// 只断言关键路径 + tag 存在；结构完整性留给 Swagger UI / openapi-generator-cli 验证。
func TestSpec_ContainsCloudGatewayEndpoints(t *testing.T) {
	if len(specYAML) < 1024 {
		t.Fatal("embedded openapi.yaml too small or missing")
	}
	want := []string{
		"name: cloud-gateway",
		"/v1/account:",
		"/v1/instances:",
		"/v1/instances/{id}:",
		"/v1/instances/{id}/boot:",
		"/v1/instances/{id}/reboot:",
		"/v1/instances/{id}/shutdown:",
		"/v1/types:",
		"/v1/regions:",
		"/v1/images:",
		"/v1/ssh-keys:",
		// schemas
		"StructuredError:",
		"PaginatedResponse:",
		"AccountDTO:",
		"InstanceDTO:",
		"TypeDTO:",
		"RegionDTO:",
		"ImageDTO:",
		"SSHKeyDTO:",
		"InstanceCreateRequest:",
		"InstanceActionResponse:",
		// runway 字段 spec 显式标注
		"estimated_runway_days:",
		// idempotency header
		"V1IdempotencyKey:",
		"Idempotent-Replay:",
	}
	for _, w := range want {
		if !bytes.Contains(specYAML, []byte(w)) {
			t.Errorf("missing %q in embedded openapi.yaml", w)
		}
	}
}

// TestSpec_V1OperationCount 校验 12 个 /v1 operation —— grep "/v1/" 后缀 ":"。
func TestSpec_V1OperationCount(t *testing.T) {
	lines := strings.Split(string(specYAML), "\n")
	v1Paths := 0
	v1Ops := 0
	inV1 := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		// 仅识别顶层 path key（2 空格缩进；与 yaml 结构对齐）。
		if strings.HasPrefix(line, "  /v1/") && strings.HasSuffix(trim, ":") {
			v1Paths++
			inV1 = true
			continue
		}
		// 一旦遇到非 v1 顶层 key 或 components，重置 inV1。
		if strings.HasPrefix(line, "  /") && !strings.HasPrefix(line, "  /v1/") && strings.HasSuffix(trim, ":") {
			inV1 = false
		}
		if strings.HasPrefix(line, "components:") {
			break
		}
		if !inV1 {
			continue
		}
		// 4 空格缩进的 get/post/put/patch/delete: 视为一个 operation
		switch trim {
		case "get:", "post:", "put:", "patch:", "delete:":
			if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") {
				v1Ops++
			}
		}
	}
	if v1Paths != 10 {
		t.Errorf("v1 paths = %d, want 10", v1Paths)
	}
	if v1Ops != 12 {
		t.Errorf("v1 operations = %d, want 12 (5 read + 4 catalog + 3 action + create + delete)", v1Ops)
	}
}
