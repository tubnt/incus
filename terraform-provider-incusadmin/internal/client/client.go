// Package client 是给 Terraform Provider 用的 incus-admin HTTP 客户端。
//
// 设计：
//   - 单一 Client 实例，由 Provider Configure 创建
//   - JSON 请求 / 响应；4xx/5xx 解析 { error: "..." } body
//   - Bearer token 通过 Authorization 头传递
//   - context.Context 一律传递
//
// 不在 .tfstate 持久化 token：用户应在 provider block 用 env var
//
//	INCUSADMIN_TOKEN，或显式 sensitive = true 字段。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	endpoint string
	token    string
	http     *http.Client
}

func New(endpoint, token string) *Client {
	endpoint = strings.TrimRight(endpoint, "/")
	return &Client{
		endpoint: endpoint,
		token:    token,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Do 执行一次 JSON 请求并解码 response 到 out（out=nil 表示忽略 body）。
// path 必须以 /api/... 开头；用 BaseURL 拼接。
func (c *Client) Do(ctx context.Context, method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "terraform-provider-incusadmin/0.1")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode >= 400 {
		var perr struct {
			Error    string `json:"error"`
			Redirect string `json:"redirect,omitempty"`
		}
		_ = json.Unmarshal(respBody, &perr)
		if perr.Error == "step_up_required" {
			return ErrStepUpRequired
		}
		msg := perr.Error
		if msg == "" {
			msg = string(respBody)
		}
		// 返回结构化错误，携带 StatusCode 供上层按状态码判断（如 404 幂等删除），
		// 避免上层用 strings.Contains(err.Error(), "404") 这类脆弱匹配。
		return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Message: msg}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// ErrStepUpRequired 是后端返回 401 + step_up_required 时的标志错误。
// Terraform provider 不应自动重试（不能开浏览器走 OIDC），让用户先在 UI 完成 step-up。
var ErrStepUpRequired = errors.New("step-up authentication required (please re-authenticate via web UI within 5 minutes, then retry)")

// APIError 承载后端非 2xx 响应的结构化信息。上层可用 errors.As 取出 StatusCode
// 做幂等判断（典型场景：Delete 时把 404 视为删除成功）。
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("incus-admin %s %s: %d %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// IsNotFound 判断 err 是否为后端 404（资源已不存在）。
// 用于 Delete 幂等（已不存在=删除成功）与 Read drift 检测（从 state 移除）。
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// ============================================================================
// Resource I/O 类型（与 incus-admin handler DTO 对齐）。
// ============================================================================

type VM struct {
	ID        int64   `json:"id,omitempty"`
	Name      string  `json:"name"`
	ClusterID int64   `json:"cluster_id,omitempty"`
	UserID    int64   `json:"user_id,omitempty"`
	IP        *string `json:"ip,omitempty"`
	Status    string  `json:"status,omitempty"`
	CPU       int     `json:"cpu"`
	MemoryMB  int     `json:"memory_mb"`
	DiskGB    int     `json:"disk_gb"`
	OSImage   string  `json:"os_image"`
	Node      string  `json:"node,omitempty"`
}

type FirewallRule struct {
	Direction       string `json:"direction"`
	Action          string `json:"action"`
	Protocol        string `json:"protocol"`
	DestinationPort string `json:"destination_port"`
	SourceCIDR      string `json:"source_cidr"`
	Description     string `json:"description"`
	SortOrder       int    `json:"sort_order"`
}

type FirewallGroup struct {
	ID          int64          `json:"id,omitempty"`
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Rules       []FirewallRule `json:"rules,omitempty"`
}

// FloatingIP 与后端 model.FloatingIP 的响应键逐字段对齐：
//   - cluster_id：所属 cluster
//   - bound_vm_id：绑定的 VM（后端键为 bound_vm_id，非 vm_id）
type FloatingIP struct {
	ID          int64  `json:"id,omitempty"`
	ClusterID   int64  `json:"cluster_id,omitempty"`
	IP          string `json:"ip,omitempty"`
	BoundVMID   *int64 `json:"bound_vm_id,omitempty"`
	Status      string `json:"status,omitempty"`
	Description string `json:"description,omitempty"`
}

type User struct {
	ID      int64   `json:"id,omitempty"`
	Email   string  `json:"email"`
	Name    string  `json:"name,omitempty"`
	Role    string  `json:"role,omitempty"`
	Balance float64 `json:"balance,omitempty"`
}

type SSHKey struct {
	ID        int64  `json:"id,omitempty"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

type Order struct {
	ID        int64   `json:"id"`
	UserID    int64   `json:"user_id"`
	ProductID int64   `json:"product_id"`
	Status    string  `json:"status"`
	Amount    float64 `json:"amount"`
}

type Invoice struct {
	ID      int64   `json:"id"`
	OrderID int64   `json:"order_id"`
	Amount  float64 `json:"amount"`
	Status  string  `json:"status"`
}
