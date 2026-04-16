package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/model"
)

type VMService struct {
	clusters *cluster.Manager
}

func NewVMService(clusters *cluster.Manager) *VMService {
	return &VMService{clusters: clusters}
}

type CreateVMParams struct {
	ClusterName string
	Project     string
	UserID      int64
	VMName      string
	CPU         int
	MemoryMB    int
	DiskGB      int
	OSImage     string
	SSHKeys     []string
	IP          string
	Gateway     string
	SubnetCIDR  string
	StoragePool string
	Network     string
}

type CreateVMResult struct {
	VMName   string `json:"vm_name"`
	IP       string `json:"ip"`
	Username string `json:"username"`
	Password string `json:"password"`
	Node     string `json:"node"`
}

func (s *VMService) Create(ctx context.Context, params CreateVMParams) (*CreateVMResult, error) {
	client, ok := s.clusters.Get(params.ClusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", params.ClusterName)
	}

	password := generatePassword()
	vmName := params.VMName
	if vmName == "" {
		b := make([]byte, 3)
		rand.Read(b)
		vmName = fmt.Sprintf("vm-%s", hex.EncodeToString(b))
	}

	cloudInit := buildCloudInit(password, params.SSHKeys)
	networkConfig := buildNetworkConfig(params.IP, params.SubnetCIDR, params.Gateway)

	imageAlias := params.OSImage
	if len(imageAlias) > 7 && imageAlias[:7] == "images:" {
		imageAlias = imageAlias[7:]
	}

	body := map[string]any{
		"name": vmName,
		"type": "virtual-machine",
		"source": map[string]any{
			"type":     "image",
			"alias":    imageAlias,
			"server":   "https://images.linuxcontainers.org",
			"protocol": "simplestreams",
		},
		"config": map[string]any{
			"limits.cpu":                fmt.Sprintf("%d", params.CPU),
			"limits.memory":            fmt.Sprintf("%dMiB", params.MemoryMB),
			"user.cloud-init":          cloudInit,
			"cloud-init.network-config": networkConfig,
			"security.secureboot":       "false",
		},
		"devices": map[string]any{
			"root": map[string]any{
				"type": "disk",
				"pool": params.StoragePool,
				"path": "/",
				"size": fmt.Sprintf("%dGiB", params.DiskGB),
			},
			"eth0": map[string]any{
				"type":                    "nic",
				"nictype":                 "bridged",
				"parent":                  params.Network,
				"ipv4.address":            params.IP,
				"security.ipv4_filtering": "true",
				"security.mac_filtering":  "true",
			},
		},
	}

	bodyJSON, _ := json.Marshal(body)
	path := fmt.Sprintf("/1.0/instances?project=%s", params.Project)
	resp, err := client.APIPost(ctx, path, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}

	if resp.Type == "async" {
		var op struct{ ID string }
		json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			if err := client.WaitForOperation(ctx, op.ID); err != nil {
				slog.Error("wait for create operation failed", "vm", vmName, "error", err)
			}
		}
	}

	startBody, _ := json.Marshal(map[string]any{"action": "start", "timeout": 60})
	startPath := fmt.Sprintf("/1.0/instances/%s/state?project=%s", vmName, params.Project)
	startResp, err := client.APIPut(ctx, startPath, bytes.NewReader(startBody))
	if err != nil {
		slog.Error("start instance failed", "vm", vmName, "error", err)
	} else if startResp.Type == "async" {
		var op struct{ ID string }
		json.Unmarshal(startResp.Metadata, &op)
		if op.ID != "" {
			client.WaitForOperation(ctx, op.ID)
		}
	}

	node := ""
	if instanceData, err := client.GetInstance(ctx, params.Project, vmName); err == nil {
		var inst struct{ Location string }
		json.Unmarshal(instanceData, &inst)
		node = inst.Location
	}

	slog.Info("vm created", "name", vmName, "ip", params.IP, "node", node, "cluster", params.ClusterName)

	return &CreateVMResult{
		VMName:   vmName,
		IP:       params.IP,
		Username: "ubuntu",
		Password: password,
		Node:     node,
	}, nil
}

func (s *VMService) ChangeState(ctx context.Context, clusterName, project, vmName, action string, force bool) error {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not found", clusterName)
	}

	body := map[string]any{"action": action, "timeout": 30, "force": force}
	bodyJSON, _ := json.Marshal(body)
	path := fmt.Sprintf("/1.0/instances/%s/state?project=%s", vmName, project)

	resp, err := client.APIPut(ctx, path, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("%s vm: %w", action, err)
	}

	if resp.Type == "async" {
		var op struct{ ID string }
		json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			return client.WaitForOperation(ctx, op.ID)
		}
	}

	return nil
}

func (s *VMService) Delete(ctx context.Context, clusterName, project, vmName string) error {
	_ = s.ChangeState(ctx, clusterName, project, vmName, "stop", true)

	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not found", clusterName)
	}

	path := fmt.Sprintf("/1.0/instances/%s?project=%s", vmName, project)
	_, err := client.APIDelete(ctx, path)
	return err
}

