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
	requiresReplaceStr := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	requiresReplaceInt := []planmodifier.Int64{int64planmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Incus VM 资源（admin only，跳过订单流）。Import ID 形式 `cluster/name`。cpu/memory_mb/disk_gb/os_image 修改触发 ForceNew（incus-admin 不支持 in-place resize）；二期接 admin PATCH 端点后再放开。",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true},
			"cluster": schema.StringAttribute{
				Required:      true,
				Description:   "Cluster 名（与 incus-admin admin clusters 列表一致）",
				PlanModifiers: requiresReplaceStr,
			},
			// name 由后端 GenerateVMName 生成（CreateVM 忽略请求体的 name），
			// 故为 Computed：Create 后从 202 响应的 vm_name 回填。
			"name": schema.StringAttribute{
				Computed:    true,
				Description: "VM 名（后端自动生成，创建后回填）",
			},
			// cpu/memory_mb/disk_gb 补 RequiresReplace：改这三项必须重建（无 in-place resize）。
			"cpu":       schema.Int64Attribute{Required: true, PlanModifiers: requiresReplaceInt, Description: "vCPU 核数"},
			"memory_mb": schema.Int64Attribute{Required: true, PlanModifiers: requiresReplaceInt, Description: "内存 MB"},
			"disk_gb":   schema.Int64Attribute{Required: true, PlanModifiers: requiresReplaceInt, Description: "系统盘 GB"},
			"os_image": schema.StringAttribute{
				Required:      true,
				Description:   "OS 镜像 alias（如 ubuntu-22.04）",
				PlanModifiers: requiresReplaceStr,
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
// 注意：后端 CreateVM 用 GenerateVMName 生成 VM 名并忽略请求体的 name，
// 故此处不含 name 字段；VM 名由 202 响应的 vm_name 回填。
type adminCreateVMReq struct {
	CPU          int      `json:"cpu"`
	MemoryMB     int      `json:"memory_mb"`
	DiskGB       int      `json:"disk_gb"`
	OSImage      string   `json:"os_image,omitempty"`
	Project      string   `json:"project,omitempty"`
	SSHKeys      []string `json:"ssh_keys,omitempty"`
	TargetUserID int64    `json:"target_user_id,omitempty"`
	Count        int      `json:"count,omitempty"`
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
		Count:    1,
	}
	// 后端 CreateVM 走异步 jobs runtime，单 VM 返 202 +
	//   { status, job_id, vm_id, vm_name, ip }
	// name 由后端 GenerateVMName 生成，请求体的 name 被忽略；此处按响应真实字段回填。
	var out struct {
		Status string `json:"status"`
		JobID  int64  `json:"job_id"`
		VMID   int64  `json:"vm_id"`
		VMName string `json:"vm_name"`
		IP     string `json:"ip"`
	}
	cluster := plan.Cluster.ValueString()
	if err := r.c.Do(ctx, "POST", fmt.Sprintf("/api/admin/clusters/%s/vms", cluster), body, &out); err != nil {
		resp.Diagnostics.AddError("create vm failed", err.Error())
		return
	}
	plan.ID = types.Int64Value(out.VMID)
	plan.Name = types.StringValue(out.VMName) // 回填后端生成名
	if out.IP != "" {
		plan.IP = types.StringValue(out.IP)
	} else {
		plan.IP = types.StringNull()
	}
	if out.Status != "" {
		plan.Status = types.StringValue(out.Status)
	} else {
		plan.Status = types.StringValue("provisioning")
	}
	// 异步创建：node 尚未确定，交由后续 Read 从 db 行填充。
	plan.Node = types.StringNull()
	resp.Diagnostics.AddWarning(
		"vm creation queued",
		fmt.Sprintf("VM %s 创建已入队（job_id=%d）。Provider 不轮询 SSE；下次 plan/refresh 时 Read 自动同步 status/node。", out.VMName, out.JobID),
	)
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
	// admin GET /clusters/{name}/vms/{vmName} 返
	//   { vm, state, snapshots, project, db }
	// 其中 db 键承载持久化的 model.VM 行（cpu/memory_mb/disk_gb/os_image/status/node/ip）；
	// vm 键是脱敏后的 Incus 实例原始 JSON，不用于回填 Terraform 字段。
	var out struct {
		DB *client.VM `json:"db"`
	}
	if err := r.c.Do(ctx, "GET", fmt.Sprintf("/api/admin/clusters/%s/vms/%s", cluster, name), nil, &out); err != nil {
		// 404 → drift；让 framework 自动从 state remove
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("read vm failed", err.Error())
		return
	}
	if out.DB == nil {
		// Incus 有实例但 DB 无持久化行（漂移/手工创建）：无可回填的权威字段，视为 drift。
		resp.State.RemoveResource(ctx)
		return
	}
	db := out.DB
	state.ID = types.Int64Value(db.ID)
	state.Name = types.StringValue(db.Name)
	state.CPU = types.Int64Value(int64(db.CPU))
	state.MemoryMB = types.Int64Value(int64(db.MemoryMB))
	state.DiskGB = types.Int64Value(int64(db.DiskGB))
	state.OSImage = types.StringValue(db.OSImage)
	state.Status = types.StringValue(db.Status)
	state.Node = types.StringValue(db.Node)
	if db.IP != nil {
		state.IP = types.StringValue(*db.IP)
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
	// admin DELETE /vms/{name} 走 trash → 30s 后 worker 真删（PLAN-034）。
	// T7 幂等：404（已不存在）视为删除成功。
	if err := r.c.Do(ctx, "DELETE", fmt.Sprintf("/api/admin/vms/%s", state.Name.ValueString()), nil, nil); err != nil && !client.IsNotFound(err) {
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
