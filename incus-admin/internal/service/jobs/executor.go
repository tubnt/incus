package jobs

import (
	"context"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/sshexec"
)

// Executor 是 vm.create / vm.reinstall 共用的接口。
//
// Run 在 in-flight job 上推进步骤；返回 nil 表示成功。
// Rollback 在失败 / stale 恢复时调用，必须幂等：可能已有部分资源（IP、instance）
// 需要清理。runtime 已经把 job 翻到 failed/partial，executor 只关心补偿动作。
type Executor interface {
	Run(ctx context.Context, rt *Runtime, job *model.ProvisioningJob) error
	Rollback(ctx context.Context, rt *Runtime, job *model.ProvisioningJob, reason string)
}

// Params 是 handler 入队时携带的额外配置；存到 provisioning_jobs.params JSONB 是
// 一种选择，但当前 schema 没那列。改用 in-memory map：runtime 维护 jobID→params，
// Enqueue 时 set，worker 消费后 clear。简单也避免 DB 漂移。
type Params struct {
	// vm.create
	Project     string
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
	OrderAmount float64 // 用于 rollback 时的退款金额

	// WP-I1 /v1 三字段透传（cloud-gateway /v1/instances）：
	//   - RootPass：用户指定的 root 密码；为空走 service.GeneratePassword 随机生成。
	//   - UserData：用户自定义 cloud-init user-data，合并进 OS-aware 基础配置（不覆盖）。
	//   - Tags：打到 incus 实例 user.tags 配置（当前 vms 表无 tags 列，落 incus 实例标签）。
	RootPass string
	UserData string
	Tags     []string

	// vm.reinstall
	ImageSource string
	ServerURL   string
	Protocol    string
	DefaultUser string

	// cluster.node.add / cluster.node.remove
	NodeName       string // 新节点名 / 待移除节点名
	NodePublicIP   string // 新节点公网 IP（add 必填）
	NodeRole       string // "osd" 或 "mon-mgr-osd"（add 用，远端 join-node.sh 暂未消费但保留）
	SSHUser        string // 远端 SSH 用户（add）/ leader SSH 用户（remove），默认 root
	SSHKeyFile     string // 私钥路径（admin 服务器本地）
	KnownHostsFile string // known_hosts 文件路径
	LeaderHost     string // 跑 incus cluster add / scale-node.sh 的目标 host，默认配置取
	IncusToken     string // add 时 leader 生成的 join token（在 leader_token 步骤前置同步阶段写入）
	ScriptDir      string // 远端脚本暂存目录，默认 /tmp/incus-admin-scripts

	// OPS-026 / PLAN-028：node 拓扑覆盖（仅 add 用）
	NICPrimary     string // 主网卡名覆盖；为空走 cluster-env.sh 默认
	NICCluster     string // 集群网卡名覆盖
	BridgeName     string // 桥接名覆盖
	MgmtIP         string // mgmt 网 IP 覆盖（不传 = 按 pub IP 末位推算）
	CephPubIP      string // Ceph public 网 IP 覆盖
	CephClusterIP  string // Ceph cluster 网 IP 覆盖
	SkipNetwork    bool   // 跳过 do_network 阶段（运维已预配 IP / 路由 / 桥）

	// PLAN-033 / OPS-039：cluster.node.add 凭据多形态
	// Credential 取代 SSHKeyFile（如果非 nil）；SSHKeyFile 仍保留作向后兼容
	// 与 cluster.node.remove 的全局 default key 路径。worker 消费 params 后
	// 必须 Wipe Credential 把内存中的密码 / 私钥清零。
	Credential             *sshexec.Credential
	AcceptedHostKeySHA256  string // 启动 add-node job 前已写入 known_hosts，仅用于 audit

	// PLAN-037 / OPS-040：cluster.vm.migrate-batch
	ClusterName        string             // VM 所在集群（与 job.ClusterID 等价但批量执行需直接用名）
	BatchItems         []MigrateBatchItem // 待迁移条目（同一 cluster 内）
	ConcurrencyPerSrc  int                // 单个 source 节点并发上限；0 → 默认 2
	GlobalConcurrency  int                // 全局并发上限；0 → 默认 4
}

// MigrateBatchItem 描述一台 VM 的迁移条目。
type MigrateBatchItem struct {
	VMName     string `json:"vm_name"`
	Project    string `json:"project"`
	SourceNode string `json:"source_node"` // 来自前端预解析；空字符串表示"未知/任意"
	TargetNode string `json:"target_node"`
	// PLAN-039 / OPS-043 mode：auto / live / cold；空 = auto。
	Mode string `json:"mode,omitempty"`
}
