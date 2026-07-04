// Package openapi 暴露 OpenAPI spec + Swagger UI。
//
// PLAN-042 / INFRA-010 一期：
//   - spec 在 docs/openapi/openapi.yaml 维护，build 时 //go:embed 进 binary
//   - GET /api/openapi.json：把 yaml 解析为 json 返回（缓存解析结果）
//   - GET /api/openapi.yaml：原样返回 yaml
//   - GET /api/docs / /api/docs/：Swagger UI（CDN 引用，零静态依赖）
//
// 全量 swag v1 注释 80 endpoint 通过 OPS 子任务增量推进；本 phase A 先把
// 核心资源覆盖以让客户端 SDK 能 generate。
package openapi

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed openapi.yaml
var specYAML []byte

// 简单的 yaml→json 转换：用 stdlib json + 一次启动期解析。
//
// 实际上这里嵌入的 yaml 是手写 spec；我们不在 runtime 解析 yaml（避免引入
// yaml.v3 依赖）。改为：embed 同时维护一份 openapi.json（同源生成），
// 启动时直接 serve。简化方案：仅发布 .yaml 给人读、.json 给 client gen 时
// 在 build 中转换。本 handler 只 serve yaml；JSON 转换交给客户端工具链或
// Swagger UI（它支持 yaml 直接渲染）。
//
// Decision: 不内嵌 yaml→json 转换，省一个依赖；Swagger UI 的 url 指向 .yaml 即可。

type Handler struct {
	yaml []byte
}

func NewHandler() *Handler {
	return &Handler{yaml: specYAML}
}

// Routes 挂在 chi.Router 下（不属于 portal/admin，外层 server.go 单挂）。
func (h *Handler) ServeYAML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(h.yaml)
}

// ServeJSON 是 placeholder：当前阶段不在 server-side 转 yaml→json（避免 yaml
// 依赖）。客户端工具链（openapi-generator）可直接吃 yaml；想要 JSON 的用户
// 用 `yq -o=json` 转即可。返 200 + 提示文本，便于发现路径正确但未启用。
//
// 后续 OPS 可加 yaml.v3 依赖把这块切到真转换。
func (h *Handler) ServeJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"hint": "openapi spec 当前以 YAML 维护；请改用 /api/openapi.yaml。客户端 SDK 生成器（openapi-generator-cli / oapi-codegen）原生支持 yaml 输入。",
		"yaml_url": "/api/openapi.yaml",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ServeUI 渲染 Swagger UI（CDN 引用）。
//
// 不内嵌 swagger-ui dist（≈ 4MB，无意义膨胀）；用 unpkg.com 公共 CDN。
// 内部部署可改 url 指向私有镜像。
func (h *Handler) ServeUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}

const swaggerUIHTML = `<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8" />
<title>incus-admin API · Swagger UI</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui.css" />
<style>html,body{margin:0;padding:0;background:#0f1011;}#swagger-ui{max-width:1200px;margin:0 auto;padding:24px;}</style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui-bundle.js"></script>
<script src="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui-standalone-preset.js"></script>
<script>
window.onload = function() {
  window.ui = SwaggerUIBundle({
    url: "/api/openapi.yaml",
    dom_id: "#swagger-ui",
    deepLinking: true,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl],
    layout: "StandaloneLayout",
    persistAuthorization: true,
  });
};
</script>
</body>
</html>`
