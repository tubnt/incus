package sshexec

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Runner struct {
	host           string
	user           string
	keyFile        string
	knownHostsFile string
}

func New(host, user, keyFile string) *Runner {
	return &Runner{host: host, user: user, keyFile: keyFile}
}

// WithKnownHosts 指定 OpenSSH known_hosts 文件路径；若文件存在则启用严格主机密钥校验，
// 未命中/被替换时返回错误而非静默放行，防止 MITM。调用方应在启动时注入。
func (r *Runner) WithKnownHosts(file string) *Runner {
	r.knownHostsFile = file
	return r
}

func (r *Runner) Run(ctx context.Context, cmd string) (string, error) {
	key, err := os.ReadFile(r.keyFile)
	if err != nil {
		return "", fmt.Errorf("read key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("parse key: %w", err)
	}

	hostKeyCB, err := r.hostKeyCallback()
	if err != nil {
		return "", fmt.Errorf("host key callback: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            r.user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCB,
		Timeout:         10 * time.Second,
	}

	addr := r.host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = addr + ":22"
	}

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return "", fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	out, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(out), fmt.Errorf("ssh exec: %w", err)
	}
	return string(out), nil
}

// RunArgs 以参数化方式执行远端命令，所有 token 经 POSIX 单引号转义后拼接，
// 避免调用点手写 fmt.Sprintf 造成的命令注入。
func (r *Runner) RunArgs(ctx context.Context, program string, args ...string) (string, error) {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(program))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return r.Run(ctx, strings.Join(parts, " "))
}

// shellQuote 用 POSIX 单引号把任意字符串安全嵌入 shell 命令。
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hostKeyCallback 构造 SSH 主机密钥校验器：
//   - 若注入了 `knownHostsFile` 且文件存在，使用 knownhosts.New 严格校验；
//   - 若 `knownHostsFile` 为空，回退到 `InsecureIgnoreHostKey` 并打 warn 日志，
//     以便在历史部署没有配置该文件时不直接阻塞启动；生产必须显式配置。
func (r *Runner) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if r.knownHostsFile == "" {
		slog.Warn("ssh host key verification disabled (no known_hosts file configured)", "host", r.host)
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if _, err := os.Stat(r.knownHostsFile); err != nil {
		return nil, fmt.Errorf("known_hosts %q not accessible: %w", r.knownHostsFile, err)
	}
	cb, err := knownhosts.New(r.knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return cb, nil
}
