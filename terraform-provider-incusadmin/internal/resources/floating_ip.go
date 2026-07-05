package resources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/incuscloud/terraform-provider-incusadmin/internal/client"
)

// NewFloatingIPResource 注册 incusadmin_floating_ip 资源。
//
// P0 CR 修复（#4）：原方案用 portal /floating-ips/{id}/attach 不存在；
// portal 实际是 service-scoped /portal/services/{vm_id}/floating-ips/{fip_id}/attach。
// 改用 admin 路径 /admin/floating-ips/{id}/{attach,detach}（admin handler 真实存在）。
func NewFloatingIPResource() resource.Resource { return &fipResource{} }

type fipResource struct{ c *client.Client }

type fipModel struct {
	ID          types.Int64  `tfsdk:"id"`
	Cluster     types.String `tfsdk:"cluster"`
	IP          types.String `tfsdk:"ip"`
	VMID        types.Int64  `tfsdk:"vm_id"`
	Status      types.String `tfsdk:"status"`
	Description types.String `tfsdk:"description"`
}

func (r *fipResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "incusadmin_floating_ip"
}

func (r *fipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiresReplace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Floating IP 资源（admin endpoint）。分配需指定 cluster + ip；`vm_id` 变化时 attach/detach。",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true},
			// cluster + ip 为后端 Allocate 必填；分配后不可改（改则重建）。
			"cluster": schema.StringAttribute{
				Required:      true,
				Description:   "所属 cluster 名",
				PlanModifiers: requiresReplace,
			},
			"ip": schema.StringAttribute{
				Required:      true,
				Description:   "要认领的公网 IP（须属于该 cluster 的 Floating IP 池）",
				PlanModifiers: requiresReplace,
			},
			"vm_id":       schema.Int64Attribute{Optional: true, Computed: true},
			"status":      schema.StringAttribute{Computed: true},
			"description": schema.StringAttribute{Optional: true, Computed: true},
		},
	}
}

func (r *fipResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData != nil {
		r.c, _ = req.ProviderData.(*client.Client)
	}
}

func (r *fipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// 后端 Allocate 请求体：{ cluster(名), ip(必填), description }，响应 { floating_ip }。
	var out struct {
		FloatingIP client.FloatingIP `json:"floating_ip"`
	}
	if err := r.c.Do(ctx, "POST", "/api/admin/floating-ips", map[string]any{
		"cluster":     plan.Cluster.ValueString(),
		"ip":          plan.IP.ValueString(),
		"description": plan.Description.ValueString(),
	}, &out); err != nil {
		resp.Diagnostics.AddError("allocate floating ip failed", err.Error())
		return
	}
	plan.ID = types.Int64Value(out.FloatingIP.ID)
	plan.IP = types.StringValue(out.FloatingIP.IP)
	plan.Status = types.StringValue(out.FloatingIP.Status)
	plan.Description = types.StringValue(out.FloatingIP.Description)
	if !plan.VMID.IsNull() && !plan.VMID.IsUnknown() && plan.VMID.ValueInt64() > 0 {
		// admin attach: POST /admin/floating-ips/{id}/attach body {vm_id}
		if err := r.c.Do(ctx, "POST", fmt.Sprintf("/api/admin/floating-ips/%d/attach", out.FloatingIP.ID),
			map[string]any{"vm_id": plan.VMID.ValueInt64()}, nil); err != nil {
			resp.Diagnostics.AddError("attach floating ip failed", err.Error())
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *fipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		FloatingIPs []client.FloatingIP `json:"floating_ips"`
	}
	if err := r.c.Do(ctx, "GET", "/api/admin/floating-ips", nil, &out); err != nil {
		resp.Diagnostics.AddError("list floating ips failed", err.Error())
		return
	}
	for _, f := range out.FloatingIPs {
		if f.ID == state.ID.ValueInt64() {
			state.IP = types.StringValue(f.IP)
			state.Status = types.StringValue(f.Status)
			// 后端绑定字段为 bound_vm_id。
			if f.BoundVMID != nil {
				state.VMID = types.Int64Value(*f.BoundVMID)
			} else {
				state.VMID = types.Int64Null()
			}
			state.Description = types.StringValue(f.Description)
			// cluster 为 create-time 字段；list 响应仅含 cluster_id，无法反查名字，
			// 保留 state 原值不覆盖。
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *fipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state fipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.VMID.ValueInt64() != state.VMID.ValueInt64() {
		if state.VMID.ValueInt64() != 0 {
			_ = r.c.Do(ctx, "POST", fmt.Sprintf("/api/admin/floating-ips/%d/detach", state.ID.ValueInt64()), nil, nil)
		}
		if plan.VMID.ValueInt64() != 0 {
			if err := r.c.Do(ctx, "POST", fmt.Sprintf("/api/admin/floating-ips/%d/attach", state.ID.ValueInt64()),
				map[string]any{"vm_id": plan.VMID.ValueInt64()}, nil); err != nil {
				resp.Diagnostics.AddError("attach failed", err.Error())
				return
			}
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *fipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	// T7 幂等：404（已不存在）视为释放成功。
	if err := r.c.Do(ctx, "DELETE", fmt.Sprintf("/api/admin/floating-ips/%d", state.ID.ValueInt64()), nil, nil); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("release failed", err.Error())
	}
}

func (r *fipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
