// Package offering loads file-based presentation documents for public paywall reads.
package offering

import "github.com/imariman/iapstack/internal/core"

const (
	// MaxDocumentBytes is the largest offering file this process will read.
	MaxDocumentBytes = 64 << 10
	// documentSchemaVersion is the only offering document shape this loader accepts.
	documentSchemaVersion = 1
	// maxRevisionLength bounds a revision before it is copied into an ETag.
	maxRevisionLength = 128
)

// Document is the public versioned offering for one application.
type Document struct {
	SchemaVersion int                `json:"schema_version"`
	ApplicationID core.ApplicationID `json:"application_id"`
	Revision      string             `json:"revision"`
	Offerings     []Offering         `json:"offerings"`
}

// Offering is one named group of store packages.
type Offering struct {
	ID       string    `json:"id"`
	Packages []Package `json:"packages"`
}

// Package is one store product a client may present.
type Package struct {
	ID             string                 `json:"id"`
	StoreProductID core.ProviderProductID `json:"store_product_id"`
	DisplayKey     string                 `json:"display_key,omitempty"`
}
