package mcp

import (
	"context"

	"github.com/imariman/iapstack/contracts"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// OpenAPIResourceURI names the embedded v1 OpenAPI contract resource.
	OpenAPIResourceURI = "iapstack://openapi/v1"
	// openAPIMIMEType is the media type of the embedded contract.
	openAPIMIMEType = "application/yaml"
)

// addResources registers the read-only resources an agent may read without web access.
func addResources(server *sdk.Server) {
	server.AddResource(&sdk.Resource{
		URI:         OpenAPIResourceURI,
		Name:        "openapi-v1",
		Title:       "IAPStack v1 OpenAPI contract",
		Description: "The published OpenAPI 3.0 contract of the versioned IAPStack HTTP API this server runs.",
		MIMEType:    openAPIMIMEType,
		Size:        int64(len(contracts.OpenAPIV1)),
	}, func(ctx context.Context, _ *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		if _, authenticated := actorFromContext(ctx); !authenticated {
			return nil, sdk.ResourceNotFoundError(OpenAPIResourceURI)
		}
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{
			URI: OpenAPIResourceURI, MIMEType: openAPIMIMEType, Text: string(contracts.OpenAPIV1),
		}}}, nil
	})
}
