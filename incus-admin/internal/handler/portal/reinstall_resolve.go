package portal

import (
	"context"
	"fmt"
	"strings"

	"github.com/incuscloud/incus-admin/internal/service"
)

// resolveReinstallTemplate turns a {template_slug, os_image} pair into the
// four fields service.ReinstallParams needs. Call it with empty strings and it
// returns an error so callers can refuse requests that didn't pick anything.
//
// Precedence:
//  1. template_slug → DB lookup (authoritative)
//  2. os_image (legacy wire, stripped of "images:" prefix) with heuristics
//     for default_user based on the distro prefix.
func resolveReinstallTemplate(ctx context.Context, templateSlug, osImage string) (service.ReinstallParams, error) {
	params := service.ReinstallParams{}

	if templateSlug != "" && osTemplateRepo != nil {
		tpl, err := osTemplateRepo.GetBySlug(ctx, templateSlug)
		if err != nil {
			return params, fmt.Errorf("lookup template: %w", err)
		}
		if tpl == nil {
			return params, fmt.Errorf("template %q not found", templateSlug)
		}
		if !tpl.Enabled {
			return params, fmt.Errorf("template %q is disabled", templateSlug)
		}
		params.ImageSource = tpl.Source
		params.ServerURL = tpl.ServerURL
		params.Protocol = tpl.Protocol
		params.DefaultUser = tpl.DefaultUser
		return params, nil
	}

	if osImage == "" {
		return params, fmt.Errorf("template_slug or os_image is required")
	}
	source := strings.TrimPrefix(osImage, "images:")
	params.ImageSource = source
	params.DefaultUser = defaultUserForSource(source)
	return params, nil
}

// defaultUserForSource：legacy os_image 路径的默认登录用户兜底。OPS-051 /
// PLAN-052 Q7 决策：所有 Linux 镜像统一 root；Windows 仍 Administrator。
// 实际生产路径都走 template_slug → DB.default_user（已 migration 改 root）。
func defaultUserForSource(source string) string {
	s := strings.ToLower(source)
	if strings.Contains(s, "windows") {
		return "Administrator"
	}
	return "root"
}
