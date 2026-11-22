// Package contracts embeds the published API contracts so the server can serve them to clients.
package contracts

import (
	_ "embed"
)

var (
	// OpenAPIV1 is the published versioned HTTP API contract, byte for byte.
	//
	//go:embed openapi/v1.yaml
	OpenAPIV1 []byte
)
