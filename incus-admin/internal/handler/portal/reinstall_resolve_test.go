package portal

import (
	"context"
	"testing"
)

// OPS-051 / PLAN-052 Q7：所有 Linux 镜像统一 root 登录，仅 Windows 仍
// Administrator。本测试与 web/src/features/vms/default-user.test.ts 同步。
func TestResolveReinstallTemplate(t *testing.T) {
	tests := []struct {
		name     string
		slug     string
		osImage  string
		wantErr  bool
		wantSrc  string
		wantUser string
	}{
		{
			name:    "empty slug and image returns error",
			wantErr: true,
		},
		{
			name:     "legacy images: prefix stripped, root login",
			osImage:  "images:ubuntu/24.04/cloud",
			wantSrc:  "ubuntu/24.04/cloud",
			wantUser: "root",
		},
		{
			name:     "debian source: root login",
			osImage:  "images:debian/12/cloud",
			wantSrc:  "debian/12/cloud",
			wantUser: "root",
		},
		{
			name:     "windows local alias keeps Administrator",
			osImage:  "windows-server-2022",
			wantSrc:  "windows-server-2022",
			wantUser: "Administrator",
		},
		{
			name:     "raw source without images: prefix passes through",
			osImage:  "fedora/40/cloud",
			wantSrc:  "fedora/40/cloud",
			wantUser: "root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveReinstallTemplate(context.Background(), tt.slug, tt.osImage)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; params=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ImageSource != tt.wantSrc {
				t.Fatalf("ImageSource: want %q, got %q", tt.wantSrc, got.ImageSource)
			}
			if got.DefaultUser != tt.wantUser {
				t.Fatalf("DefaultUser: want %q, got %q", tt.wantUser, got.DefaultUser)
			}
		})
	}
}

// OPS-051 / PLAN-052 Q7：defaultUserForSource 简化为 root/Administrator 二分。
func TestDefaultUserForSource(t *testing.T) {
	cases := map[string]string{
		"ubuntu/24.04/cloud":      "root",
		"UBUNTU/22.04/cloud":      "root",
		"debian/12/cloud":         "root",
		"rockylinux/9/cloud":      "root",
		"fedora/40/cloud":         "root",
		"alpine/3.19/cloud":       "root",
		"unknown/something":       "root",
		"windows-server-2022":     "Administrator",
		"windows-11":              "Administrator",
	}
	for src, wantUser := range cases {
		if got := defaultUserForSource(src); got != wantUser {
			t.Errorf("src=%q: want %q, got %q", src, wantUser, got)
		}
	}
}
