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

type VM struct {
	ID        int64     `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	ClusterID int64     `json:"cluster_id" db:"cluster_id"`
	UserID    int64     `json:"user_id" db:"user_id"`
	OrderID   *int64    `json:"order_id,omitempty" db:"order_id"`
	IP        *string   `json:"ip,omitempty" db:"ip"`
	Status    string    `json:"status" db:"status"`
	CPU       int       `json:"cpu" db:"cpu"`
	MemoryMB  int       `json:"memory_mb" db:"memory_mb"`
	DiskGB    int       `json:"disk_gb" db:"disk_gb"`
	OSImage   string    `json:"os_image" db:"os_image"`
	Node      string    `json:"node" db:"node"`
	Password  *string   `json:"password,omitempty" db:"password"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
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
	Access       string  `json:"access" db:"access"`
	Active       bool    `json:"active" db:"active"`
	SortOrder    int     `json:"sort_order" db:"sort_order"`
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
	ID        int64      `json:"id" db:"id"`
	UserID    int64      `json:"user_id" db:"user_id"`
	ProductID int64      `json:"product_id" db:"product_id"`
	ClusterID int64      `json:"cluster_id" db:"cluster_id"`
	Status    string     `json:"status" db:"status"`
	Amount    float64    `json:"amount" db:"amount"`
	ExpiresAt *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
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

const (
	RoleAdmin    = "admin"
	RoleCustomer = "customer"

	VMStatusCreating  = "creating"
	VMStatusRunning   = "running"
	VMStatusStopped   = "stopped"
	VMStatusSuspended = "suspended"
	VMStatusError     = "error"
	VMStatusDeleted   = "deleted"

	OrderPending      = "pending"
	OrderPaid         = "paid"
	OrderProvisioning = "provisioning"
	OrderActive       = "active"
	OrderExpired      = "expired"
	OrderCancelled    = "cancelled"

	IPAvailable = "available"
	IPAssigned  = "assigned"
	IPReserved  = "reserved"
	IPCooldown  = "cooldown"
)
