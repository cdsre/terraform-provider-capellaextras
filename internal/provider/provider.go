// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"
	"time"

	apiclient "github.com/cdsre/terraform-provider-capellaextras/api/client"
	"github.com/cdsre/terraform-provider-capellaextras/internal/actions"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure CapellaProvider satisfies various provider interfaces.
var _ provider.Provider = &CapellaProvider{}
var _ provider.ProviderWithFunctions = &CapellaProvider{}
var _ provider.ProviderWithEphemeralResources = &CapellaProvider{}
var _ provider.ProviderWithActions = &CapellaProvider{}

const (
	capellaAuthenticationTokenField = "authentication_token"
	capellaPublicAPIHostField       = "host"
	apiRequestTimeout               = 60 * time.Second
	defaultAPIHostURL               = "https://cloudapi.cloud.couchbase.com"
	providerName                    = "couchbase-capella"
)

// CapellaProvider defines the provider implementation.
type CapellaProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// CapellaProviderModel describes the provider data model.
type CapellaProviderModel struct {
	Host                types.String `tfsdk:"host"`
	AuthenticationToken types.String `tfsdk:"authentication_token"`
}

func (p *CapellaProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "capellaextras"
	resp.Version = p.version
}

func (p *CapellaProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Optional:    true,
				Description: "Capella Public API HTTPS Host URL",
			},
			"authentication_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Capella API Token that serves as an authentication mechanism.",
			},
		},
	}
}

func (p *CapellaProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config CapellaProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if config.Host.IsNull() {
		envHost, exists := os.LookupEnv("CAPELLA_HOST")
		if exists {
			config.Host = types.StringValue(envHost)
		} else {
			config.Host = types.StringValue(defaultAPIHostURL)
		}
	}

	if config.AuthenticationToken.IsNull() {
		config.AuthenticationToken = types.StringValue(os.Getenv("CAPELLA_AUTHENTICATION_TOKEN"))
	}

	if config.AuthenticationToken.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root(capellaAuthenticationTokenField),
			"Unknown Capella Authentication Token",
			"The provider cannot create the Capella API client as there is an unknown configuration value for the capella authentication token. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the CAPELLA_AUTHENTICATION_TOKEN environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// Set the host and authentication token to be used

	host := config.Host.ValueString()
	authenticationToken := config.AuthenticationToken.ValueString()

	// If any of the expected configurations are missing, return
	// error with provider-specific guidance.
	if host == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root(capellaPublicAPIHostField),
			"Missing Capella Public API Host",
			"The provider cannot create the Capella API client as there is a missing or empty value for the Capella API host. "+
				"Set the host value in the configuration or use the TF_VAR_host environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if authenticationToken == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root(capellaAuthenticationTokenField),
			"Missing Capella Authentication Token",
			"The provider cannot create the Capella API client as there is a missing or empty value for the capella authentication token. "+
				"Set the password value in the configuration or use the CAPELLA_AUTHENTICATION_TOKEN environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// Configuration values are now available.
	// if data.Endpoint.IsNull() { /* ... */ }

	// Example client configuration for data sources and resources
	client := apiclient.NewClient(
		apiclient.WithBaseURL(config.Host.ValueString()),
		apiclient.WithAuthenticator(apiclient.BearerTokenAuth{Token: config.AuthenticationToken.ValueString()}),
	)
	resp.DataSourceData = client
	resp.ResourceData = client
	resp.ActionData = client
}

func (p *CapellaProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *CapellaProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{}
}

func (p *CapellaProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func (p *CapellaProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{}
}

func (p *CapellaProvider) Actions(ctx context.Context) []func() action.Action {
	return []func() action.Action{
		actions.NewBuildIndexAction,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &CapellaProvider{
			version: version,
		}
	}
}
