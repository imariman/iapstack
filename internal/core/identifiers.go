// Package core contains the store-neutral IAPStack domain model.
package core

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ProjectID string
type ApplicationID string
type CustomerID string
type ProductID string
type EntitlementID string
type ObservationID string
type ProviderApplicationID string
type ProviderProductID string

// ValidateExternalCustomerID checks one application-owned customer identity.
func ValidateExternalCustomerID(value string) error {
	return validateIdentifier("external customer ID", value)
}

// Validate checks that the project ID is present and well formed.
func (id ProjectID) Validate() error {
	return validateIdentifier("project ID", string(id))
}

// Validate checks that the application ID is present and well formed.
func (id ApplicationID) Validate() error {
	return validateIdentifier("application ID", string(id))
}

// Validate checks that the customer ID is present and well formed.
func (id CustomerID) Validate() error {
	return validateIdentifier("customer ID", string(id))
}

// Validate checks that the product ID is present and well formed.
func (id ProductID) Validate() error {
	return validateIdentifier("product ID", string(id))
}

// Validate checks that the entitlement ID is present and well formed.
func (id EntitlementID) Validate() error {
	return validateIdentifier("entitlement ID", string(id))
}

// Validate checks that the observation ID is present and well formed.
func (id ObservationID) Validate() error {
	return validateIdentifier("observation ID", string(id))
}

// Validate checks that the provider application ID is present and well formed.
func (id ProviderApplicationID) Validate() error {
	return validateIdentifier("provider application ID", string(id))
}

// Validate checks that the provider product ID is present and well formed.
func (id ProviderProductID) Validate() error {
	return validateIdentifier("provider product ID", string(id))
}

// validateIdentifier enforces the shared safety rules for opaque string identifiers.
func validateIdentifier(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have leading or trailing whitespace", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
