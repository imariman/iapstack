package revenuecatimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/imariman/iapstack/internal/core"
)

// Mapping is the operator file that names existing IAPStack entitlements and products.
type Mapping struct {
	ProjectID         string
	CanonicalIDColumn string
	Entitlements      map[string]string
	Products          []ProductMapping
}

// ProductMapping binds one RevenueCat store product to an existing application and product.
type ProductMapping struct {
	Store             string
	Environment       string
	ProviderProductID string
	ApplicationID     string
	ProductID         string
}

// ParseMapping decodes a mapping file and rejects unknown fields.
func ParseMapping(payload []byte) (Mapping, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var raw struct {
		ProjectID         string            `json:"project_id"`
		CanonicalIDColumn string            `json:"canonical_id_column"`
		Entitlements      map[string]string `json:"entitlements"`
		Products          []struct {
			Store             string `json:"store"`
			Environment       string `json:"environment"`
			ProviderProductID string `json:"provider_product_id"`
			ApplicationID     string `json:"application_id"`
			ProductID         string `json:"product_id"`
		} `json:"products"`
	}
	if err := decoder.Decode(&raw); err != nil {
		return Mapping{}, fmt.Errorf("decode revenuecat mapping: %w", ErrUsage)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Mapping{}, fmt.Errorf("decode revenuecat mapping trailer: %w", ErrUsage)
	}
	mapping := Mapping{
		ProjectID:         raw.ProjectID,
		CanonicalIDColumn: raw.CanonicalIDColumn,
		Entitlements:      raw.Entitlements,
		Products:          make([]ProductMapping, 0, len(raw.Products)),
	}
	for _, product := range raw.Products {
		mapping.Products = append(mapping.Products, ProductMapping{
			Store:             product.Store,
			Environment:       product.Environment,
			ProviderProductID: product.ProviderProductID,
			ApplicationID:     product.ApplicationID,
			ProductID:         product.ProductID,
		})
	}
	return mapping, nil
}

// Validate checks the mapping against one project and export format.
func (mapping Mapping) Validate(projectID core.ProjectID, format string) error {
	if mapping.ProjectID != string(projectID) {
		return fmt.Errorf("mapping project %q does not match %q: %w", mapping.ProjectID, projectID, ErrUsage)
	}
	switch format {
	case FormatTransactionsCSV:
		if mapping.CanonicalIDColumn != columnOriginalAppUserID && mapping.CanonicalIDColumn != columnLastSeenAlias {
			return fmt.Errorf("canonical id column is invalid: %w", ErrUsage)
		}
	case FormatSubscriberJSONL:
		if mapping.CanonicalIDColumn != "" {
			return fmt.Errorf("canonical id column is forbidden for subscriber jsonl: %w", ErrUsage)
		}
	default:
		return fmt.Errorf("unsupported revenuecat import format %q: %w", format, ErrUsage)
	}
	if len(mapping.Entitlements) == 0 || len(mapping.Products) == 0 {
		return fmt.Errorf("mapping entitlements and products are required: %w", ErrUsage)
	}
	for revenueCatID, key := range mapping.Entitlements {
		if strings.TrimSpace(revenueCatID) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(revenueCatID) != revenueCatID || strings.TrimSpace(key) != key {
			return fmt.Errorf("mapping entitlement %q is invalid: %w", revenueCatID, ErrUsage)
		}
	}
	seen := make(map[string]struct{}, len(mapping.Products))
	stores := make(map[string]struct{}, len(mapping.Products))
	for _, product := range mapping.Products {
		if product.Store == "" || strings.TrimSpace(product.Store) != product.Store {
			return fmt.Errorf("mapping store is invalid: %w", ErrUsage)
		}
		if product.Environment != string(core.EnvironmentProduction) && product.Environment != string(core.EnvironmentSandbox) {
			return fmt.Errorf("mapping environment %q is invalid: %w", product.Environment, ErrUsage)
		}
		if err := core.ValidateOpaqueIdentifier("provider product ID", product.ProviderProductID); err != nil {
			return fmt.Errorf("mapping provider product: %w", ErrUsage)
		}
		if err := core.ApplicationID(product.ApplicationID).Validate(); err != nil {
			return fmt.Errorf("mapping application: %w", ErrUsage)
		}
		if err := core.ProductID(product.ProductID).Validate(); err != nil {
			return fmt.Errorf("mapping product: %w", ErrUsage)
		}
		key := product.Store + "\x00" + product.Environment + "\x00" + product.ProviderProductID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate product mapping: %w", ErrUsage)
		}
		seen[key] = struct{}{}
		stores[product.Store] = struct{}{}
	}
	return nil
}

// productMapping returns the entry for one store, environment, and provider product id.
func (mapping Mapping) productMapping(store, environment, providerProductID string) (ProductMapping, bool) {
	for _, product := range mapping.Products {
		if product.Store == store && product.Environment == environment && product.ProviderProductID == providerProductID {
			return product, true
		}
	}
	return ProductMapping{}, false
}

// storeMapped reports whether any product entry names the store.
func (mapping Mapping) storeMapped(store string) bool {
	for _, product := range mapping.Products {
		if product.Store == store {
			return true
		}
	}
	return false
}

// productMappedOnOtherEnvironment reports a product entry whose environment differs.
func (mapping Mapping) productMappedOnOtherEnvironment(store, environment, providerProductID string) bool {
	for _, product := range mapping.Products {
		if product.Store == store && product.ProviderProductID == providerProductID && product.Environment != environment {
			return true
		}
	}
	return false
}
