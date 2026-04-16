package sshexec

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

type Runner struct {
	host    string
	user    string
	keyFile string
}

func New(host, user, keyFile string) *Runner {
	return &Runner{host: host, user: user, keyFile: keyFile}
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

	config := &ssh.ClientConfig{
		User:            r.user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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
