package mcp

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// definitionWriteStatus is the status a successful definition write reports, like the v1 mutation result.
	definitionWriteStatus = "ok"
)

// projectScopedInput is implemented by write tool inputs, so the call log names the project a write targeted.
type projectScopedInput interface {
	// loggedProjectID returns the project id the write names.
	loggedProjectID() string
}

// definitionInput is implemented by definition write inputs, so the call log names the definition a
// successful write created or confirmed. Grants and aliases store their own audit rows and log no ids.
type definitionInput interface {
	// loggedDefinition returns the operator-chosen id of the definition the write names.
	loggedDefinition() string
}

// createManualGrantInput asks for one audited grant for a relative duration or until an absolute end.
type createManualGrantInput struct {
	ProjectID      string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	CustomerID     string `json:"customer_id" jsonschema:"The internal customer id. A merged customer's grant is stored on its canonical customer."`
	IdempotencyKey string `json:"idempotency_key" jsonschema:"A key unique to this grant in the project, at most 200 characters. Repeating the call with the same key and arguments returns the stored grant as a replay; the same key with other arguments fails with conflict."`
	EntitlementID  string `json:"entitlement_id" jsonschema:"The entitlement to grant. The product must grant it; get_customer lists valid pairs in grant_options."`
	ProductID      string `json:"product_id" jsonschema:"The product the grant cites. It must not be a consumable and must be mapped to a store product of the application."`
	ApplicationID  string `json:"application_id" jsonschema:"The application whose webhook receives the entitlement change."`
	Reason         string `json:"reason" jsonschema:"Why the grant is made, for example a support ticket. One line, 1 to 500 characters, stored in the audit trail."`
	DurationDays   *int   `json:"duration_days,omitempty" jsonschema:"Grant length in days from now, from 1 to 3660. Give either duration_days or ends_at."`
	EndsAt         string `json:"ends_at,omitempty" jsonschema:"Absolute RFC 3339 end time in the future, at most 3660 days ahead. Give either duration_days or ends_at."`
}

// revokeManualGrantInput ends one grant early for a required reason.
type revokeManualGrantInput struct {
	ProjectID  string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	CustomerID string `json:"customer_id" jsonschema:"The customer the grant belongs to, or any customer merged with it."`
	GrantID    string `json:"grant_id" jsonschema:"The manual grant id (mgr-...), from get_customer."`
	Reason     string `json:"reason" jsonschema:"Why the grant ends early. One line, 1 to 500 characters, stored in the audit trail."`
}

// putEntitlementInput creates one entitlement definition or confirms the identical one.
type putEntitlementInput struct {
	ProjectID     string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	EntitlementID string `json:"entitlement_id" jsonschema:"The entitlement id."`
	Key           string `json:"key" jsonschema:"The public entitlement key apps check, for example premium."`
}

