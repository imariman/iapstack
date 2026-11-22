package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// ProviderReferencePurpose scopes protected identifiers exactly like verification persistence.
	ProviderReferencePurpose = "provider_reference"
	// searchPageTokenVersion identifies the opaque search cursor encoding.
	searchPageTokenVersion = 1
	// searchPageTokenMaxBytes bounds an opaque search cursor before it is decoded.
	searchPageTokenMaxBytes = 1024
	// ManualGrantStatusActive means the grant allows access now.
	ManualGrantStatusActive = "active"
	// ManualGrantStatusExpired means the grant reached its scheduled end.
	ManualGrantStatusExpired = "expired"
	// ManualGrantStatusRevoked means an administrator ended the grant early.
	ManualGrantStatusRevoked = "revoked"
)

// CustomerSearchInput selects one page of project customers. Limit is nil for the default page size.
type CustomerSearchInput struct {
	ProjectID core.ProjectID
	// Query is untrusted search text; it is trimmed before matching.
	Query     string
	Limit     *int
	PageToken string
}

// CustomerSearchPage is one page of matched customers.
type CustomerSearchPage struct {
	Customers     []CustomerMatch `json:"customers"`
	NextPageToken string          `json:"next_page_token,omitempty"`
}

// CustomerMatch is one customer summary and the reason it matched.
type CustomerMatch struct {
	Customer
	Match persistence.AdminCustomerMatch `json:"match,omitempty"`
}

// CustomerDetail is one customer closure for support work.
type CustomerDetail struct {
	GeneratedAt         time.Time             `json:"generated_at"`
	Customer            CustomerIdentity      `json:"customer"`
	CanonicalCustomerID core.CustomerID       `json:"canonical_customer_id"`
	Members             []CustomerIdentity    `json:"members"`
	Aliases             []CustomerAlias       `json:"aliases"`
	Merges              []CustomerMergeRecord `json:"merges"`
	Entitlements        []CustomerEntitlement `json:"entitlements"`
	ManualGrants        []ManualGrant         `json:"manual_grants"`
	GrantOptions        []GrantOption         `json:"grant_options"`
	Timeline            []TimelineEvent       `json:"timeline"`
	TimelineTruncated   bool                  `json:"timeline_truncated"`
}

// CustomerIdentity is one stored customer row.
type CustomerIdentity struct {
	ID                   core.CustomerID  `json:"id"`
	ExternalID           string           `json:"external_id"`
	MergedIntoCustomerID *core.CustomerID `json:"merged_into_customer_id,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
}

// CustomerAlias is one alias audit row.
type CustomerAlias struct {
	ExternalID          string                 `json:"external_id"`
	CanonicalCustomerID core.CustomerID        `json:"canonical_customer_id"`
	ActorRole           persistence.APIKeyRole `json:"actor_role"`
	ActorKeyID          string                 `json:"actor_key_id"`
	CreatedAt           time.Time              `json:"created_at"`
}

// CustomerMergeRecord is one merge audit row.
type CustomerMergeRecord struct {
	ID                  string          `json:"id"`
	CanonicalCustomerID core.CustomerID `json:"canonical_customer_id"`
	SourceCustomerID    core.CustomerID `json:"source_customer_id"`
	SourceExternalID    string          `json:"source_external_id"`
	ActorKeyID          string          `json:"actor_key_id"`
	CreatedAt           time.Time       `json:"created_at"`
}

// CustomerEntitlement is one current projection and the access source it cites.
// Access and reason are evaluated at generated_at, exactly like the public entitlement read.
type CustomerEntitlement struct {
	EntitlementID       core.EntitlementID     `json:"entitlement_id"`
	Key                 string                 `json:"key"`
	Source              persistence.SourceKind `json:"source"`
	SourceID            string                 `json:"source_id"`
	SourceApplicationID core.ApplicationID     `json:"source_application_id"`
	SourceProductID     core.ProductID         `json:"source_product_id"`
	Access              core.AccessStatus      `json:"access"`
	Reason              core.AccessReason      `json:"reason"`
	EffectiveStartsAt   *time.Time             `json:"effective_starts_at,omitempty"`
	EffectiveEndsAt     *time.Time             `json:"effective_ends_at,omitempty"`
	Version             int64                  `json:"version"`
	UpdatedAt           time.Time              `json:"updated_at"`
}

// ManualGrant is one audited operator grant.
type ManualGrant struct {
	ID                string             `json:"id"`
	ProjectID         core.ProjectID     `json:"project_id"`
	CustomerID        core.CustomerID    `json:"customer_id"`
	ApplicationID     core.ApplicationID `json:"application_id"`
	ProductID         core.ProductID     `json:"product_id"`
	EntitlementID     core.EntitlementID `json:"entitlement_id"`
	EntitlementKey    string             `json:"entitlement_key"`
	Reason            string             `json:"reason"`
	ActorKeyID        string             `json:"actor_key_id"`
	Status            string             `json:"status"`
	EffectiveStartsAt time.Time          `json:"effective_starts_at"`
	EffectiveEndsAt   time.Time          `json:"effective_ends_at"`
	RevokedAt         *time.Time         `json:"revoked_at,omitempty"`
	RevokedByKeyID    string             `json:"revoked_by_key_id,omitempty"`
	RevocationReason  string             `json:"revocation_reason,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
}

