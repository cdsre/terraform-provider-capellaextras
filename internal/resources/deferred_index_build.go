// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package resources

import (
	"context"
	"fmt"
	"strings"

	apiclient "github.com/cdsre/terraform-provider-capellaextras/api/client"
	"github.com/cdsre/terraform-provider-capellaextras/api/indexes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &DeferredIndexBuildResource{}
var _ resource.ResourceWithConfigure = &DeferredIndexBuildResource{}
var _ resource.ResourceWithModifyPlan = &DeferredIndexBuildResource{}

func NewDeferredIndexBuildResource() resource.Resource {
	return &DeferredIndexBuildResource{}
}

// DeferredIndexBuildResource manages deferred index builds.
type DeferredIndexBuildResource struct {
	client *apiclient.Client
}

// DeferredIndexBuildModel describes the resource data model.
type DeferredIndexBuildModel struct {
	Id                   types.String `tfsdk:"id"`
	OrganizationId       types.String `tfsdk:"organization_id"`
	ProjectId            types.String `tfsdk:"project_id"`
	ClusterId            types.String `tfsdk:"cluster_id"`
	BucketName           types.String `tfsdk:"bucket_name"`
	ScopeName            types.String `tfsdk:"scope_name"`
	CollectionName       types.String `tfsdk:"collection_name"`
	IndexNames           types.List   `tfsdk:"index_names"`
	BuildTriggerStatuses types.List   `tfsdk:"build_trigger_statuses"`
	IndexStatuses        types.Map    `tfsdk:"index_statuses"`
}

func (r *DeferredIndexBuildResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deferred_index_build"
}

func (r *DeferredIndexBuildResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages deferred index builds for Couchbase Capella query indexes. " +
			"Tracks index build statuses and triggers builds only for indexes that have not yet been built. " +
			"Unlike the `capellaextras_build_index` action, this resource participates in `terraform plan` " +
			"and automatically detects when indexes require building due to drift.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Composite identifier: `{organization_id}/{project_id}/{cluster_id}/{bucket_name}`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The organization ID where the indexes are located.",
				Required:            true,
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "The project ID where the indexes are located.",
				Required:            true,
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "The cluster ID where the indexes are located.",
				Required:            true,
			},
			"bucket_name": schema.StringAttribute{
				MarkdownDescription: "The bucket where the indexes are located.",
				Required:            true,
			},
			"scope_name": schema.StringAttribute{
				MarkdownDescription: "The scope where the indexes are located. Defaults to `_default`.",
				Optional:            true,
			},
			"collection_name": schema.StringAttribute{
				MarkdownDescription: "The collection where the indexes are located. Defaults to `_default`.",
				Optional:            true,
			},
			"index_names": schema.ListAttribute{
				ElementType:         types.StringType,
				MarkdownDescription: "The names of the deferred indexes to manage builds for.",
				Required:            true,
			},
			"build_trigger_statuses": schema.ListAttribute{
				ElementType: types.StringType,
				MarkdownDescription: "Index statuses that should trigger a deferred build. " +
					"Defaults to `[\"Created\"]`. Extend this list to include additional statuses " +
					"(e.g. error states) that should also trigger a rebuild.",
				Optional: true,
				Computed: true,
				Default: listdefault.StaticValue(types.ListValueMust(
					types.StringType,
					[]attr.Value{types.StringValue("Created")},
				)),
			},
			"index_statuses": schema.MapAttribute{
				ElementType: types.StringType,
				MarkdownDescription: "Current build status of each managed index, keyed by index name. " +
					"Updated after each apply and refreshed on `terraform plan`.",
				Computed: true,
			},
		},
	}
}

func (r *DeferredIndexBuildResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*apiclient.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *apiclient.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = client
}

