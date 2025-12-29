// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package actions

import (
	"context"
	"fmt"

	apiclient "github.com/cdsre/terraform-provider-capellaextras/api/client"
	"github.com/cdsre/terraform-provider-capellaextras/api/indexes"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ action.Action = &BuildIndexAction{}
var _ action.ActionWithConfigure = &BuildIndexAction{}

func NewBuildIndexAction() action.Action {
	return &BuildIndexAction{}
}

// BuildIndexAction defines the action implementation.
type BuildIndexAction struct {
	*apiclient.Client
}

// BuildIndexActionModel describes the action data model.
type BuildIndexActionModel struct {
	OrganizationId types.String `tfsdk:"organization_id"`
	ProjectId      types.String `tfsdk:"project_id"`
	ClusterId      types.String `tfsdk:"cluster_id"`
	BucketName     types.String `tfsdk:"bucket_name"`
	IndexNames     types.List   `tfsdk:"index_names"`
	ScopeName      types.String `tfsdk:"scope_name"`
	CollectionName types.String `tfsdk:"collection_name"`
}

func (bi *BuildIndexAction) Metadata(ctx context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_build_index"
}

func (bi *BuildIndexAction) Schema(ctx context.Context, req action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Triggers the building of an index when its in the created state.",

		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The organization id where the index is located.",
				Required:            true,
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "The project id where the index is located.",
				Required:            true,
			},
			"cluster_id": schema.StringAttribute{
				MarkdownDescription: "The cluster id where the index is located.",
				Required:            true,
			},
			"bucket_name": schema.StringAttribute{
				MarkdownDescription: "The bucket name where the index is located.",
				Required:            true,
			},
			"index_names": schema.ListAttribute{
				ElementType:         types.StringType,
				MarkdownDescription: "The name of the index to build.",
				Required:            true,
			},
			"collection_name": schema.StringAttribute{
				MarkdownDescription: "The name of the collection where the index is located.",
				Optional:            true,
			},
			"scope_name": schema.StringAttribute{
				MarkdownDescription: "The name of the scope where the index is located.",
				Optional:            true,
			},
		},
	}
}

func (bi *BuildIndexAction) Configure(ctx context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*apiclient.Client)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *providerschema.Data, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	bi.Client = client
}

func (bi *BuildIndexAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data BuildIndexActionModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Set default values for optional attributes
	var scope, collection string
	if data.ScopeName.IsNull() {
		scope = "_default"
	} else {
		scope = data.ScopeName.ValueString()
	}
	if data.CollectionName.IsNull() {
		collection = "_default"
	} else {
		collection = data.CollectionName.ValueString()
	}

	var indexNames []string
	diags := data.IndexNames.ElementsAs(ctx, &indexNames, false)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var buildIndexes []string
	for _, indexName := range indexNames {
		ibReq := indexes.IndexBuildStatusRequest{
			OrganizationId: data.OrganizationId.ValueString(),
			ProjectId:      data.ProjectId.ValueString(),
			ClusterId:      data.ClusterId.ValueString(),
			Bucket:         data.BucketName.ValueString(),
			IndexName:      indexName,
			Scope:          scope,
			Collection:     collection,
		}
		res, err := indexes.GetIndexBuildStatus(ctx, bi.Client, &ibReq)
		if err != nil {
			resp.Diagnostics.AddError(
				"Get Index Build Status Failed",
				fmt.Sprintf("Cannot get index build status for index %s.  Error: %v\n", indexName, err.Error()),
			)
			return
		}

		// Send a progress message back to Terraform
		resp.SendProgress(action.InvokeProgressEvent{
			Message: fmt.Sprintf("Index: %s, Status: %s", indexName, res.Status),
		})

		if res.Status == "Scheduled for Creation" {
			res, err = indexes.WaitForScheduledIndexCreation(ctx, bi.Client, &ibReq)
			if err != nil {
				resp.Diagnostics.AddError(
					"Wait for Scheduled Index Creation Failed",
					fmt.Sprintf("Cannot wait for scheduled index creation for index %s.  Error: %v\n", indexName, err.Error()),
				)
				return
			}
		}

		if res.Status == "Created" {
			buildIndexes = append(buildIndexes, indexName)
		}
	}

	if len(buildIndexes) == 0 {
		// Send a progress message back to Terraform
		resp.SendProgress(action.InvokeProgressEvent{
			Message: "No indexes need built",
		})
		return
	}

	// Send a progress message back to Terraform
	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("The following indexes need built: %v", buildIndexes),
	})

	_, err := indexes.BuildDeferredIndexes(ctx, bi.Client, &indexes.IndexBuildRequest{
		OrganizationId: data.OrganizationId.ValueString(),
		ProjectId:      data.ProjectId.ValueString(),
		ClusterId:      data.ClusterId.ValueString(),
		Bucket:         data.BucketName.ValueString(),
		Collection:     collection,
		Scope:          scope,
		IndexNames:     buildIndexes,
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Build Deferred Indexes Failed",
			fmt.Sprintf("Cannot build deferred indexes.  Error: %v\n", err.Error()),
		)
		return
	}

	// Send a progress message back to Terraform
	resp.SendProgress(action.InvokeProgressEvent{
		Message: "finished action invocation, Indexes will be built in the background.",
	})

}
