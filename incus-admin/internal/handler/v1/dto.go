package v1

import (
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// AccountDTO /v1/account 响应：当前 Bearer token 持有人的最小账户信息。
// balance / currency 字段名固定，给 cloud-gateway 标准 client 用；email 不含 PII
// 之外的字段。
type AccountDTO struct {
	ID       int64   `json:"id"`
	Email    string  `json:"email"`
	Balance  float64 `json:"balance"`
	Currency string  `json:"currency"`
}

// InstanceDTO /v1/instances 单条。字段命名对齐 cloud-gateway 标准：
//   - label = VM.Name（用户可见名）
//   - type = order.product_id（VM 未存 product_id，借 order 反查）；无 order 时 0
//   - region = cluster.name（cloud-gateway 标准 region 是字符串 id，对应我们的 cluster.name）
//   - image = VM.OSImage（os_template.source 形式，如 "ubuntu/24.04/cloud"）
//   - ip4 = VM.IP（IPv6 暂未存独立列，ip6 留空给将来）
//   - tags = []（VM 表无 tags 列，预留字段以兼容 cloud-gateway schema）
type InstanceDTO struct {
	ID        int64     `json:"id"`
	Label     string    `json:"label"`
	Type      int64     `json:"type"`
	Region    string    `json:"region"`
	Image     string    `json:"image"`
	Status    string    `json:"status"`
	IP4       string    `json:"ip4"`
	IP6       string    `json:"ip6"`
	CreatedAt time.Time `json:"created_at"`
	Tags      []string  `json:"tags"`
}

// PricesDTO 嵌入 TypeDTO 的价格分组（cloud-gateway 标准把 monthly/daily 折成
// 一个 prices 子对象，便于以后 INFRA-013 daily 价格直接追加字段，不破坏 schema）。
// daily 为 nil 时 omitempty 不输出。
type PricesDTO struct {
	Monthly float64  `json:"monthly"`
	Daily   *float64 `json:"daily,omitempty"`
}

// TypeDTO /v1/types 单条。把 product 的 spec 折给 cloud-gateway client：
// id 用 product.id（与 InstanceDTO.Type 对齐，client 可直接复用），slug 透传供
// 用户友好显示。一期沿用现有 monthly 计价；price_daily 未填时 omitempty。
type TypeDTO struct {
	ID          int64     `json:"id"`
	Label       string    `json:"label"`
	Slug        string    `json:"slug"`
	VCPUs       int       `json:"vcpus"`
	MemoryMB    int       `json:"memory_mb"`
	DiskGB      int       `json:"disk_gb"`
	BandwidthTB int       `json:"bandwidth_tb"`
	Prices      PricesDTO `json:"prices"`
}

// RegionDTO /v1/regions 单条。id 取 cluster.name（cloud-gateway region id 习惯用
// 字符串 slug；name 已是稳定 slug，比 numeric id 更易于跨环境写死）。
// country / city 未填时输出空字符串而不是 null，避免 client 必须做 null check。
type RegionDTO struct {
	ID           string   `json:"id"`
	Country      string   `json:"country"`
	City         string   `json:"city"`
	Status       string   `json:"status"`
	Capabilities []string `json:"capabilities"`
}

// ImageDTO /v1/images 单条。id = os_template.slug（与 client 创建 instance 时
// image 字段对齐）；os/version/arch 从 source 解析（"ubuntu/24.04/cloud" →
// os=ubuntu, version=24.04, arch=amd64 默认）。解析失败字段留空，label 仍能用。
type ImageDTO struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	OS      string `json:"os"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
}

// SSHKeyDTO /v1/ssh-keys 单条。fingerprint 输出 DB 存的格式（通常 SHA256:xxx），
// public_key 是完整的 "ssh-rsa AAAA... user@host" 字符串。
type SSHKeyDTO struct {
	ID          int64  `json:"id"`
	Label       string `json:"label"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
}

