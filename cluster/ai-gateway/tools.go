package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
)

// ToolDefs 返回所有 Claude tool 定义
func ToolDefs() []anthropic.ToolUnionParam {
	return []anthropic.ToolUnionParam{
		toolDef("list_vms", "列出当前用户的所有虚拟机，返回名称、状态、配置等信息", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		toolDef("create_vm", "创建一台新的虚拟机", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "虚拟机名称，仅允许小写字母、数字和短横线",
				},
				"cpu": map[string]any{
					"type":        "integer",
					"description": "CPU 核心数（1-16）",
				},
				"memory_gb": map[string]any{
					"type":        "integer",
					"description": "内存大小（GB，1-64）",
				},
				"disk_gb": map[string]any{
					"type":        "integer",
					"description": "系统盘大小（GB，20-500）",
				},
				"os": map[string]any{
					"type":        "string",
					"description": "操作系统镜像，如 ubuntu-24.04, debian-12, rocky-9",
				},
			},
			"required": []string{"name", "cpu", "memory_gb", "disk_gb", "os"},
		}),
		toolDef("delete_vm", "删除指定虚拟机（危险操作，需用户确认）", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "虚拟机名称",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "必须为 true 才能执行删除",
				},
			},
			"required": []string{"name", "confirm"},
		}),
		toolDef("start_vm", "启动指定虚拟机", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "虚拟机名称",
				},
			},
			"required": []string{"name"},
		}),
		toolDef("stop_vm", "停止指定虚拟机", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "虚拟机名称",
				},
			},
			"required": []string{"name"},
		}),
		toolDef("resize_vm", "调整虚拟机配置（升配不停机，降配需停机）", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "虚拟机名称",
				},
				"cpu": map[string]any{
					"type":        "integer",
					"description": "新的 CPU 核心数",
				},
				"memory_gb": map[string]any{
					"type":        "integer",
					"description": "新的内存大小（GB）",
				},
			},
			"required": []string{"name"},
		}),
		toolDef("get_metrics", "获取虚拟机的实时监控指标（CPU、内存、磁盘、网络）", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "虚拟机名称",
				},
			},
			"required": []string{"name"},
		}),
		toolDef("manage_firewall", "管理虚拟机防火墙规则", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "虚拟机名称",
				},
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "add", "remove"},
					"description": "操作类型：list=查看规则, add=添加规则, remove=删除规则",
				},
				"rule": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"direction": map[string]any{
							"type": "string",
							"enum": []string{"ingress", "egress"},
						},
						"protocol": map[string]any{
							"type": "string",
							"enum": []string{"tcp", "udp", "icmp"},
						},
						"port": map[string]any{
							"type":        "string",
							"description": "端口号或范围，如 80 或 8000-9000",
						},
						"source": map[string]any{
							"type":        "string",
							"description": "来源 IP/CIDR，如 0.0.0.0/0",
						},
						"action": map[string]any{
							"type": "string",
							"enum": []string{"allow", "deny"},
						},
					},
					"description": "防火墙规则（add/remove 时必填）",
				},
				"rule_id": map[string]any{
					"type":        "string",
					"description": "规则 ID（remove 时使用）",
				},
			},
			"required": []string{"name", "action"},
		}),
		toolDef("create_snapshot", "为虚拟机创建快照", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "虚拟机名称",
				},
				"snapshot_name": map[string]any{
					"type":        "string",
					"description": "快照名称",
				},
			},
			"required": []string{"name", "snapshot_name"},
		}),
	}
}

func toolDef(name, desc string, schema map[string]any) anthropic.ToolUnionParam {
	inputSchema := anthropic.ToolInputSchemaParam{
		Properties: schema["properties"].(map[string]any),
	}
	if req, ok := schema["required"]; ok {
		inputSchema.ExtraFields = map[string]any{"required": req}
	}
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        name,
			Description: anthropic.String(desc),
			InputSchema: inputSchema,
		},
	}
}

// ToolExecutor 执行 tool 调用，向 Extension API 发请求
type ToolExecutor struct {
	ExtensionURL string
	HTTPClient   *http.Client
}

// Execute 根据 tool 名称执行对应操作
func (e *ToolExecutor) Execute(userID, toolName string, input json.RawMessage) (string, error) {
	switch toolName {
	case "list_vms":
		return e.callExtension("GET", fmt.Sprintf("/vms?user_id=%s", userID), nil)
	case "create_vm":
		return e.callExtension("POST", "/vms", withUserID(userID, input))
	case "delete_vm":
		var p struct {
			Name    string `json:"name"`
			Confirm bool   `json:"confirm"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("参数解析失败: %w", err)
		}
		if !p.Confirm {
			return `{"error": "删除操作需要 confirm=true 确认"}`, nil
		}
		return e.callExtension("DELETE", fmt.Sprintf("/vms/%s?user_id=%s", p.Name, userID), nil)
	case "start_vm":
		return e.vmAction(userID, input, "start")
	case "stop_vm":
		return e.vmAction(userID, input, "stop")
	case "resize_vm":
		return e.callExtensionWithName(userID, input, "resize", "POST")
	case "get_metrics":
		return e.callExtensionWithName(userID, input, "metrics", "GET")
	case "manage_firewall":
		return e.handleFirewall(userID, input)
	case "create_snapshot":
		return e.callExtensionWithName(userID, input, "snapshots", "POST")
	default:
		return "", fmt.Errorf("未知 tool: %s", toolName)
	}
}

func (e *ToolExecutor) vmAction(userID string, input json.RawMessage, action string) (string, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]string{"action": action, "user_id": userID})
	return e.callExtension("POST", fmt.Sprintf("/vms/%s/state", p.Name), body)
}

func (e *ToolExecutor) callExtensionWithName(userID string, input json.RawMessage, endpoint, method string) (string, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	url := fmt.Sprintf("/vms/%s/%s?user_id=%s", p.Name, endpoint, userID)
	if method == "GET" {
		return e.callExtension(method, url, nil)
	}
	return e.callExtension(method, url, withUserID(userID, input))
}

func (e *ToolExecutor) handleFirewall(userID string, input json.RawMessage) (string, error) {
	var p struct {
		Name   string `json:"name"`
		Action string `json:"action"`
		RuleID string `json:"rule_id"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	base := fmt.Sprintf("/vms/%s/firewall?user_id=%s", p.Name, userID)
	switch p.Action {
	case "list":
		return e.callExtension("GET", base, nil)
	case "add":
		return e.callExtension("POST", base, input)
	case "remove":
		return e.callExtension("DELETE", fmt.Sprintf("/vms/%s/firewall/%s?user_id=%s", p.Name, p.RuleID, userID), nil)
	default:
		return "", fmt.Errorf("未知防火墙操作: %s", p.Action)
	}
}

func (e *ToolExecutor) callExtension(method, path string, body []byte) (string, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.ExtensionURL+path, reqBody)
	if err != nil {
		return "", fmt.Errorf("构建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用 Extension API 失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Extension API 返回错误 %d: %s", resp.StatusCode, string(data))
	}
	return string(data), nil
}

func withUserID(userID string, input json.RawMessage) []byte {
	var m map[string]any
	json.Unmarshal(input, &m)
	m["user_id"] = userID
	b, _ := json.Marshal(m)
	return b
}