// putProductInput creates one product and its exact entitlement set or confirms the identical one.
type putProductInput struct {
	ProjectID      string   `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	ProductID      string   `json:"product_id" jsonschema:"The internal product id."`
	Kind           string   `json:"kind" jsonschema:"subscription, non_consumable, or consumable."`
	EntitlementIDs []string `json:"entitlement_ids" jsonschema:"The complete set of entitlement ids the product grants. Each must already exist."`
}

// putStoreProductInput maps one provider product of an application to an internal product.
type putStoreProductInput struct {
	ProjectID         string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	ApplicationID     string `json:"application_id" jsonschema:"The application the provider product belongs to."`
	ProviderProductID string `json:"provider_product_id" jsonschema:"The product id in the store, for example the App Store product identifier."`
	ProductID         string `json:"product_id" jsonschema:"The internal product the store product maps to. It must already exist."`
}

// putCustomerAliasInput attaches one external id to a canonical customer.
type putCustomerAliasInput struct {
	ProjectID  string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	CustomerID string `json:"customer_id" jsonschema:"The canonical internal customer id the alias resolves to."`
	ExternalID string `json:"external_id" jsonschema:"The additional external customer id. It must not already belong to another customer."`
}

// definitionWriteOutput reports a stored or confirmed definition, like the v1 mutation result.
type definitionWriteOutput struct {
	Status string `json:"status"`
}

var (
	// errInvalidEndsAt rejects an ends_at value that is not an RFC 3339 time.
	errInvalidEndsAt = errors.New("manual grant ends_at is not an RFC 3339 time")
)

// writeToolDefinitions returns the write tools, offered only to keys that may write.
func writeToolDefinitions(service *controlplane.Service, logger *slog.Logger) []toolDefinition {
	return []toolDefinition{
		defineTool(&sdk.Tool{
			Name:  "create_manual_grant",
			Title: "Create manual grant",
			Description: "Grants one entitlement to a customer for a duration or until an end time, citing a product mapped " +
				"to the application, and records the reason and the acting key in the audit trail. The customer's " +
				"entitlements are recomputed and the application's webhook is notified. Use get_customer first to " +
				"see grant_options. Requires a key with write access.",
			Annotations: writeAnnotations(false),
		}, logger, manualGrantBounds, func(ctx context.Context, actor controlplane.Actor, input createManualGrantInput) (controlplane.ManualGrantResult, error) {
			var endsAt *time.Time
			if input.EndsAt != "" {
				parsed, err := time.Parse(time.RFC3339, input.EndsAt)
				if err != nil {
					return controlplane.ManualGrantResult{}, validation.Wrap(errInvalidEndsAt)
				}
				endsAt = &parsed
			}
			return service.CreateManualGrant(ctx, actor, controlplane.ManualGrantInput{
				ProjectID: core.ProjectID(input.ProjectID), CustomerID: core.CustomerID(input.CustomerID),
				IdempotencyKey: input.IdempotencyKey, EntitlementID: core.EntitlementID(input.EntitlementID),
				ProductID: core.ProductID(input.ProductID), ApplicationID: core.ApplicationID(input.ApplicationID),
				Reason: input.Reason, DurationDays: input.DurationDays, EndsAt: endsAt,
			})
		}),
		defineTool(&sdk.Tool{
			Name:  "revoke_manual_grant",
			Title: "Revoke manual grant",
			Description: "Ends one manual grant early, records the reason and the acting key in the audit trail, recomputes " +
				"the customer's entitlements, and notifies the application's webhook. The customer loses access that the " +
				"grant gave, and a revocation cannot be undone. Revoking a revoked grant returns the stored revocation. " +
				"Requires a key with write access.",
			Annotations: writeAnnotations(true),
		}, logger, reasonBounds, func(ctx context.Context, actor controlplane.Actor, input revokeManualGrantInput) (controlplane.ManualGrantResult, error) {
			return service.RevokeManualGrant(ctx, actor, controlplane.ManualGrantRevocationInput{
				ProjectID: core.ProjectID(input.ProjectID), CustomerID: core.CustomerID(input.CustomerID),
				GrantID: input.GrantID, Reason: input.Reason,
			})
		}),
		defineTool(&sdk.Tool{
			Name:  "put_entitlement",
			Title: "Put entitlement",
			Description: "Creates one entitlement definition, or confirms it when the same id already has the same key. A " +
				"different key for an existing id fails with conflict; nothing is overwritten. Requires a key with write access.",
			Annotations: writeAnnotations(false),
		}, logger, nil, func(ctx context.Context, actor controlplane.Actor, input putEntitlementInput) (definitionWriteOutput, error) {
			err := service.PutEntitlement(ctx, actor, controlplane.EntitlementInput{
				ProjectID: core.ProjectID(input.ProjectID), EntitlementID: core.EntitlementID(input.EntitlementID),
				Key: input.Key,
			})
			return definitionWriteResult(err)
		}),
		defineTool(&sdk.Tool{
			Name:  "put_product",
			Title: "Put product",
			Description: "Creates one product with its kind and the exact set of entitlements it grants, or confirms it when " +
				"the same id already has the same kind and set. A different kind or set fails with conflict; nothing is " +
				"overwritten. Requires a key with write access.",
			Annotations: writeAnnotations(false),
		}, logger, productSchema, func(ctx context.Context, actor controlplane.Actor, input putProductInput) (definitionWriteOutput, error) {
			entitlementIDs := make([]core.EntitlementID, 0, len(input.EntitlementIDs))
			for _, entitlementID := range input.EntitlementIDs {
				entitlementIDs = append(entitlementIDs, core.EntitlementID(entitlementID))
			}
			err := service.PutProduct(ctx, actor, controlplane.ProductInput{
				ProjectID: core.ProjectID(input.ProjectID), ProductID: core.ProductID(input.ProductID),
				Kind: core.ProductKind(input.Kind), EntitlementIDs: entitlementIDs,
			})
			return definitionWriteResult(err)
		}),
		defineTool(&sdk.Tool{
			Name:  "put_store_product",
			Title: "Put store product",
			Description: "Maps one store product of an application to an internal product, or confirms the identical " +
				"mapping. A mapping to a different product fails with conflict; nothing is overwritten. Requires a key " +
				"with write access.",
			Annotations: writeAnnotations(false),
		}, logger, nil, func(ctx context.Context, actor controlplane.Actor, input putStoreProductInput) (definitionWriteOutput, error) {
			err := service.PutStoreProduct(ctx, actor, controlplane.StoreProductInput{
				ProjectID: core.ProjectID(input.ProjectID), ApplicationID: core.ApplicationID(input.ApplicationID),
				ProviderProductID: core.ProviderProductID(input.ProviderProductID), ProductID: core.ProductID(input.ProductID),
			})
			return definitionWriteResult(err)
		}),
		defineTool(&sdk.Tool{
			Name:  "put_customer_alias",
			Title: "Put customer alias",
			Description: "Attaches an additional external customer id to a canonical customer and records the acting key in " +
				"the audit trail, or confirms an alias that already resolves to that customer. An external id that " +
				"belongs to another customer fails with conflict. Requires a key with write access.",
			Annotations: writeAnnotations(false),
		}, logger, nil, func(ctx context.Context, actor controlplane.Actor, input putCustomerAliasInput) (controlplane.CustomerResolution, error) {
			return service.PutCustomerAlias(ctx, actor, controlplane.CustomerAliasInput{
				ProjectID: core.ProjectID(input.ProjectID), CustomerID: core.CustomerID(input.CustomerID),
				ExternalID: input.ExternalID,
			})
		}),
	}
}

// loggedProjectID returns the project the grant is written in.
func (input createManualGrantInput) loggedProjectID() string {
	return input.ProjectID
}

// loggedProjectID returns the project the revocation is written in.
func (input revokeManualGrantInput) loggedProjectID() string {
	return input.ProjectID
}

// loggedProjectID returns the project the entitlement is written in.
func (input putEntitlementInput) loggedProjectID() string {
	return input.ProjectID
}

// loggedProjectID returns the project the product is written in.
func (input putProductInput) loggedProjectID() string {
	return input.ProjectID
}

// loggedProjectID returns the project the store product mapping is written in.
func (input putStoreProductInput) loggedProjectID() string {
	return input.ProjectID
}

// loggedProjectID returns the project the alias is written in.
func (input putCustomerAliasInput) loggedProjectID() string {
	return input.ProjectID
}

// loggedDefinition returns the entitlement id.
func (input putEntitlementInput) loggedDefinition() string {
	return input.EntitlementID
}

// loggedDefinition returns the product id.
func (input putProductInput) loggedDefinition() string {
	return input.ProductID
}

// loggedDefinition returns the application and the store product id it maps.
func (input putStoreProductInput) loggedDefinition() string {
	return input.ApplicationID + "/" + input.ProviderProductID
}

// writeAnnotations marks a closed-world write. Every write tool is idempotent: a repeated call with the same
// arguments replays or confirms the stored state. Only a write that removes access is destructive.
func writeAnnotations(destructive bool) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{
		ReadOnlyHint: false, DestructiveHint: boolPointer(destructive), IdempotentHint: true,
		OpenWorldHint: boolPointer(false),
	}
}

// definitionWriteResult maps a definition write error onto the tool result.
func definitionWriteResult(err error) (definitionWriteOutput, error) {
	if err != nil {
		return definitionWriteOutput{}, err
	}
	return definitionWriteOutput{Status: definitionWriteStatus}, nil
}

// manualGrantBounds documents the reason length, duration range, and end time format in the input schema.
func manualGrantBounds(schema *jsonschema.Schema) {
	reasonBounds(schema)
	setIntegerBounds(schema, "duration_days", 1, controlplane.ManualGrantMaxDurationDays)
	setStringLength(schema, "idempotency_key", 1, persistence.ManualGrantIdempotencyKeyMaxRunes)
	if property := schema.Properties["ends_at"]; property != nil {
		property.Format = "date-time"
	}
}

// reasonBounds documents the audit reason length in the input schema.
func reasonBounds(schema *jsonschema.Schema) {
	setStringLength(schema, "reason", 1, persistence.ManualGrantReasonMaxRunes)
}

// productSchema documents the product kinds and requires an entitlement id array, never null.
func productSchema(schema *jsonschema.Schema) {
	if property := schema.Properties["kind"]; property != nil {
		property.Enum = []any{
			string(core.ProductKindSubscription), string(core.ProductKindNonConsumable), string(core.ProductKindConsumable),
		}
	}
	if property := schema.Properties["entitlement_ids"]; property != nil {
		property.Types = nil
		property.Type = "array"
	}
}

// setStringLength sets the inclusive minimum and maximum length of one string property.
func setStringLength(schema *jsonschema.Schema, name string, minimum, maximum int) {
	property := schema.Properties[name]
	if property == nil {
		panic("input schema has no " + name + " property")
	}
	low, high := minimum, maximum
	property.MinLength = &low
	property.MaxLength = &high
}
