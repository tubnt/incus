package model

import (
	"time"
)

type User struct {
	ID        int64     `json:"id" db:"id"`
	Email     string    `json:"email" db:"email"`
	Name      string    `json:"name" db:"name"`
	Role      string    `json:"role" db:"role"`
	LogtoSub  string    `json:"-" db:"logto_sub"`
	Balance   float64   `json:"balance" db:"balance"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type Cluster struct {
	ID          int64     `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	DisplayName string    `json:"display_name" db:"display_name"`
	APIURL      string    `json:"api_url" db:"api_url"`
	Status      string    `json:"status" db:"status"`
	// PLAN-027 / INFRA-003：完整运行时配置进 DB
	Kind           string `json:"kind" db:"kind"` // 'cluster' | 'standalone'
	CertFile       string `json:"cert_file,omitempty" db:"cert_file"`
	KeyFile        string `json:"key_file,omitempty" db:"key_file"`
	CAFile         string `json:"ca_file,omitempty" db:"ca_file"`
	DefaultProject string `json:"default_project,omitempty" db:"default_project"`
	StoragePool    string `json:"storage_pool,omitempty" db:"storage_pool"`
	Network        string `json:"network,omitempty" db:"network"`
	// IPPoolsJSON 是 config.IPPoolConfig 数组的序列化形式；repo 层 unmarshal
	IPPoolsJSON string `json:"-" db:"ip_pools_json"`
	// PLAN-053 Phase C / INFRA-012：region metadata（cloud-gateway /v1/regions 必需）
	// country / city 留空表示未填；region_status 与 status 解耦，仅用于对外开放下单口径。
	// capabilities 一期固定 ["instances"]。
	Country      string `json:"country,omitempty" db:"country"`
	City         string `json:"city,omitempty" db:"city"`
	RegionStatus string `json:"region_status" db:"region_status"`
	// CapabilitiesJSON 是 capabilities JSONB 列的原始字节；repo 层负责 unmarshal 到 Capabilities。
	CapabilitiesJSON []byte    `json:"-" db:"capabilities"`
	Capabilities     []string  `json:"capabilities" db:"-"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

const (
	RegionStatusAvailable   = "available"
	RegionStatusUnavailable = "unavailable"
	RegionStatusMaintenance = "maintenance"
)

// DefaultClusterCapabilities 与 migration 027 中 JSONB DEFAULT 对齐；
// repo 层 unmarshal 失败 / 列为 NULL 时回退到这个值。
var DefaultClusterCapabilities = []string{"instances"}

type VM struct {
	ID                  int64      `json:"id" db:"id"`
	Name                string     `json:"name" db:"name"`
	ClusterID           int64      `json:"cluster_id" db:"cluster_id"`
	UserID              int64      `json:"user_id" db:"user_id"`
	OrderID             *int64     `json:"order_id,omitempty" db:"order_id"`
	IP                  *string    `json:"ip,omitempty" db:"ip"`
	Status              string     `json:"status" db:"status"`
	CPU                 int        `json:"cpu" db:"cpu"`
	MemoryMB            int        `json:"memory_mb" db:"memory_mb"`
	DiskGB              int        `json:"disk_gb" db:"disk_gb"`
	OSImage             string     `json:"os_image" db:"os_image"`
	Node                string     `json:"node" db:"node"`
	Password            *string    `json:"password,omitempty" db:"password"`
	RescueState         string     `json:"rescue_state" db:"rescue_state"`
	RescueStartedAt     *time.Time `json:"rescue_started_at,omitempty" db:"rescue_started_at"`
	RescueSnapshotName  *string    `json:"rescue_snapshot_name,omitempty" db:"rescue_snapshot_name"`
	// PLAN-034 trash-with-undo. TrashedAt != nil 表示 VM 在回收站；超过 trash 窗口后
	// worker 走原 hard-delete 路径。TrashedPrevStatus 记 trash 前的 status，restore
	// 时让前端决定是否要重新启动（不自动启，更安全）。
	TrashedAt           *time.Time `json:"trashed_at,omitempty" db:"trashed_at"`
	TrashedPrevStatus   *string    `json:"trashed_prev_status,omitempty" db:"trashed_prev_status"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
}

