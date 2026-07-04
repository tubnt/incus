package service

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsValidLinuxUsername(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"ubuntu", true},
		{"user_01", true},
		{"_system", true},
		{"a", true},
		{"", false},
		{"0user", false},
		{"-user", false},
		{"USER", false},
		{"user name", false},
		{"user;rm", false},
		{"user'x", false},
		{"user`x", false},
		{"user$x", false},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false}, // 33 chars
	}
	for _, c := range cases {
		if got := isValidLinuxUsername(c.in); got != c.want {
			t.Errorf("isValidLinuxUsername(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestShellSingleQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ubuntu", "'ubuntu'"},
		{"a b c", "'a b c'"},
		{"it's", `'it'\''s'`},
		{"''", `''\'''\'''`},
		{"", "''"},
		{`$(whoami)`, `'$(whoami)'`},
	}
	for _, c := range cases {
		if got := shellSingleQuote(c.in); got != c.want {
			t.Errorf("shellSingleQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOfflineChpasswdCloudConfig pins the YAML shape the offline reset path
// injects into cloud-init.vendor-data. chpasswd.users[] + ssh_pwauth is what
// cloud-init's set_passwords module consumes; the shape rarely changes, so
// diffs on this test signal intent shifts (e.g. switching to a hashed form).
func TestOfflineChpasswdCloudConfig(t *testing.T) {
	got := offlineChpasswdCloudConfig("ubuntu", "deadbeef")

	wantFragments := []string{
		"#cloud-config",
		"chpasswd:",
		"expire: false",
		`name: "ubuntu"`,
		`password: "deadbeef"`,
		"type: text",
		"ssh_pwauth: true",
	}
	for _, frag := range wantFragments {
		if !strings.Contains(got, frag) {
			t.Errorf("cloud-config missing %q:\n---\n%s---", frag, got)
		}
	}
	// Guard against accidentally leaving a runcmd echo that would log the
	// password to /var/log/cloud-init.log via shell command tracing.
	if strings.Contains(got, "runcmd") || strings.Contains(got, "chpasswd |") {
		t.Errorf("cloud-config should not contain runcmd/chpasswd pipe:\n%s", got)
	}
}

// TestNewInstanceID confirms the ID is unique-ish (two calls differ) and uses
// the documented "iid-" prefix. A collision here would silently skip
// cloud-init re-run and the offline reset would appear to "succeed" without
// actually rotating the password.
func TestNewInstanceID(t *testing.T) {
	a := newInstanceID()
	b := newInstanceID()
	if a == b {
		t.Fatalf("newInstanceID produced identical IDs: %q", a)
	}
	for _, id := range []string{a, b} {
		if !strings.HasPrefix(id, "iid-") {
			t.Errorf("id %q missing iid- prefix", id)
		}
		if len(id) != len("iid-")+16 {
			t.Errorf("id %q wrong length (want iid- + 16 hex chars)", id)
		}
	}
}

// TestStripVolatileConfig protects against the Incus PUT-instance bug we
// hit on 2026-04-25: vm-8f8912 offline reset returned 500 because the GET
// body included `volatile.eth0.host_name` and PUT-ing it back is forbidden.
// Any future caller of GET-instance → modify → PUT-instance MUST strip
// volatile.* before marshal; this test pins the helper's contract.
func TestStripVolatileConfig(t *testing.T) {
	cfg := map[string]string{
		"limits.cpu":              "1",
		"user.cloud-init":         "#cloud-config\n",
		"cloud-init.instance-id":  "iid-abc",
		"volatile.eth0.host_name": "vethXXXX",
		"volatile.uuid":           "00000000-0000-0000-0000-000000000000",
		"volatile.last_state.power":     "RUNNING",
		"volatile.cloud-init.instance-id": "iid-prev",
	}
	stripVolatileConfig(cfg)
	for k := range cfg {
		if strings.HasPrefix(k, "volatile.") {
			t.Errorf("volatile key %q survived strip", k)
		}
	}
	for _, must := range []string{"limits.cpu", "user.cloud-init", "cloud-init.instance-id"} {
		if _, ok := cfg[must]; !ok {
			t.Errorf("non-volatile key %q was wrongly removed", must)
		}
	}
}

// TestResetPasswordModeConstants pins the wire-level mode strings used by
// handlers. Changing any of these is a breaking API change for the portal /
// admin reset endpoints; the test makes that explicit.
func TestProbeImageServer(t *testing.T) {
	// Empty serverURL is allowed (caller passes default upstream).
	if err := probeImageServer(context.Background(), ""); err != nil {
		t.Fatalf("empty url should not error, got %v", err)
	}

	// Server returns 200 → reachable.
	srvOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srvOK.Close()
	if err := probeImageServer(context.Background(), srvOK.URL); err != nil {
		t.Fatalf("expected reachable, got %v", err)
	}

	// Server returns 405 (HEAD not allowed by some CDNs) → still treated as reachable.
	srv405 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv405.Close()
	if err := probeImageServer(context.Background(), srv405.URL); err != nil {
		t.Fatalf("405 should be treated as reachable, got %v", err)
	}

	// Server returns 503 → not reachable, must error.
	srv503 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv503.Close()
	if err := probeImageServer(context.Background(), srv503.URL); err == nil {
		t.Fatal("expected error for 503, got nil")
	}

	// Unreachable URL must error.
	if err := probeImageServer(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Fatal("expected error for unreachable URL, got nil")
	}
}

// TestClassifyOSFamily 验证 image alias / template source 到 OS family 的映射。
// 任何被分类到 unknown 的 alias 会回退 apt 模板（与 BuildCloudInit fallback 一致），
// 但单测覆盖明示意图比"无声 fallback"更稳。OPS-051 / PLAN-052。
func TestClassifyOSFamily(t *testing.T) {
	cases := []struct {
		in   string
		want OSFamily
	}{
		{"ubuntu/24.04/cloud", OSFamilyAPT},
		{"images:ubuntu/22.04/cloud", OSFamilyAPT},
		{"debian/12/cloud", OSFamilyAPT},
		{"rockylinux/9/cloud", OSFamilyDNF},
		{"almalinux/9/cloud", OSFamilyDNF},
		{"fedora/40/cloud", OSFamilyDNF},
		{"archlinux/current/cloud", OSFamilyPacman},
		{"alpine/3.20/cloud", OSFamilyAlpine},
		{"windows-server-2022", OSFamilyUnknown}, // Windows 不走 BuildCloudInit；这里 unknown 是正确的
		{"", OSFamilyUnknown},
	}
	for _, c := range cases {
		if got := ClassifyOSFamily(c.in); got != c.want {
			t.Errorf("ClassifyOSFamily(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestBuildCloudInit_AllFamilies 锁定 BuildCloudInit 在 4 个 OS family
// 上的关键 invariant：必有 openssh-server / qemu-guest-agent / runcmd 启用 sshd /
// chpasswd users[] / sshd_config drop-in PermitRootLogin yes。OPS-051 / PLAN-052。
func TestBuildCloudInit_AllFamilies(t *testing.T) {
	families := []struct {
		family   OSFamily
		sshPkg   string // 期望的 openssh 包名
		sshd     string // 期望的 systemd 服务名
		extraPkg string // 自动补丁包（如 unattended-upgrades / dnf-automatic）
	}{
		{OSFamilyAPT, "openssh-server", "ssh", "unattended-upgrades"},
		{OSFamilyDNF, "openssh-server", "sshd", "dnf-automatic"},
		{OSFamilyPacman, "openssh", "sshd", ""},
		{OSFamilyAlpine, "openssh-server", "sshd", ""},
	}
	for _, f := range families {
		ci := BuildCloudInit(CloudInitInput{
			OSFamily:    f.family,
			LoginUser:   "root",
			Password:    "deadbeef",
			AptProxyURL: "http://10.0.0.1:3142/",
		})
		// 必装包
		if !strings.Contains(ci, "- "+f.sshPkg+"\n") {
			t.Errorf("%s: missing package %q in output:\n%s", f.family, f.sshPkg, ci)
		}
		if !strings.Contains(ci, "- qemu-guest-agent\n") {
			t.Errorf("%s: missing qemu-guest-agent", f.family)
		}
		if f.extraPkg != "" && !strings.Contains(ci, "- "+f.extraPkg+"\n") {
			t.Errorf("%s: missing %q", f.family, f.extraPkg)
		}
		// 必启 sshd
		if !strings.Contains(ci, "systemctl enable --now "+f.sshd+".service") {
			t.Errorf("%s: missing systemctl enable %s", f.family, f.sshd)
		}
		// chpasswd users[root]
		if !strings.Contains(ci, `name: "root"`) || !strings.Contains(ci, `password: "deadbeef"`) {
			t.Errorf("%s: missing chpasswd root entry:\n%s", f.family, ci)
		}
		// sshd drop-in
		if !strings.Contains(ci, "PermitRootLogin yes") {
			t.Errorf("%s: missing PermitRootLogin drop-in", f.family)
		}
		// users.root
		if !strings.Contains(ci, "  - name: root\n") {
			t.Errorf("%s: missing users.root:\n%s", f.family, ci)
		}
		// apt proxy 仅 apt 家族
		if f.family == OSFamilyAPT {
			if !strings.Contains(ci, "http_proxy: http://10.0.0.1:3142/") {
				t.Errorf("apt: missing proxy injection")
			}
		} else {
			if strings.Contains(ci, "http_proxy:") {
				t.Errorf("%s: non-apt family must not get apt proxy", f.family)
			}
		}
		// 头一行必为 #cloud-config
		if !strings.HasPrefix(ci, "#cloud-config\n") {
			t.Errorf("%s: output must start with #cloud-config, got %.40q", f.family, ci)
		}
	}
}

// TestBuildCloudInit_SSHKeys 验证 SSHKeys 注入到 users.root.ssh_authorized_keys。
func TestBuildCloudInit_SSHKeys(t *testing.T) {
	ci := BuildCloudInit(CloudInitInput{
		OSFamily: OSFamilyAPT,
		Password: "x",
		SSHKeys:  []string{"ssh-ed25519 AAAA test1@host", "ssh-rsa BBBB test2@host"},
	})
	for _, want := range []string{
		"ssh_authorized_keys:",
		"      - ssh-ed25519 AAAA test1@host",
		"      - ssh-rsa BBBB test2@host",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("missing %q in:\n%s", want, ci)
		}
	}
}

// TestBuildCloudInit_NoAptProxy 当 AptProxyURL 为空时不应注入 apt: 字段，且
// runcmd 中不应有 acng healthcheck（避免 5s 无谓延迟）。
func TestBuildCloudInit_NoAptProxy(t *testing.T) {
	ci := BuildCloudInit(CloudInitInput{
		OSFamily: OSFamilyAPT,
		Password: "x",
	})
	if strings.Contains(ci, "http_proxy:") {
		t.Errorf("AptProxyURL='' but proxy still injected:\n%s", ci)
	}
	if strings.Contains(ci, "acng-report.html") {
		t.Errorf("AptProxyURL='' but acng healthcheck still in runcmd")
	}
}

// TestBuildCloudInit_ExtraYAML 验证 os_templates.cloud_init_template 中追加
// 字段被真正合并到 base（list append，不冲突）。OPS-051 测试发现纯字符串
// 拼接遇到同名 list key（write_files）会让 cloud-init YAML 解析失败，改
// yaml.v3 merge。
func TestBuildCloudInit_ExtraYAML(t *testing.T) {
	extra := "write_files:\n  - path: /etc/motd\n    content: hello-ai\n"
	ci := BuildCloudInit(CloudInitInput{
		OSFamily:  OSFamilyAPT,
		Password:  "x",
		ExtraYAML: extra,
	})
	// extra write_files 条目必须出现
	if !strings.Contains(ci, "/etc/motd") || !strings.Contains(ci, "hello-ai") {
		t.Errorf("extra write_files entry missing:\n%s", ci)
	}
	// 系统 write_files（sshd drop-in）必须仍在 —— list 应已 append
	if !strings.Contains(ci, "99-incusadmin.conf") {
		t.Errorf("system sshd drop-in stripped by extra merge:\n%s", ci)
	}
	// 系统 packages 必须仍在
	if !strings.Contains(ci, "openssh-server") {
		t.Errorf("system openssh-server stripped by extra merge")
	}
	// 输出必须是合法 cloud-config（不重复 mapping key）
	if !strings.HasPrefix(ci, "#cloud-config") {
		t.Errorf("output missing #cloud-config header")
	}
}

// TestBuildCloudInit_UserDataCloudConfig 验证 WP-I1：用户 user_data 为
// #cloud-config 时走 append 合并，既保留 OS-aware 基础段又并入用户字段。
func TestBuildCloudInit_UserDataCloudConfig(t *testing.T) {
	ud := "#cloud-config\nruncmd:\n  - echo user-hello\npackages:\n  - htop\n"
	ci := BuildCloudInit(CloudInitInput{
		OSFamily: OSFamilyAPT,
		Password: "x",
		UserData: ud,
	})
	// 用户字段必须出现
	if !strings.Contains(ci, "echo user-hello") || !strings.Contains(ci, "htop") {
		t.Errorf("user_data fields missing:\n%s", ci)
	}
	// 系统基础段（sshd drop-in / openssh-server）必须仍在 —— 未被覆盖
	if !strings.Contains(ci, "99-incusadmin.conf") {
		t.Errorf("system sshd drop-in stripped by user_data merge:\n%s", ci)
	}
	if !strings.Contains(ci, "openssh-server") {
		t.Errorf("system openssh-server stripped by user_data merge")
	}
	if !strings.HasPrefix(ci, "#cloud-config") {
		t.Errorf("output missing #cloud-config header")
	}
}

// TestBuildCloudInit_UserDataScriptMIME 验证 WP-I1：user_data 为非 cloud-config
// （shell 脚本）时走 multipart MIME，base 配置与用户脚本都被保留，不静默丢弃。
func TestBuildCloudInit_UserDataScriptMIME(t *testing.T) {
	ud := "#!/bin/bash\necho custom-script > /tmp/marker\n"
	ci := BuildCloudInit(CloudInitInput{
		OSFamily: OSFamilyAPT,
		Password: "x",
		UserData: ud,
	})
	if !strings.Contains(ci, "multipart/mixed") {
		t.Errorf("expected multipart MIME wrapper for shell-script user_data:\n%s", ci)
	}
	// base cloud-config 段（openssh-server）与用户脚本必须都在
	if !strings.Contains(ci, "openssh-server") {
		t.Errorf("base cloud-config dropped in MIME output")
	}
	if !strings.Contains(ci, "echo custom-script") {
		t.Errorf("user shell-script dropped in MIME output:\n%s", ci)
	}
	if !strings.Contains(ci, "text/x-shellscript") {
		t.Errorf("shell-script part not typed as x-shellscript:\n%s", ci)
	}
}

// TestBuildCloudInit_SpecialCharPasswordSafe 验证 WP-I1：用户指定的 root_pass
// 含 shell/YAML 特殊字符（' $ 反引号）时，runcmd chpasswd 兜底走 base64 编码，
// 不把原始特殊字符裸露进单引号 YAML scalar（否则破坏 cloud-init 解析或设错密码）。
func TestBuildCloudInit_SpecialCharPasswordSafe(t *testing.T) {
	pw := "a'b$c`d"
	ci := BuildCloudInit(CloudInitInput{
		OSFamily: OSFamilyAPT,
		Password: pw,
	})
	// runcmd chpasswd 兜底必须走 base64 -d 管道
	if !strings.Contains(ci, "base64 -d | chpasswd") {
		t.Errorf("chpasswd fallback not base64-encoded:\n%s", ci)
	}
	// 原始密码不得出现在 runcmd 的单引号 echo 里（会破坏 YAML/shell）
	if strings.Contains(ci, "echo \"root:a'b$c`d\"") || strings.Contains(ci, "'echo \"root:"+pw) {
		t.Errorf("raw special-char password leaked into runcmd echo:\n%s", ci)
	}
	// base64(root:a'b$c`d) 必须出现，确认凭据确实透传
	want := base64.StdEncoding.EncodeToString([]byte("root:" + pw))
	if !strings.Contains(ci, want) {
		t.Errorf("expected base64 cred %q in runcmd:\n%s", want, ci)
	}
}

// TestBuildCloudInit_NoUserData 无 user_data 时输出与既有基础配置一致（回归保护）。
func TestBuildCloudInit_NoUserData(t *testing.T) {
	ci := BuildCloudInit(CloudInitInput{OSFamily: OSFamilyAPT, Password: "x"})
	if !strings.HasPrefix(ci, "#cloud-config") {
		t.Errorf("output missing #cloud-config header")
	}
	if strings.Contains(ci, "multipart/mixed") {
		t.Errorf("unexpected MIME wrapper without user_data")
	}
}

// TestBuildCloudInit_NonRootLoginUser 当 LoginUser != root 时应注入 sudo
// NOPASSWD（与 cloud-init 镜像默认行为对齐）。
func TestBuildCloudInit_NonRootLoginUser(t *testing.T) {
	ci := BuildCloudInit(CloudInitInput{
		OSFamily:  OSFamilyAPT,
		LoginUser: "ubuntu",
		Password:  "x",
	})
	if !strings.Contains(ci, "sudo: ALL=(ALL) NOPASSWD:ALL") {
		t.Errorf("missing sudo NOPASSWD for non-root user:\n%s", ci)
	}
	if !strings.Contains(ci, `name: "ubuntu"`) {
		t.Errorf("missing chpasswd users.ubuntu")
	}
}

func TestResetPasswordModeConstants(t *testing.T) {
	cases := []struct {
		mode ResetPasswordMode
		want string
	}{
		{ResetPasswordAuto, "auto"},
		{ResetPasswordOnline, "online"},
		{ResetPasswordOffline, "offline"},
	}
	for _, c := range cases {
		if string(c.mode) != c.want {
			t.Errorf("mode constant mismatch: got %q, want %q", c.mode, c.want)
		}
	}
}