// ModifyPlan marks index_statuses as unknown — forcing an Update — in two situations:
//
//  1. A stored status matches build_trigger_statuses (e.g. "Created" after an index
//     was recreated externally).
//
//  2. An index listed in index_names is absent from index_statuses.  This happens
//     when Read skipped the index because it returned 404 (deleted outside Terraform
//     and not yet recreated).  Flagging it here ensures the plan includes an update
//     for this resource so that — when the Capella provider recreates the index in
//     the same apply (ahead of this resource due to the dependency chain) — the build
//     is triggered in a single apply rather than requiring a second run.
func (r *DeferredIndexBuildResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Skip on create (no prior state) and destroy (no plan).
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var state DeferredIndexBuildModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var plan DeferredIndexBuildModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.BuildTriggerStatuses.IsUnknown() || state.IndexStatuses.IsNull() || state.IndexStatuses.IsUnknown() {
		return
	}

	var triggerStatuses []string
	resp.Diagnostics.Append(plan.BuildTriggerStatuses.ElementsAs(ctx, &triggerStatuses, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	triggerSet := make(map[string]bool, len(triggerStatuses))
	for _, s := range triggerStatuses {
		triggerSet[s] = true
	}

	statusMap := make(map[string]string)
	resp.Diagnostics.Append(state.IndexStatuses.ElementsAs(ctx, &statusMap, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check 1: any stored status is a build trigger.
	for _, status := range statusMap {
		if triggerSet[status] {
			resp.Diagnostics.Append(
				resp.Plan.SetAttribute(ctx, path.Root("index_statuses"), types.MapUnknown(types.StringType))...,
			)
			return
		}
	}

	// Check 2: any planned index name is absent from stored statuses.
	// A missing entry means Read skipped that index (404); it will be recreated by the
	// Capella provider in the same apply before this resource's Update runs.
	// Skip this check if index_names itself is unknown (Terraform already knows it will change).
	if plan.IndexNames.IsUnknown() {
		return
	}
	var planIndexNames []string
	diags := plan.IndexNames.ElementsAs(ctx, &planIndexNames, false)
	if diags.HasError() {
		// Some elements are unknown — index_names is changing anyway, so Terraform will call Update.
		return
	}
	for _, name := range planIndexNames {
		if _, exists := statusMap[name]; !exists {
			resp.Diagnostics.Append(
				resp.Plan.SetAttribute(ctx, path.Root("index_statuses"), types.MapUnknown(types.StringType))...,
			)
			return
		}
	}
}

func (r *DeferredIndexBuildResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data DeferredIndexBuildModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.performBuild(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *DeferredIndexBuildResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data DeferredIndexBuildModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope, collection := resolveDefaults(&data)

	var indexNames []string
	resp.Diagnostics.Append(data.IndexNames.ElementsAs(ctx, &indexNames, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	statusMap := make(map[string]attr.Value, len(indexNames))
	for _, indexName := range indexNames {
		res, err := indexes.GetIndexBuildStatus(ctx, r.client, &indexes.IndexBuildStatusRequest{
			OrganizationId: data.OrganizationId.ValueString(),
			ProjectId:      data.ProjectId.ValueString(),
			ClusterId:      data.ClusterId.ValueString(),
			Bucket:         data.BucketName.ValueString(),
			IndexName:      indexName,
			Scope:          scope,
			Collection:     collection,
		})
		if err != nil {
			if isNotFoundError(err) {
				// Index does not exist yet (e.g. deleted outside Terraform and not yet recreated).
				// Omit it from index_statuses so the plan can proceed; once the Capella provider
				// recreates it, the next Read will pick it up and ModifyPlan will trigger a build.
				continue
			}
			resp.Diagnostics.AddError(
				"Get Index Build Status Failed",
				fmt.Sprintf("Cannot get build status for index %q: %v", indexName, err),
			)
			return
		}
		statusMap[indexName] = types.StringValue(res.Status)
	}

	indexStatuses, diags := types.MapValue(types.StringType, statusMap)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.IndexStatuses = indexStatuses

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *DeferredIndexBuildResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data DeferredIndexBuildModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.performBuild(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Delete is a no-op: this resource does not own the underlying indexes.
func (r *DeferredIndexBuildResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// There is nothing to delete
}

// performBuild fetches current index statuses, triggers a build for any indexes whose status
// matches build_trigger_statuses, and stores the resulting statuses in data.IndexStatuses.
// Triggered indexes are recorded as "Building" in state without an extra API call.
func (r *DeferredIndexBuildResource) performBuild(ctx context.Context, data *DeferredIndexBuildModel, diagnostics *diag.Diagnostics) {
	scope, collection := resolveDefaults(data)

	var indexNames []string
	diagnostics.Append(data.IndexNames.ElementsAs(ctx, &indexNames, false)...)
	if diagnostics.HasError() {
		return
	}

	var triggerStatuses []string
	diagnostics.Append(data.BuildTriggerStatuses.ElementsAs(ctx, &triggerStatuses, false)...)
	if diagnostics.HasError() {
		return
	}

	triggerSet := make(map[string]bool, len(triggerStatuses))
	for _, s := range triggerStatuses {
		triggerSet[s] = true
	}

	statusMap := make(map[string]attr.Value, len(indexNames))
	var toBuild []string

	for _, indexName := range indexNames {
		res, err := indexes.GetIndexBuildStatus(ctx, r.client, &indexes.IndexBuildStatusRequest{
			OrganizationId: data.OrganizationId.ValueString(),
			ProjectId:      data.ProjectId.ValueString(),
			ClusterId:      data.ClusterId.ValueString(),
			Bucket:         data.BucketName.ValueString(),
			IndexName:      indexName,
			Scope:          scope,
			Collection:     collection,
		})
		if err != nil {
			if isNotFoundError(err) {
				// Index does not exist yet; skip it so other indexes can still be built.
				// It will appear in index_statuses once the Capella provider recreates it.
				continue
			}
			diagnostics.AddError(
				"Get Index Build Status Failed",
				fmt.Sprintf("Cannot get build status for index %q: %v", indexName, err),
			)
			return
		}
		statusMap[indexName] = types.StringValue(res.Status)
		if triggerSet[res.Status] {
			toBuild = append(toBuild, indexName)
		}
	}

	if len(toBuild) > 0 {
		_, err := indexes.BuildDeferredIndexes(ctx, r.client, &indexes.IndexBuildRequest{
			OrganizationId: data.OrganizationId.ValueString(),
			ProjectId:      data.ProjectId.ValueString(),
			ClusterId:      data.ClusterId.ValueString(),
			Bucket:         data.BucketName.ValueString(),
			Collection:     collection,
			Scope:          scope,
			IndexNames:     toBuild,
		})
		if err != nil {
			diagnostics.AddError(
				"Build Deferred Indexes Failed",
				fmt.Sprintf("Cannot build deferred indexes: %v", err),
			)
			return
		}

		// Reflect the triggered build in state without an extra API call.
		// Read() will correct this to the real status on the next plan refresh.
		for _, idx := range toBuild {
			statusMap[idx] = types.StringValue("Building")
		}
	}

	indexStatuses, diags := types.MapValue(types.StringType, statusMap)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return
	}
	data.IndexStatuses = indexStatuses
	data.Id = types.StringValue(fmt.Sprintf("%s/%s/%s/%s",
		data.OrganizationId.ValueString(),
		data.ProjectId.ValueString(),
		data.ClusterId.ValueString(),
		data.BucketName.ValueString(),
	))
}

// isNotFoundError reports whether an API error is an HTTP 404 (index does not exist yet).
func isNotFoundError(err error) bool {
	return strings.Contains(err.Error(), "status 404")
}

func resolveDefaults(data *DeferredIndexBuildModel) (scope, collection string) {
	if data.ScopeName.IsNull() || data.ScopeName.IsUnknown() {
		scope = "_default"
	} else {
		scope = data.ScopeName.ValueString()
	}
	if data.CollectionName.IsNull() || data.CollectionName.IsUnknown() {
		collection = "_default"
	} else {
		collection = data.CollectionName.ValueString()
	}
	return
}