// GrantOption is one product and entitlement pair a manual grant may cite.
type GrantOption struct {
	EntitlementID  core.EntitlementID   `json:"entitlement_id"`
	EntitlementKey string               `json:"entitlement_key"`
	ProductID      core.ProductID       `json:"product_id"`
	ProductKind    core.ProductKind     `json:"product_kind"`
	ApplicationIDs []core.ApplicationID `json:"application_ids"`
}

// TimelineEvent is one stored record on the customer timeline.
type TimelineEvent struct {
	Kind                   persistence.AdminTimelineKind `json:"kind"`
	OccurredAt             time.Time                     `json:"occurred_at"`
	CustomerID             core.CustomerID               `json:"customer_id"`
	SourceID               string                        `json:"source_id,omitempty"`
	Source                 persistence.SourceKind        `json:"source,omitempty"`
	ApplicationID          core.ApplicationID            `json:"application_id,omitempty"`
	ProductID              core.ProductID                `json:"product_id,omitempty"`
	ProviderProductID      core.ProviderProductID        `json:"provider_product_id,omitempty"`
	EntitlementID          core.EntitlementID            `json:"entitlement_id,omitempty"`
	EntitlementKey         string                        `json:"entitlement_key,omitempty"`
	LifecycleState         core.LifecycleState           `json:"lifecycle_state,omitempty"`
	PreviousLifecycleState core.LifecycleState           `json:"previous_lifecycle_state,omitempty"`
	Access                 core.AccessStatus             `json:"access,omitempty"`
	AccessReason           core.AccessReason             `json:"access_reason,omitempty"`
	EffectiveEndsAt        *time.Time                    `json:"effective_ends_at,omitempty"`
	Version                int64                         `json:"version,omitempty"`
	Status                 string                        `json:"status,omitempty"`
	ErrorCode              string                        `json:"error_code,omitempty"`
	ActorRole              persistence.APIKeyRole        `json:"actor_role,omitempty"`
	ActorKeyID             string                        `json:"actor_key_id,omitempty"`
	ExternalID             string                        `json:"external_id,omitempty"`
	RelatedCustomerID      core.CustomerID               `json:"related_customer_id,omitempty"`
	Reason                 string                        `json:"reason,omitempty"`
	OriginStore            string                        `json:"origin_store,omitempty"`
}

// CustomerResolution is the canonical identity for one external id.
type CustomerResolution struct {
	ProjectID           core.ProjectID  `json:"project_id"`
	ExternalID          string          `json:"external_id"`
	CanonicalCustomerID core.CustomerID `json:"canonical_customer_id"`
	Match               string          `json:"match"`
}

// searchableReference is one provider reference scope that customer search may fingerprint.
type searchableReference struct {
	Role core.ReferenceRole
	Kind string
}

// SearchReferenceFingerprinter fingerprints one search text exactly like verification protects references.
type SearchReferenceFingerprinter struct {
	Protector protection.Protector
	Text      string
}

// searchPageToken is the decoded keyset cursor bound to one search text.
type searchPageToken struct {
	Version    int    `json:"v"`
	Rank       int    `json:"r"`
	CreatedAt  string `json:"t"`
	CustomerID string `json:"i"`
	QueryHash  string `json:"q"`
}

var (
	// searchableReferences lists the transaction and original transaction reference scopes adapters store.
	// Purchase tokens and customer bindings are deliberately not searchable.
	searchableReferences = []searchableReference{
		{Role: core.ReferenceTransaction, Kind: "transaction_id"},
		{Role: core.ReferenceTransaction, Kind: "order_id"},
		{Role: core.ReferenceLineage, Kind: "original_transaction_id"},
	}
)

