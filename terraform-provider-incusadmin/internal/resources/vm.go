package resources

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/incuscloud/terraform-provider-incusadmin/internal/client"
)

// NewVMResource 注册 incusadmin_vm 资源（admin only）。
//
// P0 CR 修复（#3）：原方案 POST /portal/vms 不存在，portal VM 走订单流。
// 改用 admin endpoint POST /admin/clusters/{cluster}/vms（直接创建跳过订单），
// schema 用 cluster 替代 project（admin endpoint 是 cluster-scoped）。
//
// 字段 cpu/memory_mb/disk_gb/os_image 与后端 AdminVMHandler.CreateVM DTO 对齐。
// 一期所有字段 RequiresReplace（incus-admin 不支持 in-place resize）；
// memory_mb 二期等 admin PATCH endpoint 落地后改 in-place。
func NewVMResource() resource.Resource { return &vmResource{} }

type vmResource struct{ c *client.Client }

type vmModel struct {
	ID       types.Int64  `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Cluster  types.String `tfsdk:"cluster"`
	CPU      types.Int64  `tfsdk:"cpu"`
	MemoryMB types.Int64  `tfsdk:"memory_mb"`
	DiskGB   types.Int64  `tfsdk:"disk_gb"`
	OSImage  types.String `tfsdk:"os_image"`
	IP       types.String `tfsdk:"ip"`
	Status   types.String `tfsdk:"status"`
	Node     types.String `tfsdk:"node"`
}

func (r *vmResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "incusadmin_vm"
}

func (r *vmResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiresReplace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Incus VM 资源（admin only，跳过订单流）。Import ID 形式 `cluster/name`。一期所有字段修改触发 ForceNew；二期接 admin PATCH 端点支持 in-place resize。",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true},
			"cluster": schema.StringAttribute{
				Required:      true,
				Description:   "Cluster 名（与 incus-admin admin clusters 列表一致）",
				PlanModifiers: requiresReplace,
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "VM 名（仅小写字母 / 数字 / 连字符）",
				PlanModifiers: requiresReplace,
			},
			"cpu":       schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{}},
			"memory_mb": schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{}},
			"disk_gb":   schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{}},
			"os_image": schema.StringAttribute{
				Required:      true,
				Description:   "OS 镜像 alias（如 ubuntu-22.04）",
				PlanModifiers: requiresReplace,
			},
			"ip":     schema.StringAttribute{Computed: true},
			"status": schema.StringAttribute{Computed: true},
			"node":   schema.StringAttribute{Computed: true},
		},
	}
}

func (r *vmResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("provider data type", "expected *client.Client")
		return
	}
	r.c = c
}

// adminCreateVMReq 与后端 AdminVMHandler.CreateVM body schema 对齐。
type adminCreateVMReq struct {
	CPU          int      `json:"cpu"`
	MemoryMB     int      `json:"memory_mb"`
	DiskGB       int      `json:"disk_gb"`
	OSImage      string   `json:"os_image,omitempty"`
	Project      string   `json:"project,omitempty"`
	SSHKeys      []string `json:"ssh_keys,omitempty"`
	TargetUserID int64    `json:"target_user_id,omitempty"`
	Count        int      `json:"count,omitempty"`
	Name         string   `json:"name,omitempty"` // PLAN-026 batch with names
}

func (r *vmResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := adminCreateVMReq{
		CPU:      int(plan.CPU.ValueInt64()),
		MemoryMB: int(plan.MemoryMB.ValueInt64()),
		DiskGB:   int(plan.DiskGB.ValueInt64()),
		OSImage:  plan.OSImage.ValueString(),
		Name:     plan.Name.ValueString(),
		Count:    1,
	}
	// 后端响应：异步 jobs runtime 返 202 + { vm: {...}, job_id: ... }；
	// 这里一期不轮询 SSE，直接接受异步语义后立即 Read 拉最新。
	var out struct {
		VM    *client.VM `json:"vm,omitempty"`
		JobID int64      `json:"job_id,omitempty"`
	}
	cluster := plan.Cluster.ValueString()
	if err := r.c.Do(ctx, "POST", fmt.Sprintf("/api/admin/clusters/%s/vms", cluster), body, &out); err != nil {
		resp.Diagnostics.AddError("create vm failed", err.Error())
		return
	}
	if out.VM == nil {
		// 异步路径下后端可能仅返 job_id；让 Read 用 name 拉详情兜底
		resp.Diagnostics.AddWarning(
			"vm creation queued",
			fmt.Sprintf("VM %s 创建已入队（job_id=%d）。Provider 不轮询 SSE；下次 plan/refresh 时 Read 自动同步。", plan.Name.ValueString(), out.JobID),
		)
		plan.Status = types.StringValue("creating")
		plan.IP = types.StringNull()
		plan.Node = types.StringNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}
	plan.ID = types.Int64Value(out.VM.ID)
	plan.Status = types.StringValue(out.VM.Status)
	plan.Node = types.StringValue(out.VM.Node)
	if out.VM.IP != nil {
		plan.IP = types.StringValue(*out.VM.IP)
	} else {
		plan.IP = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cluster := state.Cluster.ValueString()
	name := state.Name.ValueString()
	var out struct {
		VM client.VM `json:"vm"`
	}
	// admin GET /clusters/{name}/vms/{vmName} 返 single VM by cluster + name
	if err := r.c.Do(ctx, "GET", fmt.Sprintf("/api/admin/clusters/%s/vms/%s", cluster, name), nil, &out); err != nil {
		// 404 → drift；让 framework 自动从 state remove
		if strings.Contains(err.Error(), "404") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("read vm failed", err.Error())
		return
	}
	state.ID = types.Int64Value(out.VM.ID)
	state.CPU = types.Int64Value(int64(out.VM.CPU))
	state.MemoryMB = types.Int64Value(int64(out.VM.MemoryMB))
	state.DiskGB = types.Int64Value(int64(out.VM.DiskGB))
	state.OSImage = types.StringValue(out.VM.OSImage)
	state.Status = types.StringValue(out.VM.Status)
	state.Node = types.StringValue(out.VM.Node)
	if out.VM.IP != nil {
		state.IP = types.StringValue(*out.VM.IP)
	} else {
		state.IP = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vmResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
	// 一期所有 schema 字段都 RequiresReplace；Update 不会被 framework 调用。
	// 留空 fn 防止 panic。二期接 admin PATCH endpoint 后再展开。
}

func (r *vmResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// admin DELETE /vms/{name} 走 trash → 30s 后 worker 真删（PLAN-034）
	if err := r.c.Do(ctx, "DELETE", fmt.Sprintf("/api/admin/vms/%s", state.Name.ValueString()), nil, nil); err != nil {
		resp.Diagnostics.AddError("delete vm failed", err.Error())
	}
}

// ImportState 接受 "cluster/name" 形式 ID（与 admin endpoint 路径一致）。
func (r *vmResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError("invalid import ID", "expected `cluster/name`, got: "+req.ID)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}
