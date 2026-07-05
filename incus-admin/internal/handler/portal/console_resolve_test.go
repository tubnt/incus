package portal

import (
	"testing"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/config"
	"github.com/incuscloud/incus-admin/internal/model"
)

// WP-E：console/snapshot 越权修复的核心是"从 owner 的 VM 行反解 cluster/project，
// 忽略客户端传值"。resolveClusterProjectForVM 只接受 VM 行、不接受任何客户端输入，
// 本测试断言：给定 VM 落在集群 B，就解析出集群 B 的名字与其 DefaultProject——
// 无论调用方（攻击者）在 query/body 里传什么 cluster=A，都影响不到结果。
func TestResolveClusterProjectForVM(t *testing.T) {
	configs := []config.ClusterConfig{
		{Name: "alpha", APIURL: "https://alpha", DefaultProject: "proj-a"},
		{Name: "beta", APIURL: "https://beta", DefaultProject: "proj-b"},
		{Name: "gamma", APIURL: "https://gamma"}, // 无 DefaultProject → 回退 customers
	}
	mgr := cluster.NewTestManager(configs)
	mgr.SetID("alpha", 1)
	mgr.SetID("beta", 2)
	mgr.SetID("gamma", 3)

	tests := []struct {
		name        string
		vm          *model.VM
		wantCluster string
		wantProject string
	}{
		{
			name:        "VM 在 beta，按行反解到 beta 而非任何客户端传值",
			vm:          &model.VM{Name: "web", ClusterID: 2},
			wantCluster: "beta",
			wantProject: "proj-b",
		},
		{
			name:        "同名 VM 在 alpha，独立解析到 alpha",
			vm:          &model.VM{Name: "web", ClusterID: 1},
			wantCluster: "alpha",
			wantProject: "proj-a",
		},
		{
			name:        "cluster 无 DefaultProject 时回退 customers",
			vm:          &model.VM{Name: "db", ClusterID: 3},
			wantCluster: "gamma",
			wantProject: "customers",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCluster, gotProject := resolveClusterProjectForVM(mgr, tt.vm)
			if gotCluster != tt.wantCluster {
				t.Errorf("cluster = %q, want %q", gotCluster, tt.wantCluster)
			}
			if gotProject != tt.wantProject {
				t.Errorf("project = %q, want %q", gotProject, tt.wantProject)
			}
		})
	}
}

// 跨集群同名越权的关键断言：两个同名 VM 分属不同集群/不同 owner，
// 反解结果只由 VM 行的 ClusterID 决定，攻击者无法通过"传别人的 cluster"
// 让自己的 VM 名解析到别人集群。
func TestResolveClusterProjectForVM_CrossClusterSameName(t *testing.T) {
	mgr := cluster.NewTestManager([]config.ClusterConfig{
		{Name: "alpha", APIURL: "https://alpha", DefaultProject: "proj-a"},
		{Name: "beta", APIURL: "https://beta", DefaultProject: "proj-b"},
	})
	mgr.SetID("alpha", 1)
	mgr.SetID("beta", 2)

	// 用户自己的 VM "web" 在 alpha（ClusterID=1）。
	ownVM := &model.VM{Name: "web", ClusterID: 1, UserID: 42}
	cl, proj := resolveClusterProjectForVM(mgr, ownVM)
	if cl != "alpha" || proj != "proj-a" {
		t.Fatalf("own VM resolved to %q/%q, want alpha/proj-a — 反解不应受客户端影响", cl, proj)
	}

	// 别人集群 beta 里的同名 VM 是另一行（ClusterID=2）；即便攻击者知道该 VM 名，
	// 反解仍严格跟随传入行的 ClusterID，绝不会把 ownVM 解析到 beta。
	if cl == "beta" {
		t.Fatal("own VM 不应解析到 beta —— 跨集群同名越权未被阻断")
	}
}