// SearchCustomers returns one page of project customers matching an identifier or transaction id.
func (service *Service) SearchCustomers(
	ctx context.Context,
	actor Actor,
	input CustomerSearchInput,
) (CustomerSearchPage, error) {
	if err := actor.AuthorizeProject(input.ProjectID); err != nil {
		return CustomerSearchPage{}, err
	}
	if len(input.Query) > 2*persistence.AdminCustomerSearchMaxQueryBytes {
		return CustomerSearchPage{}, validation.Wrap(errors.New("search text is too long"))
	}
	query := strings.TrimSpace(input.Query)
	var queryErr error
	if len(query) > persistence.AdminCustomerSearchMaxQueryBytes {
		queryErr = errors.New("search text is too long")
	} else if query != "" {
		queryErr = core.ValidateOpaqueIdentifier("search text", query)
	}
	if queryErr != nil {
		return CustomerSearchPage{}, validation.Wrap(queryErr)
	}
	limit := 0
	if input.Limit != nil {
		if *input.Limit < 1 || *input.Limit > persistence.AdminCustomerSearchMaxLimit {
			return CustomerSearchPage{}, validation.Wrap(errors.New("search limit is out of range"))
		}
		limit = *input.Limit
	}
	after, err := DecodeSearchPageToken(input.PageToken, query)
	if err := errors.Join(input.ProjectID.Validate(), err); err != nil {
		return CustomerSearchPage{}, validation.Wrap(err)
	}
	if service.dependencies.Admin == nil {
		return CustomerSearchPage{}, errMissingDependency
	}
	search := persistence.AdminCustomerSearch{ProjectID: input.ProjectID, Query: query, Limit: limit, After: after}
	if query != "" && service.dependencies.Protection != nil {
		search.References = SearchReferenceFingerprinter{Protector: service.dependencies.Protection, Text: query}
	}
	page, err := service.dependencies.Admin.AdminSearchCustomers(ctx, search)
	if err != nil {
		return CustomerSearchPage{}, err
	}
	response := CustomerSearchPage{Customers: make([]CustomerMatch, 0, len(page.Results))}
	for _, result := range page.Results {
		response.Customers = append(response.Customers, CustomerMatch{
			Customer: CustomerView(result.Customer),
			Match:    result.Match,
		})
	}
	if page.Next != nil {
		response.NextPageToken = EncodeSearchPageToken(*page.Next, query)
	}
	return response, nil
}

