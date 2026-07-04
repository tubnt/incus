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

// NewSSHKeyResource 注册 incusadmin_ssh_key 资源。
//
// P0 CR 修复（#5）：原方案 PUT /portal/ssh-keys/{id} 不存在；portal sshkey 仅
// GET/POST/DELETE。`name` 改 RequiresReplace；移除 Update 路径（framework 仍要求
// 实现 Update 函数，留空即可，因所有字段都 ForceNew）。
func NewSSHKeyResource() resource.Resource { return &sshKeyResource{} }

type sshKeyResource struct{ c *client.Client }

type sshKeyModel struct {
	ID        types.Int64  `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	PublicKey types.String `tfsdk:"public_key"`
}

func (r *sshKeyResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "incusadmin_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiresReplace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "用户 SSH 公钥。所有字段修改触发 ForceNew（portal sshkey API 无 PUT 端点）。",
		Attributes: map[string]schema.Attribute{
			"id":         schema.Int64Attribute{Computed: true},
			"name":       schema.StringAttribute{Required: true, PlanModifiers: requiresReplace},
			"public_key": schema.StringAttribute{Required: true, PlanModifiers: requiresReplace},
		},
	}
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData != nil {
		r.c, _ = req.ProviderData.(*client.Client)
	}
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// 后端 Create 响应键为 "key"（单条）。
	var out struct {
		Key client.SSHKey `json:"key"`
	}
	if err := r.c.Do(ctx, "POST", "/api/portal/ssh-keys", client.SSHKey{
		Name:      plan.Name.ValueString(),
		PublicKey: plan.PublicKey.ValueString(),
	}, &out); err != nil {
		resp.Diagnostics.AddError("create ssh key failed", err.Error())
		return
	}
	plan.ID = types.Int64Value(out.Key.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// 后端 List 响应键为 "keys"（列表）。
	var out struct {
		Keys []client.SSHKey `json:"keys"`
	}
	if err := r.c.Do(ctx, "GET", "/api/portal/ssh-keys", nil, &out); err != nil {
		resp.Diagnostics.AddError("list ssh keys failed", err.Error())
		return
	}
	for _, k := range out.Keys {
		if k.ID == state.ID.ValueInt64() {
			state.Name = types.StringValue(k.Name)
			state.PublicKey = types.StringValue(k.PublicKey)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

// Update 留空：所有字段 RequiresReplace，framework 不会真调到这里。
func (r *sshKeyResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	// T7 幂等：404（已不存在）视为删除成功。
	if err := r.c.Do(ctx, "DELETE", fmt.Sprintf("/api/portal/ssh-keys/%d", state.ID.ValueInt64()), nil, nil); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("delete ssh key failed", err.Error())
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