type Product struct {
	ID           int64   `json:"id" db:"id"`
	Name         string  `json:"name" db:"name"`
	Slug         string  `json:"slug" db:"slug"`
	CPU          int     `json:"cpu" db:"cpu"`
	MemoryMB     int     `json:"memory_mb" db:"memory_mb"`
	DiskGB       int     `json:"disk_gb" db:"disk_gb"`
	BandwidthTB  int     `json:"bandwidth_tb" db:"bandwidth_tb"`
	PriceMonthly float64 `json:"price_monthly" db:"price_monthly"`
	// PLAN-054 / INFRA-013：按天单价。nil 表示不支持 daily 周期。
	// NUMERIC(10,4) → float64 接收；账单计算严禁累加浮点结果，由 worker
	// 走 NUMERIC SQL 表达式做扣费。
	PriceDaily *float64 `json:"price_daily,omitempty" db:"price_daily"`
	// PeriodSupported 是 TEXT[]，至少含一个值（DB 层 DEFAULT ARRAY['monthly']）。
	// repo 层用 pgTextSlice 适配 PG array 反序列化。
	PeriodSupported []string `json:"period_supported" db:"period_supported"`
	Currency        string   `json:"currency" db:"currency"`
	Access          string   `json:"access" db:"access"`
	Active          bool     `json:"active" db:"active"`
	SortOrder       int      `json:"sort_order" db:"sort_order"`
}

type Quota struct {
	ID           int64 `json:"id" db:"id"`
	UserID       int64 `json:"user_id" db:"user_id"`
	MaxVMs       int   `json:"max_vms" db:"max_vms"`
	MaxVCPUs     int   `json:"max_vcpus" db:"max_vcpus"`
	MaxRAMMB     int   `json:"max_ram_mb" db:"max_ram_mb"`
	MaxDiskGB    int   `json:"max_disk_gb" db:"max_disk_gb"`
	MaxIPs       int   `json:"max_ips" db:"max_ips"`
	MaxSnapshots int   `json:"max_snapshots" db:"max_snapshots"`
	// PLAN-035 用户级 firewall quota（默认 5 组 × 20 规则）
	MaxFirewallGroups        int `json:"max_firewall_groups" db:"max_firewall_groups"`
	MaxFirewallRulesPerGroup int `json:"max_firewall_rules_per_group" db:"max_firewall_rules_per_group"`
}

type IPPool struct {
	ID        int64  `json:"id" db:"id"`
	ClusterID int64  `json:"cluster_id" db:"cluster_id"`
	CIDR      string `json:"cidr" db:"cidr"`
	Gateway   string `json:"gateway" db:"gateway"`
	VLANID    int    `json:"vlan_id" db:"vlan_id"`
}

type IPAddress struct {
	ID            int64      `json:"id" db:"id"`
	PoolID        int64      `json:"pool_id" db:"pool_id"`
	IP            string     `json:"ip" db:"ip"`
	VMID          *int64     `json:"vm_id,omitempty" db:"vm_id"`
	Status        string     `json:"status" db:"status"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty" db:"cooldown_until"`
}

