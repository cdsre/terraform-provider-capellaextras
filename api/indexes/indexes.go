package indexes

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	apiclient "github.com/cdsre/terraform-provider-capellaextras/api/client"
)

type IndexBuildStatusResponse struct {
	Status string
}

type IndexBuildStatusRequest struct {
	OrganizationId string
	ProjectId      string
	ClusterId      string
	Bucket         string
	IndexName      string
	Scope          string
	Collection     string
}

type IndexBuildRequest struct {
	OrganizationId string
	ProjectId      string
	ClusterId      string
	Bucket         string
	IndexNames     []string
	Scope          string
	Collection     string
}
type IndexDefinition struct {
	Definition string
}

type IndexBuildResponse struct {
	Error *string `json:"error,omitempty"`
}

func GetIndexBuildStatus(ctx context.Context, c *apiclient.Client, req *IndexBuildStatusRequest) (*IndexBuildStatusResponse, error) {
	var res *IndexBuildStatusResponse
	path := fmt.Sprintf("v4/organizations/%s/projects/%s/clusters/%s/queryService/indexBuildStatus/%s",
		req.OrganizationId,
		req.ProjectId,
		req.ClusterId,
		url.PathEscape(req.IndexName),
	)
	params := map[string]string{
		"bucket":     req.Bucket,
		"scope":      req.Scope,
		"collection": req.Collection,
	}

	_, err := c.Get(ctx, path, params, &res)
	return res, err
}

func BuildDeferredIndexes(ctx context.Context, c *apiclient.Client, req *IndexBuildRequest) (*IndexBuildResponse, error) {
	var res *IndexBuildResponse
	def := IndexDefinition{Definition: fmt.Sprintf(
		"BUILD INDEX ON `%s`.`%s`.`%s`(%s)",
		req.Bucket,
		req.Scope,
		req.Collection,
		strings.Join(req.IndexNames, ", "),
	)}

	path := fmt.Sprintf("v4/organizations/%s/projects/%s/clusters/%s/queryService/indexes",
		req.OrganizationId,
		req.ProjectId,
		req.ClusterId,
	)
	_, err := c.Post(ctx, path, def, &res)
	return res, err
}
