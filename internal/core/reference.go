package core

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
)

const (
	// ReferenceTransaction identifies an order, payment, transaction, or receipt.
	ReferenceTransaction ReferenceRole = "transaction"
	// ReferenceLineage identifies the current purchase or subscription lineage.
	ReferenceLineage ReferenceRole = "lineage"
	// ReferenceLinkedLineage identifies a prior or related purchase lineage.
	ReferenceLinkedLineage ReferenceRole = "linked_lineage"
	// ReferenceQuery identifies a token or identifier used for authoritative lookups.
	ReferenceQuery ReferenceRole = "query"
	// ReferenceCustomerBinding identifies a provider-side application customer association.
	ReferenceCustomerBinding ReferenceRole = "customer_binding"
)

type ReferenceRole string

// StoreReference is an opaque provider identifier. Its value is deliberately
// private and redacted from string and structured log output because purchase
// tokens and customer bindings can be credentials or personal data.
type StoreReference struct {
	Role  ReferenceRole
	Kind  string
	value string
}

// NewStoreReference constructs and validates an opaque provider reference.
func NewStoreReference(role ReferenceRole, kind, value string) (StoreReference, error) {
	reference := StoreReference{Role: role, Kind: kind, value: value}
	if err := reference.Validate(); err != nil {
		return StoreReference{}, err
	}
	return reference, nil
}

// Validate checks that the reference role, kind, and private value are well formed.
func (reference StoreReference) Validate() error {
	if err := validateIdentifier("store reference role", string(reference.Role)); err != nil {
		return err
	}
	if err := validateIdentifier("store reference kind", reference.Kind); err != nil {
		return err
	}
	return validateIdentifier("store reference value", reference.value)
}

// Value returns the private provider value for explicit adapter or persistence use.
func (reference StoreReference) Value() string {
	return reference.value
}

// IsZero reports whether the reference contains no role, kind, or value.
func (reference StoreReference) IsZero() bool {
	return reference.Role == "" && reference.Kind == "" && reference.value == ""
}

// Equal compares two references without exposing their private values.
func (reference StoreReference) Equal(other StoreReference) bool {
	return reference.Role == other.Role &&
		reference.Kind == other.Kind &&
		subtle.ConstantTimeCompare([]byte(reference.value), []byte(other.value)) == 1
}

// String returns a redacted human-readable representation of the reference.
func (reference StoreReference) String() string {
	if reference.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s/%s:[REDACTED]", reference.Role, reference.Kind)
}

// LogValue returns a structured representation with the private value redacted.
func (reference StoreReference) LogValue() slog.Value {
	if reference.IsZero() {
		return slog.StringValue("")
	}
	return slog.GroupValue(
		slog.String("role", string(reference.Role)),
		slog.String("kind", reference.Kind),
		slog.String("value", "[REDACTED]"),
	)
}