type ReinstallParams struct {
	ClusterName string
	Project     string
	VMName      string
	NewOSImage  string
}

type ReinstallResult struct {
	Password string `json:"password"`
	Username string `json:"username"`
}

func (s *VMService) Reinstall(ctx context.Context, params ReinstallParams) (*ReinstallResult, error) {
	client, ok := s.clusters.Get(params.ClusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", params.ClusterName)
	}

	instData, err := client.GetInstance(ctx, params.Project, params.VMName)
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}

	var inst struct {
		Config   map[string]string         `json:"config"`
		Devices  map[string]map[string]any `json:"devices"`
		Location string                    `json:"location"`
	}
	if err := json.Unmarshal(instData, &inst); err != nil {
		return nil, fmt.Errorf("parse instance: %w", err)
	}

	_ = s.ChangeState(ctx, params.ClusterName, params.Project, params.VMName, "stop", true)

	delPath := fmt.Sprintf("/1.0/instances/%s?project=%s", params.VMName, params.Project)
	_, err = client.APIDelete(ctx, delPath)
	if err != nil {
		return nil, fmt.Errorf("delete instance: %w", err)
	}

	password := generatePassword()
	sshKeys := []string{}
	cloudInit := buildCloudInit(password, sshKeys)

	osImage := params.NewOSImage
	if len(osImage) > 7 && osImage[:7] == "images:" {
		osImage = osImage[7:]
	}

	inst.Config["user.cloud-init"] = cloudInit

	body := map[string]any{
		"name": params.VMName,
		"type": "virtual-machine",
		"source": map[string]any{
			"type":     "image",
			"alias":    osImage,
			"server":   "https://images.linuxcontainers.org",
			"protocol": "simplestreams",
		},
		"config":  inst.Config,
		"devices": inst.Devices,
	}

	bodyJSON, _ := json.Marshal(body)
	createPath := fmt.Sprintf("/1.0/instances?project=%s&target=%s", params.Project, inst.Location)
	resp, err := client.APIPost(ctx, createPath, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("recreate instance: %w", err)
	}

	if resp.Type == "async" {
		var op struct{ ID string }
		json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			if err := client.WaitForOperation(ctx, op.ID); err != nil {
				slog.Error("wait for reinstall create failed", "vm", params.VMName, "error", err)
			}
		}
	}

	startBody, _ := json.Marshal(map[string]any{"action": "start", "timeout": 60})
	startPath := fmt.Sprintf("/1.0/instances/%s/state?project=%s", params.VMName, params.Project)
	startResp, err := client.APIPut(ctx, startPath, bytes.NewReader(startBody))
	if err != nil {
		slog.Error("start reinstalled instance failed", "vm", params.VMName, "error", err)
	} else if startResp.Type == "async" {
		var op struct{ ID string }
		json.Unmarshal(startResp.Metadata, &op)
		if op.ID != "" {
			client.WaitForOperation(ctx, op.ID)
		}
	}

	slog.Info("vm reinstalled", "name", params.VMName, "os", params.NewOSImage, "cluster", params.ClusterName)

	return &ReinstallResult{
		Password: password,
		Username: "ubuntu",
	}, nil
}

func (s *VMService) ListInstances(ctx context.Context, clusterName, project string) ([]json.RawMessage, error) {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}
	return client.GetInstances(ctx, project)
}

func (s *VMService) GetInstanceState(ctx context.Context, clusterName, project, vmName string) (json.RawMessage, error) {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}
	return client.GetInstanceState(ctx, project, vmName)
}

// ResetPassword 在 VM 内执行 chpasswd 重置密码
func (s *VMService) ResetPassword(ctx context.Context, clusterName, project, vmName, username string) (string, error) {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return "", fmt.Errorf("cluster %q not found", clusterName)
	}

	newPassword := generatePassword()

	// 使用非交互式 exec 执行 chpasswd
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s:%s' | chpasswd", username, newPassword)}
	retCode, err := client.ExecNonInteractive(ctx, project, vmName, cmd)
	if err != nil {
		return "", fmt.Errorf("exec chpasswd: %w", err)
	}
	if retCode != 0 {
		return "", fmt.Errorf("chpasswd exited with code %d", retCode)
	}

	slog.Info("vm password reset", "vm", vmName, "user", username)
	return newPassword, nil
}

func generatePassword() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func buildCloudInit(password string, sshKeys []string) string {
	ci := fmt.Sprintf("#cloud-config\npassword: %s\nchpasswd:\n  expire: false\nssh_pwauth: true\n", password)
	if len(sshKeys) > 0 {
		ci += "ssh_authorized_keys:\n"
		for _, key := range sshKeys {
			ci += fmt.Sprintf("  - %s\n", key)
		}
	}
	return ci
}

func buildNetworkConfig(ip, cidr, gateway string) string {
	return fmt.Sprintf(`version: 2
ethernets:
  enp5s0:
    addresses:
      - %s/%s
    routes:
      - to: default
        via: %s
    nameservers:
      addresses:
        - 1.1.1.1
        - 8.8.8.8`, ip, cidr, gateway)
}

// Ensure VMService implements status constants from model
var _ = model.VMStatusCreating