// Customer returns identity, access sources, manual grants, and a bounded timeline for one customer.
func (service *Service) Customer(
	ctx context.Context,
	actor Actor,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (CustomerDetail, error) {
	if err := actor.AuthorizeProject(projectID); err != nil {
		return CustomerDetail{}, err
	}
	if err := errors.Join(projectID.Validate(), customerID.Validate()); err != nil {
		return CustomerDetail{}, validation.Wrap(err)
	}
	if service.dependencies.Admin == nil {
		return CustomerDetail{}, errMissingDependency
	}
	detail, err := service.dependencies.Admin.AdminCustomerDetail(ctx, projectID, customerID)
	if err != nil {
		return CustomerDetail{}, err
	}
	return CustomerDetailView(detail, service.now()), nil
}

// ResolveCustomer reads the canonical customer for one external id. An application actor may resolve
// only inside its own project, and only while its application still exists.
func (service *Service) ResolveCustomer(
	ctx context.Context,
	actor Actor,
	projectID core.ProjectID,
	externalID string,
) (CustomerResolution, error) {
	if err := actor.authorizeApplicationProject(projectID); err != nil {
		return CustomerResolution{}, err
	}
	if err := errors.Join(projectID.Validate(), core.ValidateExternalCustomerID(externalID)); err != nil {
		return CustomerResolution{}, validation.Wrap(err)
	}
	if service.dependencies.Store == nil {
		return CustomerResolution{}, errMissingDependency
	}
	var resolution persistence.CustomerResolution
	err := service.dependencies.Store.Transact(ctx, func(repository persistence.Transaction) error {
		if actor.Role == persistence.APIKeyRoleApplication {
			if _, loadErr := repository.Application(ctx, projectID, actor.ApplicationID); loadErr != nil {
				return loadErr
			}
		}
		var resolveErr error
		resolution, resolveErr = repository.ResolveCustomer(ctx, projectID, externalID)
		return resolveErr
	})
	if err != nil {
		return CustomerResolution{}, err
	}
	return CustomerResolutionView(resolution), nil
}

// ReferenceFingerprints derives the search text's keyed fingerprint in every searchable reference scope.
func (fingerprinter SearchReferenceFingerprinter) ReferenceFingerprints(
	ctx context.Context,
	projectID core.ProjectID,
	applicationIDs []core.ApplicationID,
) ([]persistence.AdminReferenceFingerprint, error) {
	fingerprints := make([]persistence.AdminReferenceFingerprint, 0, len(applicationIDs)*len(searchableReferences))
	for _, applicationID := range applicationIDs {
		for _, reference := range searchableReferences {
			plaintext := []byte(fingerprinter.Text)
			protected, err := protection.Protect(ctx, fingerprinter.Protector, protection.Scope{
				ProjectID:     projectID,
				ApplicationID: applicationID,
				Purpose:       ProviderReferencePurpose + ":" + string(reference.Role) + ":" + reference.Kind,
			}, plaintext)
			zero(plaintext)
			if err != nil {
				return nil, err
			}
			zero(protected.Ciphertext)
			fingerprints = append(fingerprints, persistence.AdminReferenceFingerprint{
				ApplicationID: applicationID,
				Role:          reference.Role,
				Kind:          reference.Kind,
				Fingerprint:   protected.Fingerprint,
			})
		}
	}
	return fingerprints, nil
}

// EncodeSearchPageToken returns an opaque cursor bound to the normalized search text.
func EncodeSearchPageToken(cursor persistence.AdminCustomerSearchCursor, query string) string {
	payload, _ := json.Marshal(searchPageToken{
		Version:    searchPageTokenVersion,
		Rank:       cursor.Rank,
		CreatedAt:  cursor.CreatedAt.UTC().Format(time.RFC3339Nano),
		CustomerID: string(cursor.CustomerID),
		QueryHash:  searchQueryHash(query),
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeSearchPageToken parses an opaque cursor and rejects one issued for different search text.
func DecodeSearchPageToken(token, query string) (*persistence.AdminCustomerSearchCursor, error) {
	if token == "" {
		return nil, nil
	}
	invalid := errors.New("search page token is invalid")
	if len(token) > searchPageTokenMaxBytes {
		return nil, invalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, invalid
	}
	var decoded searchPageToken
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Version != searchPageTokenVersion {
		return nil, invalid
	}
	if decoded.QueryHash != searchQueryHash(query) || decoded.Rank < 0 {
		return nil, invalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, decoded.CreatedAt)
	if err != nil || core.CustomerID(decoded.CustomerID).Validate() != nil {
		return nil, invalid
	}
	return &persistence.AdminCustomerSearchCursor{
		Rank: decoded.Rank, CreatedAt: createdAt, CustomerID: core.CustomerID(decoded.CustomerID),
	}, nil
}

// searchQueryHash returns a short non-reversible binding between a cursor and its search text.
func searchQueryHash(query string) string {
	digest := sha256.Sum256([]byte("iapstack-customer-search\x00" + query))
	return hex.EncodeToString(digest[:8])
}

// CustomerDetailView maps one customer closure and evaluates current access at now.
func CustomerDetailView(detail persistence.AdminCustomerDetail, now time.Time) CustomerDetail {
	response := CustomerDetail{
		GeneratedAt:         now,
		Customer:            customerIdentityView(detail.Customer),
		CanonicalCustomerID: detail.CanonicalCustomerID,
		Members:             make([]CustomerIdentity, 0, len(detail.Members)),
		Aliases:             make([]CustomerAlias, 0, len(detail.Aliases)),
		Merges:              make([]CustomerMergeRecord, 0, len(detail.Merges)),
		Entitlements:        make([]CustomerEntitlement, 0, len(detail.Entitlements)),
		ManualGrants:        make([]ManualGrant, 0, len(detail.ManualGrants)),
		GrantOptions:        make([]GrantOption, 0, len(detail.GrantOptions)),
		Timeline:            make([]TimelineEvent, 0, len(detail.Timeline)),
		TimelineTruncated:   detail.TimelineTruncated,
	}
	for _, member := range detail.Members {
		response.Members = append(response.Members, customerIdentityView(member))
	}
	for _, alias := range detail.Aliases {
		response.Aliases = append(response.Aliases, CustomerAlias{
			ExternalID: alias.ExternalID, CanonicalCustomerID: alias.CanonicalCustomerID,
			ActorRole: alias.ActorRole, ActorKeyID: alias.ActorKeyID, CreatedAt: alias.CreatedAt,
		})
	}
	for _, merge := range detail.Merges {
		response.Merges = append(response.Merges, CustomerMergeRecord{
			ID: merge.ID, CanonicalCustomerID: merge.CanonicalCustomerID, SourceCustomerID: merge.SourceCustomerID,
			SourceExternalID: merge.SourceExternalID, ActorKeyID: merge.ActorKeyID, CreatedAt: merge.CreatedAt,
		})
	}
	for _, entitlement := range detail.Entitlements {
		access, reason := core.AccessAt(
			entitlement.Access, entitlement.AccessReason, entitlement.EffectivePeriod.EndsAt, now,
		)
		var startsAt *time.Time
		if !entitlement.EffectivePeriod.StartsAt.IsZero() {
			value := entitlement.EffectivePeriod.StartsAt
			startsAt = &value
		}
		response.Entitlements = append(response.Entitlements, CustomerEntitlement{
			EntitlementID: entitlement.EntitlementID, Key: entitlement.Key,
			Source: entitlement.SourceKind, SourceID: entitlement.SourceID,
			SourceApplicationID: entitlement.SourceApplicationID, SourceProductID: entitlement.SourceProductID,
			Access: access, Reason: reason, EffectiveStartsAt: startsAt,
			EffectiveEndsAt: entitlement.EffectivePeriod.EndsAt, Version: entitlement.Version,
			UpdatedAt: entitlement.UpdatedAt,
		})
	}
	for _, grant := range detail.ManualGrants {
		response.ManualGrants = append(response.ManualGrants, ManualGrantView(grant, now))
	}
	for _, option := range detail.GrantOptions {
		applicationIDs := append([]core.ApplicationID{}, option.ApplicationIDs...)
		response.GrantOptions = append(response.GrantOptions, GrantOption{
			EntitlementID: option.EntitlementID, EntitlementKey: option.EntitlementKey,
			ProductID: option.ProductID, ProductKind: option.ProductKind, ApplicationIDs: applicationIDs,
		})
	}
	for _, event := range detail.Timeline {
		response.Timeline = append(response.Timeline, TimelineEvent{
			Kind: event.Kind, OccurredAt: event.OccurredAt, CustomerID: event.CustomerID,
			SourceID: event.SourceID, Source: event.SourceKind, ApplicationID: event.ApplicationID,
			ProductID: event.ProductID, ProviderProductID: event.ProviderProductID,
			EntitlementID: event.EntitlementID, EntitlementKey: event.EntitlementKey,
			LifecycleState: event.LifecycleState, PreviousLifecycleState: event.PreviousLifecycleState,
			Access: event.Access, AccessReason: event.AccessReason, EffectiveEndsAt: event.EffectiveEndsAt,
			Version: event.Version, Status: event.Status, ErrorCode: event.ErrorCode,
			ActorRole: event.ActorRole, ActorKeyID: event.ActorKeyID, ExternalID: event.ExternalID,
			RelatedCustomerID: event.RelatedCustomerID, Reason: event.Reason, OriginStore: event.OriginStore,
		})
	}
	return response
}

// customerIdentityView maps one stored customer row.
func customerIdentityView(identity persistence.AdminCustomerIdentity) CustomerIdentity {
	return CustomerIdentity{
		ID: identity.ID, ExternalID: identity.ExternalID,
		MergedIntoCustomerID: identity.MergedIntoCustomerID, CreatedAt: identity.CreatedAt,
	}
}

// ManualGrantView maps one stored grant and derives its status at now.
func ManualGrantView(grant persistence.ManualGrant, now time.Time) ManualGrant {
	status := ManualGrantStatusExpired
	switch {
	case grant.RevokedAt != nil:
		status = ManualGrantStatusRevoked
	case grant.Active(now):
		status = ManualGrantStatusActive
	}
	return ManualGrant{
		ID: grant.ID, ProjectID: grant.ProjectID, CustomerID: grant.CustomerID,
		ApplicationID: grant.ApplicationID, ProductID: grant.ProductID,
		EntitlementID: grant.EntitlementID, EntitlementKey: grant.EntitlementKey,
		Reason: grant.Reason, ActorKeyID: grant.ActorKeyID, Status: status,
		EffectiveStartsAt: grant.StartsAt, EffectiveEndsAt: grant.EndsAt,
		RevokedAt: grant.RevokedAt, RevokedByKeyID: grant.RevokedByKeyID,
		RevocationReason: grant.RevocationReason, CreatedAt: grant.CreatedAt,
	}
}

// CustomerResolutionView maps one stored resolution onto the public view.
func CustomerResolutionView(resolution persistence.CustomerResolution) CustomerResolution {
	return CustomerResolution{
		ProjectID:           resolution.ProjectID,
		ExternalID:          resolution.ExternalID,
		CanonicalCustomerID: resolution.CanonicalCustomerID,
		Match:               resolution.Match,
	}
}

// zero overwrites temporary search plaintext after protection.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