type Order struct {
	ID        int64   `json:"id" db:"id"`
	UserID    int64   `json:"user_id" db:"user_id"`
	ProductID int64   `json:"product_id" db:"product_id"`
	ClusterID int64   `json:"cluster_id" db:"cluster_id"`
	Status    string  `json:"status" db:"status"`
	Amount    float64 `json:"amount" db:"amount"`
	Currency  string  `json:"currency" db:"currency"`
	// PLAN-054 / INFRA-013：计费周期，'daily' | 'monthly'。
	// DB DEFAULT 'monthly'：历史订单 ALTER 后自动归 monthly，行为 100% 不变。
	Period     string     `json:"period" db:"period"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
}

type Transaction struct {
	ID          int64     `json:"id" db:"id"`
	UserID      int64     `json:"user_id" db:"user_id"`
	Amount      float64   `json:"amount" db:"amount"`
	Type        string    `json:"type" db:"type"`
	Description string    `json:"description" db:"description"`
	InvoiceID   *int64    `json:"invoice_id,omitempty" db:"invoice_id"`
	CreatedBy   *int64    `json:"created_by,omitempty" db:"created_by"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

type AuditLog struct {
	ID         int64     `json:"id" db:"id"`
	UserID     *int64    `json:"user_id,omitempty" db:"user_id"`
	Action     string    `json:"action" db:"action"`
	TargetType string    `json:"target_type" db:"target_type"`
	TargetID   int64     `json:"target_id" db:"target_id"`
	Details    string    `json:"details" db:"details"`
	IPAddress  string    `json:"ip_address" db:"ip_address"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

type Invoice struct {
	ID        int64      `json:"id" db:"id"`
	OrderID   int64      `json:"order_id" db:"order_id"`
	UserID    int64      `json:"user_id" db:"user_id"`
	Amount    float64    `json:"amount" db:"amount"`
	Currency  string     `json:"currency" db:"currency"`
	Status    string     `json:"status" db:"status"`
	DueAt     *time.Time `json:"due_at,omitempty" db:"due_at"`
	PaidAt    *time.Time `json:"paid_at,omitempty" db:"paid_at"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

type APIToken struct {
	ID         int64      `json:"id" db:"id"`
	UserID     int64      `json:"user_id" db:"user_id"`
	Name       string     `json:"name" db:"name"`
	TokenHash  string     `json:"-" db:"token_hash"`
	Token      string     `json:"token,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
}

type SSHKey struct {
	ID          int64     `json:"id" db:"id"`
	UserID      int64     `json:"user_id" db:"user_id"`
	Name        string    `json:"name" db:"name"`
	PublicKey   string    `json:"public_key" db:"public_key"`
	Fingerprint string    `json:"fingerprint" db:"fingerprint"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

type Ticket struct {
	ID        int64     `json:"id" db:"id"`
	UserID    int64     `json:"user_id" db:"user_id"`
	Subject   string    `json:"subject" db:"subject"`
	Status    string    `json:"status" db:"status"`
	Priority  string    `json:"priority" db:"priority"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type TicketMessage struct {
	ID        int64     `json:"id" db:"id"`
	TicketID  int64     `json:"ticket_id" db:"ticket_id"`
	UserID    int64     `json:"user_id" db:"user_id"`
	Body      string    `json:"body" db:"body"`
	IsStaff   bool      `json:"is_staff" db:"is_staff"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type FloatingIP struct {
	ID          int64      `json:"id" db:"id"`
	ClusterID   int64      `json:"cluster_id" db:"cluster_id"`
	IP          string     `json:"ip" db:"ip"`
	BoundVMID   *int64     `json:"bound_vm_id,omitempty" db:"bound_vm_id"`
	Status      string     `json:"status" db:"status"`
	Description string     `json:"description" db:"description"`
	AllocatedAt time.Time  `json:"allocated_at" db:"allocated_at"`
	AttachedAt  *time.Time `json:"attached_at,omitempty" db:"attached_at"`
	DetachedAt  *time.Time `json:"detached_at,omitempty" db:"detached_at"`
}

type FirewallGroup struct {
	ID          int64     `json:"id" db:"id"`
	Slug        string    `json:"slug" db:"slug"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	// OwnerID 区分共享组（NULL）与用户私有组（=users.id）。PLAN-035。
	// service.ACLName 按此字段决定 Incus ACL 命名前缀（共享组保留 fwg-<slug>
	// 旧名向后兼容，用户组用 fwg-u<id>-<slug> 隔离 namespace）。
	OwnerID     *int64    `json:"owner_id,omitempty" db:"owner_id"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

type FirewallRule struct {
	ID              int64     `json:"id" db:"id"`
	GroupID         int64     `json:"group_id" db:"group_id"`
	// Direction is 'ingress' (default; matches phase-E behaviour) or 'egress'.
	// Forwarded verbatim into the Incus ACL rule.direction field.
	Direction       string    `json:"direction" db:"direction"`
	Action          string    `json:"action" db:"action"`
	Protocol        string    `json:"protocol" db:"protocol"`
	DestinationPort string    `json:"destination_port" db:"destination_port"`
	SourceCIDR      string    `json:"source_cidr" db:"source_cidr"`
	Description     string    `json:"description" db:"description"`
	SortOrder       int       `json:"sort_order" db:"sort_order"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
}

type VMFirewallBinding struct {
	VMID      int64     `json:"vm_id" db:"vm_id"`
	GroupID   int64     `json:"group_id" db:"group_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type OSTemplate struct {
	ID                int64     `json:"id" db:"id"`
	Slug              string    `json:"slug" db:"slug"`
	Name              string    `json:"name" db:"name"`
	Source            string    `json:"source" db:"source"`
	Protocol          string    `json:"protocol" db:"protocol"`
	ServerURL         string    `json:"server_url" db:"server_url"`
	DefaultUser       string    `json:"default_user" db:"default_user"`
	CloudInitTemplate string    `json:"cloud_init_template" db:"cloud_init_template"`
	SupportsRescue    bool      `json:"supports_rescue" db:"supports_rescue"`
	Enabled           bool      `json:"enabled" db:"enabled"`
	SortOrder         int       `json:"sort_order" db:"sort_order"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}

// VMSubscription PLAN-054 / INFRA-013：单台 VM 的计费订阅。
// monthly：现行一次性付费同时插一行 sub 记账（paid_until = NOW + 30d）。
// daily：扣 1 天费用 + 创建 sub（paid_until = NOW + 24h）。
// 余额不足走 suspended → grace_until → cancelled 三态。
type VMSubscription struct {
	ID           int64      `json:"id" db:"id"`
	VMID         int64      `json:"vm_id" db:"vm_id"`
	ProductID    int64      `json:"product_id" db:"product_id"`
	UserID       int64      `json:"user_id" db:"user_id"`
	Period       string     `json:"period" db:"period"`
	DailyRate    *float64   `json:"daily_rate,omitempty" db:"daily_rate"`
	MonthlyRate  *float64   `json:"monthly_rate,omitempty" db:"monthly_rate"`
	PaidUntil    time.Time  `json:"paid_until" db:"paid_until"`
	Status       string     `json:"status" db:"status"`
	SuspendedAt  *time.Time `json:"suspended_at,omitempty" db:"suspended_at"`
	GraceUntil   *time.Time `json:"grace_until,omitempty" db:"grace_until"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

// BillingCharge PLAN-054 / INFRA-013：单次日扣记账。
// UNIQUE (subscription_id, charge_date) 保证 worker 重跑幂等。
// status: 'paid'（落账 + transactions 行）/ 'insufficient'（余额不足）/
// 'skipped'（窗口外或人工干预）。
type BillingCharge struct {
	ID             int64     `json:"id" db:"id"`
	SubscriptionID int64     `json:"subscription_id" db:"subscription_id"`
	ChargeDate     time.Time `json:"charge_date" db:"charge_date"`
	Amount         float64   `json:"amount" db:"amount"`
	Status         string    `json:"status" db:"status"`
	TransactionID  *int64    `json:"transaction_id,omitempty" db:"transaction_id"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// IdempotencyKey PLAN-053 / INFRA-012：cloud-gateway 标准写操作幂等缓存。
// middleware 命中 key 直接回放 status_code + response_body；24h TTL 由 cleanup
// worker 回收。RequestHash 用于检测「同 key 异 payload」攻击场景。
// JSON 序列化故意排除 response_body / request_hash（标 `json:"-"`），避免
// admin 审计页意外把缓存响应字节泄露到前端。
type IdempotencyKey struct {
	Key          string    `json:"key" db:"key"`
	UserID       int64     `json:"user_id" db:"user_id"`
	Method       string    `json:"method" db:"method"`
	Path         string    `json:"path" db:"path"`
	StatusCode   int       `json:"status_code" db:"status_code"`
	ResponseBody []byte    `json:"-" db:"response_body"`
	RequestHash  string    `json:"-" db:"request_hash"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// ProvisioningJob 是一次 VM 创建/重装的异步执行单元。
// 失败 / 进程崩溃后由 worker sweeper 兜底退款，refund_done_at 是幂等 guard。
type ProvisioningJob struct {
	ID            int64                 `json:"id" db:"id"`
	Kind          string                `json:"kind" db:"kind"`
	UserID        int64                 `json:"user_id" db:"user_id"`
	ClusterID     int64                 `json:"cluster_id" db:"cluster_id"`
	OrderID       *int64                `json:"order_id,omitempty" db:"order_id"`
	VMID          *int64                `json:"vm_id,omitempty" db:"vm_id"`
	TargetName    string                `json:"target_name" db:"target_name"`
	Status        string                `json:"status" db:"status"`
	Error         *string               `json:"error,omitempty" db:"error"`
	RefundDoneAt  *time.Time            `json:"refund_done_at,omitempty" db:"refund_done_at"`
	CreatedAt     time.Time             `json:"created_at" db:"created_at"`
	StartedAt     *time.Time            `json:"started_at,omitempty" db:"started_at"`
	CompletedAt   *time.Time            `json:"completed_at,omitempty" db:"completed_at"`
	Steps         []ProvisioningJobStep `json:"steps,omitempty"`
}

type ProvisioningJobStep struct {
	ID          int64      `json:"id" db:"id"`
	JobID       int64      `json:"job_id" db:"job_id"`
	Seq         int        `json:"seq" db:"seq"`
	Name        string     `json:"name" db:"name"`
	Status      string     `json:"status" db:"status"`
	Detail      *string    `json:"detail,omitempty" db:"detail"`
	StartedAt   *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty" db:"completed_at"`
}

const (
	RoleAdmin    = "admin"
	RoleCustomer = "customer"

	VMStatusCreating  = "creating"
	VMStatusRunning   = "running"
	VMStatusStopped   = "stopped"
	VMStatusSuspended = "suspended"
	VMStatusError     = "error"
	VMStatusDeleted   = "deleted"

	// PLAN-034: trash 窗口 30s。worker 每 5s 扫一次 trashed_at <= NOW()-VMTrashWindow
	// 的行执行 hard-delete。
	VMTrashWindowSeconds = 30

	OrderPending      = "pending"
	OrderPaid         = "paid"
	OrderProvisioning = "provisioning"
	OrderActive       = "active"
	OrderExpired      = "expired"
	OrderCancelled    = "cancelled"

	// PLAN-054 / INFRA-013 计费周期。orders.period / vm_subscriptions.period 共用。
	BillingPeriodDaily   = "daily"
	BillingPeriodMonthly = "monthly"

	// PLAN-054 / INFRA-013 订阅状态。
	SubscriptionStatusActive    = "active"
	SubscriptionStatusSuspended = "suspended"
	SubscriptionStatusCancelled = "cancelled"

	// PLAN-054 / INFRA-013 单次扣费状态。
	BillingChargePaid         = "paid"
	BillingChargeInsufficient = "insufficient"
	BillingChargeSkipped      = "skipped"

	// PLAN-025 / INFRA-007 provisioning job
	JobKindVMCreate    = "vm.create"
	JobKindVMReinstall = "vm.reinstall"

	// PLAN-026 / INFRA-002 cluster node orchestration（复用 jobs runtime + SSE）
	JobKindClusterNodeAdd    = "cluster.node.add"
	JobKindClusterNodeRemove = "cluster.node.remove"

	// PLAN-037 / OPS-040 批量冷迁移（复用 jobs runtime + SSE）
	JobKindClusterVMMigrateBatch = "cluster.vm.migrate-batch"

	// PLAN-027 / INFRA-003 cluster topology kind
	ClusterKindCluster    = "cluster"
	ClusterKindStandalone = "standalone"

	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
	JobStatusPartial   = "partial"

	StepStatusPending   = "pending"
	StepStatusRunning   = "running"
	StepStatusSucceeded = "succeeded"
	StepStatusFailed    = "failed"
	StepStatusSkipped   = "skipped"
	// OPS-051 / PLAN-052：smoke test 软失败（cloud-init / verify_ready 超时）
	// 不让 job=failed（避免误退款 + 删 VM），但用户在 UI 上能看到 warning step
	// + 详情 detail，可手动重试或删 VM 重开。
	StepStatusWarning = "warning"
)

// BillingPeriodDuration 返回一个完整周期对应的 time.Duration。
// daily → 24h，monthly → 30d。worker 续费推 paid_until 与 restore 重置
// paid_until 共用此函数，避免两处硬编码漂移。未知 period 返 0（调用方应
// 已在 handler 层用 oneof 校验拦下，进到这里属逻辑错误）。
func BillingPeriodDuration(period string) time.Duration {
	switch period {
	case BillingPeriodDaily:
		return 24 * time.Hour
	case BillingPeriodMonthly:
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}