// toAccountDTO 把 model.User 折成 cloud-gateway /v1/account 响应。
// currency 一期固定 "USD"（products 表沿用同币种；后续多币种支持时改为读 product
// 或新建 users.currency 列）。
func toAccountDTO(u model.User) AccountDTO {
	return AccountDTO{
		ID:       u.ID,
		Email:    u.Email,
		Balance:  u.Balance,
		Currency: "USD",
	}
}

// toInstanceDTO 折出单条 InstanceDTO。
//   - regionName: cluster_id → cluster.name 的预解析结果（caller 负责一次性查询、
//     避免 N+1）；查不到时传 "" 即可。
//   - productID: VM.OrderID → order.product_id（同上，caller 批量解析）；查不到 0。
//
// VM.IP 直接当作 ip4；ip6 当前未独立存。
func toInstanceDTO(vm model.VM, regionName string, productID int64) InstanceDTO {
	ip4 := ""
	if vm.IP != nil {
		ip4 = *vm.IP
	}
	return InstanceDTO{
		ID:        vm.ID,
		Label:     vm.Name,
		Type:      productID,
		Region:    regionName,
		Image:     vm.OSImage,
		Status:    vm.Status,
		IP4:       ip4,
		IP6:       "",
		CreatedAt: vm.CreatedAt,
		Tags:      []string{},
	}
}

// toTypeDTO 折出单条 TypeDTO。
func toTypeDTO(p model.Product) TypeDTO {
	return TypeDTO{
		ID:          p.ID,
		Label:       p.Name,
		Slug:        p.Slug,
		VCPUs:       p.CPU,
		MemoryMB:    p.MemoryMB,
		DiskGB:      p.DiskGB,
		BandwidthTB: p.BandwidthTB,
		Prices: PricesDTO{
			Monthly: p.PriceMonthly,
			Daily:   p.PriceDaily,
		},
	}
}

// toRegionDTO 折出单条 RegionDTO。capabilities 为 nil 时回退到默认 ["instances"]，
// 与 repository.scanBase 行为对齐（防御性兜底，避免 admin 手动把列改空导致输出 null）。
func toRegionDTO(c model.Cluster) RegionDTO {
	caps := c.Capabilities
	if caps == nil {
		caps = append([]string{}, model.DefaultClusterCapabilities...)
	}
	status := c.RegionStatus
	if status == "" {
		status = model.RegionStatusAvailable
	}
	return RegionDTO{
		ID:           c.Name,
		Country:      c.Country,
		City:         c.City,
		Status:       status,
		Capabilities: caps,
	}
}

// toImageDTO 折出单条 ImageDTO。source 形如 "ubuntu/24.04/cloud" 时拆 os/version；
// 三段以下保持原文落到 os 字段（version 留空）；arch 缺省 "amd64"（DB 未存）。
func toImageDTO(t model.OSTemplate) ImageDTO {
	os, version := parseSourceOSVersion(t.Source)
	return ImageDTO{
		ID:      t.Slug,
		Label:   t.Name,
		OS:      os,
		Version: version,
		Arch:    "amd64",
	}
}

// parseSourceOSVersion 把 "ubuntu/24.04/cloud" 拆成 (ubuntu, 24.04)。
// 仅一段（如 "windows"）→ os=windows, version=""；空字符串 → 都空。
func parseSourceOSVersion(source string) (os, version string) {
	if source == "" {
		return "", ""
	}
	parts := strings.SplitN(source, "/", 3)
	switch len(parts) {
	case 1:
		return parts[0], ""
	default:
		return parts[0], parts[1]
	}
}

// toSSHKeyDTO 折出单条 SSHKeyDTO。
func toSSHKeyDTO(k model.SSHKey) SSHKeyDTO {
	return SSHKeyDTO{
		ID:          k.ID,
		Label:       k.Name,
		Fingerprint: k.Fingerprint,
		PublicKey:   k.PublicKey,
	}
}
