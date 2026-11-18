package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/validation"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// manualGrantRevokeMarker is the custom-method suffix of the revoke route.
	manualGrantRevokeMarker = ":revoke"
	// manualGrantMaxDurationDays bounds a relative grant duration.
	manualGrantMaxDurationDays = 3660
	// searchPageTokenVersion identifies the opaque search cursor encoding.
	searchPageTokenVersion = 1
	// manualGrantStatusActive means the grant allows access now.
	manualGrantStatusActive = "active"
	// manualGrantStatusExpired means the grant reached its scheduled end.
	manualGrantStatusExpired = "expired"
	// manualGrantStatusRevoked means an administrator ended the grant early.
	manualGrantStatusRevoked = "revoked"
)

// searchableReference is one provider reference scope that customer search may fingerprint.
type searchableReference struct {
	Role core.ReferenceRole
	Kind string
}

// searchReferenceFingerprinter fingerprints one search text exactly like verification protects references.
type searchReferenceFingerprinter struct {
	protector protection.Protector
	text      string
}

// searchPageToken is the decoded keyset cursor bound to one search text.
type searchPageToken struct {
	Version    int    `json:"v"`
	Rank       int    `json:"r"`
	CreatedAt  string `json:"t"`
	CustomerID string `json:"i"`
	QueryHash  string `json:"q"`
}

// adminCustomerSearchResponse is one page of matched customers.
type adminCustomerSearchResponse struct {
	Customers     []adminCustomerMatchResponse `json:"customers"`
	NextPageToken string                       `json:"next_page_token,omitempty"`
}

// adminCustomerMatchResponse is one customer summary and the reason it matched.
type adminCustomerMatchResponse struct {
	adminCustomerResponse
	Match persistence.AdminCustomerMatch `json:"match,omitempty"`
}

// adminCustomerDetailResponse is one customer closure for support work.
type adminCustomerDetailResponse struct {
	GeneratedAt         time.Time                          `json:"generated_at"`
	Customer            adminCustomerIdentityResponse      `json:"customer"`
	CanonicalCustomerID core.CustomerID                    `json:"canonical_customer_id"`
	Members             []adminCustomerIdentityResponse    `json:"members"`
	Aliases             []adminCustomerAliasResponse       `json:"aliases"`
	Merges              []adminCustomerMergeRecordResponse `json:"merges"`
	Entitlements        []adminCustomerEntitlementResponse `json:"entitlements"`
	ManualGrants        []manualGrantResponse              `json:"manual_grants"`
	GrantOptions        []adminGrantOptionResponse         `json:"grant_options"`
	Timeline            []adminTimelineEventResponse       `json:"timeline"`
	TimelineTruncated   bool                               `json:"timeline_truncated"`
}

// adminCustomerIdentityResponse is one stored customer row.
type adminCustomerIdentityResponse struct {
	ID                   core.CustomerID  `json:"id"`
	ExternalID           string           `json:"external_id"`
	MergedIntoCustomerID *core.CustomerID `json:"merged_into_customer_id,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
}

// adminCustomerAliasResponse is one alias audit row.
type adminCustomerAliasResponse struct {
	ExternalID          string                 `json:"external_id"`
	CanonicalCustomerID core.CustomerID        `json:"canonical_customer_id"`
	ActorRole           persistence.APIKeyRole `json:"actor_role"`
	ActorKeyID          string                 `json:"actor_key_id"`
	CreatedAt           time.Time              `json:"created_at"`
}

// adminCustomerMergeRecordResponse is one merge audit row.
type adminCustomerMergeRecordResponse struct {
	ID                  string          `json:"id"`
	CanonicalCustomerID core.CustomerID `json:"canonical_customer_id"`
	SourceCustomerID    core.CustomerID `json:"source_customer_id"`
	SourceExternalID    string          `json:"source_external_id"`
	ActorKeyID          string          `json:"actor_key_id"`
	CreatedAt           time.Time       `json:"created_at"`
}

// adminCustomerEntitlementResponse is one current projection and the access source it cites.
// Access and reason are evaluated at generated_at, exactly like the public entitlement read.
type adminCustomerEntitlementResponse struct {
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

// manualGrantResponse is one audited operator grant.
type manualGrantResponse struct {
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

// adminGrantOptionResponse is one product and entitlement pair a manual grant may cite.
type adminGrantOptionResponse struct {
	EntitlementID  core.EntitlementID   `json:"entitlement_id"`
	EntitlementKey string               `json:"entitlement_key"`
	ProductID      core.ProductID       `json:"product_id"`
	ProductKind    core.ProductKind     `json:"product_kind"`
	ApplicationIDs []core.ApplicationID `json:"application_ids"`
}

// adminTimelineEventResponse is one stored record on the customer timeline.
type adminTimelineEventResponse struct {
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

// manualGrantRequest asks for one audited grant for a relative duration or until an absolute end.
type manualGrantRequest struct {
	IdempotencyKey string             `json:"idempotency_key"`
	EntitlementID  core.EntitlementID `json:"entitlement_id"`
	ProductID      core.ProductID     `json:"product_id"`
	ApplicationID  core.ApplicationID `json:"application_id"`
	Reason         string             `json:"reason"`
	DurationDays   *int               `json:"duration_days,omitempty"`
	EndsAt         *time.Time         `json:"ends_at,omitempty"`
}

// manualGrantRevocationRequest carries the required reason for an early revocation.
type manualGrantRevocationRequest struct {
	Reason string `json:"reason"`
}

// manualGrantResultResponse reports the stored grant and what the projection refresh changed.
type manualGrantResultResponse struct {
	Grant              manualGrantResponse `json:"grant"`
	Replay             bool                `json:"replay"`
	ProjectionsChanged int                 `json:"projections_changed"`
	WebhooksEnqueued   int                 `json:"webhooks_enqueued"`
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

// searchAdminCustomers returns one page of project customers matching an identifier or transaction id.
func (api *API) searchAdminCustomers(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	values := request.URL.Query()
	rawQuery := values.Get("query")
	if len(rawQuery) > 2*persistence.AdminCustomerSearchMaxQueryBytes {
		api.writeError(writer, request, validation.Wrap(errors.New("search text is too long")))
		return
	}
	query := strings.TrimSpace(rawQuery)
	var queryErr error
	if len(query) > persistence.AdminCustomerSearchMaxQueryBytes {
		queryErr = errors.New("search text is too long")
	} else if query != "" {
		queryErr = core.ValidateOpaqueIdentifier("search text", query)
	}
	if !api.requireValidInput(writer, request, queryErr) {
		return
	}
	limit := 0
	if rawLimit := values.Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > persistence.AdminCustomerSearchMaxLimit {
			api.writeError(writer, request, validation.Wrap(errors.New("search limit is out of range")))
			return
		}
		limit = parsed
	}
	after, err := decodeSearchPageToken(values.Get("page_token"), query)
	if !api.requireValidInput(writer, request, errors.Join(projectID.Validate(), err)) {
		return
	}
	search := persistence.AdminCustomerSearch{ProjectID: projectID, Query: query, Limit: limit, After: after}
	if query != "" && api.protection != nil {
		search.References = searchReferenceFingerprinter{protector: api.protection, text: query}
	}
	page, err := api.admin.AdminSearchCustomers(request.Context(), search)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	response := adminCustomerSearchResponse{Customers: make([]adminCustomerMatchResponse, 0, len(page.Results))}
	for _, result := range page.Results {
		response.Customers = append(response.Customers, adminCustomerMatchResponse{
			adminCustomerResponse: adminCustomerFromRecord(result.Customer),
			Match:                 result.Match,
		})
	}
	if page.Next != nil {
		response.NextPageToken = encodeSearchPageToken(*page.Next, query)
	}
	writeJSON(writer, http.StatusOK, response)
}

// getAdminCustomer returns identity, access sources, manual grants, and a bounded timeline for one customer.
func (api *API) getAdminCustomer(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	customerID := core.CustomerID(request.PathValue("customer_id"))
	if !api.requireValidInput(writer, request, errors.Join(projectID.Validate(), customerID.Validate())) {
		return
	}
	detail, err := api.admin.AdminCustomerDetail(request.Context(), projectID, customerID)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, adminCustomerDetailFromRecord(detail, api.now()))
}

// createAdminManualGrant records one audited operator grant and refreshes the normal projection and outbox.
func (api *API) createAdminManualGrant(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok || !api.requireDurableActor(writer, request, principal) {
		return
	}
	var input manualGrantRequest
	if !api.decode(writer, request, &input) {
		return
	}
	now := api.now()
	endsAt, err := manualGrantEnd(input, now)
	if !api.requireValidInput(writer, request, err) {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	write := persistence.ManualGrantWrite{
		ProjectID:      projectID,
		CustomerID:     core.CustomerID(request.PathValue("customer_id")),
		ApplicationID:  input.ApplicationID,
		ProductID:      input.ProductID,
		EntitlementID:  input.EntitlementID,
		Reason:         input.Reason,
		ActorKeyID:     principal.KeyID,
		IdempotencyKey: input.IdempotencyKey,
		StartsAt:       now,
		EndsAt:         endsAt,
	}
	if !api.requireValidInput(writer, request, write.Validate()) {
		return
	}
	var result persistence.ManualGrantResult
	var refresh verification.ProjectionRefreshResult
	err = api.store.Transact(request.Context(), func(repository persistence.Transaction) error {
		var grantErr error
		refresh = verification.ProjectionRefreshResult{}
		result, grantErr = repository.CreateManualGrant(request.Context(), write)
		if grantErr != nil || result.Replay {
			return grantErr
		}
		refresh, grantErr = verification.RefreshCustomerProjectionWithOptions(
			request.Context(), repository, verification.NewDefaultProjector(),
			projectID, result.CanonicalCustomerID, now, true,
		)
		return grantErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	if !result.Replay {
		slog.Info(
			"manual grant created",
			slog.String("project_id", string(projectID)),
			slog.String("grant_id", result.Grant.ID),
			slog.Int("projections_changed", refresh.ProjectionsChanged),
		)
	}
	writeJSON(writer, http.StatusOK, manualGrantResultFromRecord(result, refresh, now))
}

// revokeAdminManualGrant ends one operator grant early and refreshes the normal projection and outbox.
func (api *API) revokeAdminManualGrant(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok || !api.requireDurableActor(writer, request, principal) {
		return
	}
	grantID, found := strings.CutSuffix(request.PathValue("grant_id"), manualGrantRevokeMarker)
	if !found || grantID == "" || strings.Contains(grantID, ":") {
		api.notFound(writer, request)
		return
	}
	var input manualGrantRevocationRequest
	if !api.decode(writer, request, &input) {
		return
	}
	now := api.now()
	projectID := core.ProjectID(request.PathValue("project_id"))
	revocation := persistence.ManualGrantRevocation{
		ProjectID:  projectID,
		CustomerID: core.CustomerID(request.PathValue("customer_id")),
		GrantID:    grantID,
		Reason:     input.Reason,
		ActorKeyID: principal.KeyID,
		RevokedAt:  now,
	}
	if !api.requireValidInput(writer, request, revocation.Validate()) {
		return
	}
	var result persistence.ManualGrantResult
	var refresh verification.ProjectionRefreshResult
	err := api.store.Transact(request.Context(), func(repository persistence.Transaction) error {
		var revokeErr error
		refresh = verification.ProjectionRefreshResult{}
		result, revokeErr = repository.RevokeManualGrant(request.Context(), revocation)
		if revokeErr != nil || result.Replay {
			return revokeErr
		}
		refresh, revokeErr = verification.RefreshCustomerProjectionWithOptions(
			request.Context(), repository, verification.NewDefaultProjector(),
			projectID, result.CanonicalCustomerID, now, true,
		)
		return revokeErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	if !result.Replay {
		slog.Info(
			"manual grant revoked",
			slog.String("project_id", string(projectID)),
			slog.String("grant_id", result.Grant.ID),
			slog.Int("projections_changed", refresh.ProjectionsChanged),
		)
	}
	writeJSON(writer, http.StatusOK, manualGrantResultFromRecord(result, refresh, now))
}

// now returns the API clock in UTC.
func (api *API) now() time.Time {
	if api.clock == nil {
		return time.Now().UTC()
	}
	return api.clock().UTC()
}

// ReferenceFingerprints derives the search text's keyed fingerprint in every searchable reference scope.
func (fingerprinter searchReferenceFingerprinter) ReferenceFingerprints(
	ctx context.Context,
	projectID core.ProjectID,
	applicationIDs []core.ApplicationID,
) ([]persistence.AdminReferenceFingerprint, error) {
	fingerprints := make([]persistence.AdminReferenceFingerprint, 0, len(applicationIDs)*len(searchableReferences))
	for _, applicationID := range applicationIDs {
		for _, reference := range searchableReferences {
			plaintext := []byte(fingerprinter.text)
			protected, err := protection.Protect(ctx, fingerprinter.protector, protection.Scope{
				ProjectID:     projectID,
				ApplicationID: applicationID,
				Purpose:       providerReferencePurpose + ":" + string(reference.Role) + ":" + reference.Kind,
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

// manualGrantEnd resolves exactly one of a relative duration or an absolute end after now.
func manualGrantEnd(input manualGrantRequest, now time.Time) (time.Time, error) {
	switch {
	case input.DurationDays != nil && input.EndsAt != nil:
		return time.Time{}, errors.New("manual grant takes duration_days or ends_at, not both")
	case input.DurationDays != nil:
		if *input.DurationDays < 1 || *input.DurationDays > manualGrantMaxDurationDays {
			return time.Time{}, errors.New("manual grant duration_days is out of range")
		}
		return now.AddDate(0, 0, *input.DurationDays), nil
	case input.EndsAt != nil:
		endsAt := input.EndsAt.UTC()
		if !endsAt.After(now) {
			return time.Time{}, errors.New("manual grant ends_at must be in the future")
		}
		if endsAt.Sub(now) > persistence.ManualGrantMaxDuration {
			return time.Time{}, errors.New("manual grant ends_at is too far in the future")
		}
		return endsAt, nil
	default:
		return time.Time{}, errors.New("manual grant requires duration_days or ends_at")
	}
}

// encodeSearchPageToken returns an opaque cursor bound to the normalized search text.
func encodeSearchPageToken(cursor persistence.AdminCustomerSearchCursor, query string) string {
	payload, _ := json.Marshal(searchPageToken{
		Version:    searchPageTokenVersion,
		Rank:       cursor.Rank,
		CreatedAt:  cursor.CreatedAt.UTC().Format(time.RFC3339Nano),
		CustomerID: string(cursor.CustomerID),
		QueryHash:  searchQueryHash(query),
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

// decodeSearchPageToken parses an opaque cursor and rejects one issued for different search text.
func decodeSearchPageToken(token, query string) (*persistence.AdminCustomerSearchCursor, error) {
	if token == "" {
		return nil, nil
	}
	invalid := errors.New("search page token is invalid")
	if len(token) > 1024 {
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

// adminCustomerFromRecord maps one customer summary onto the overview contract.
func adminCustomerFromRecord(customer persistence.AdminCustomer) adminCustomerResponse {
	return adminCustomerResponse{
		ID: customer.ID, ExternalID: customer.ExternalID,
		MergedIntoCustomerID: customer.MergedIntoCustomerID, AliasCount: customer.AliasCount,
		EntitlementCount: customer.EntitlementCount, AllowedCount: customer.AllowedCount,
		LastObservedAt: customer.LastObservedAt, CreatedAt: customer.CreatedAt,
	}
}

// adminCustomerDetailFromRecord maps one customer closure and evaluates current access at now.
func adminCustomerDetailFromRecord(detail persistence.AdminCustomerDetail, now time.Time) adminCustomerDetailResponse {
	response := adminCustomerDetailResponse{
		GeneratedAt:         now,
		Customer:            adminCustomerIdentityFromRecord(detail.Customer),
		CanonicalCustomerID: detail.CanonicalCustomerID,
		Members:             make([]adminCustomerIdentityResponse, 0, len(detail.Members)),
		Aliases:             make([]adminCustomerAliasResponse, 0, len(detail.Aliases)),
		Merges:              make([]adminCustomerMergeRecordResponse, 0, len(detail.Merges)),
		Entitlements:        make([]adminCustomerEntitlementResponse, 0, len(detail.Entitlements)),
		ManualGrants:        make([]manualGrantResponse, 0, len(detail.ManualGrants)),
		GrantOptions:        make([]adminGrantOptionResponse, 0, len(detail.GrantOptions)),
		Timeline:            make([]adminTimelineEventResponse, 0, len(detail.Timeline)),
		TimelineTruncated:   detail.TimelineTruncated,
	}
	for _, member := range detail.Members {
		response.Members = append(response.Members, adminCustomerIdentityFromRecord(member))
	}
	for _, alias := range detail.Aliases {
		response.Aliases = append(response.Aliases, adminCustomerAliasResponse{
			ExternalID: alias.ExternalID, CanonicalCustomerID: alias.CanonicalCustomerID,
			ActorRole: alias.ActorRole, ActorKeyID: alias.ActorKeyID, CreatedAt: alias.CreatedAt,
		})
	}
	for _, merge := range detail.Merges {
		response.Merges = append(response.Merges, adminCustomerMergeRecordResponse{
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
		response.Entitlements = append(response.Entitlements, adminCustomerEntitlementResponse{
			EntitlementID: entitlement.EntitlementID, Key: entitlement.Key,
			Source: entitlement.SourceKind, SourceID: entitlement.SourceID,
			SourceApplicationID: entitlement.SourceApplicationID, SourceProductID: entitlement.SourceProductID,
			Access: access, Reason: reason, EffectiveStartsAt: startsAt,
			EffectiveEndsAt: entitlement.EffectivePeriod.EndsAt, Version: entitlement.Version,
			UpdatedAt: entitlement.UpdatedAt,
		})
	}
	for _, grant := range detail.ManualGrants {
		response.ManualGrants = append(response.ManualGrants, manualGrantFromRecord(grant, now))
	}
	for _, option := range detail.GrantOptions {
		applicationIDs := append([]core.ApplicationID{}, option.ApplicationIDs...)
		response.GrantOptions = append(response.GrantOptions, adminGrantOptionResponse{
			EntitlementID: option.EntitlementID, EntitlementKey: option.EntitlementKey,
			ProductID: option.ProductID, ProductKind: option.ProductKind, ApplicationIDs: applicationIDs,
		})
	}
	for _, event := range detail.Timeline {
		response.Timeline = append(response.Timeline, adminTimelineEventResponse{
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

// adminCustomerIdentityFromRecord maps one stored customer row.
func adminCustomerIdentityFromRecord(identity persistence.AdminCustomerIdentity) adminCustomerIdentityResponse {
	return adminCustomerIdentityResponse{
		ID: identity.ID, ExternalID: identity.ExternalID,
		MergedIntoCustomerID: identity.MergedIntoCustomerID, CreatedAt: identity.CreatedAt,
	}
}

// manualGrantFromRecord maps one stored grant and derives its status at now.
func manualGrantFromRecord(grant persistence.ManualGrant, now time.Time) manualGrantResponse {
	status := manualGrantStatusExpired
	switch {
	case grant.RevokedAt != nil:
		status = manualGrantStatusRevoked
	case grant.Active(now):
		status = manualGrantStatusActive
	}
	return manualGrantResponse{
		ID: grant.ID, ProjectID: grant.ProjectID, CustomerID: grant.CustomerID,
		ApplicationID: grant.ApplicationID, ProductID: grant.ProductID,
		EntitlementID: grant.EntitlementID, EntitlementKey: grant.EntitlementKey,
		Reason: grant.Reason, ActorKeyID: grant.ActorKeyID, Status: status,
		EffectiveStartsAt: grant.StartsAt, EffectiveEndsAt: grant.EndsAt,
		RevokedAt: grant.RevokedAt, RevokedByKeyID: grant.RevokedByKeyID,
		RevocationReason: grant.RevocationReason, CreatedAt: grant.CreatedAt,
	}
}

// manualGrantResultFromRecord maps one grant write and its projection refresh counts.
func manualGrantResultFromRecord(
	result persistence.ManualGrantResult,
	refresh verification.ProjectionRefreshResult,
	now time.Time,
) manualGrantResultResponse {
	return manualGrantResultResponse{
		Grant:              manualGrantFromRecord(result.Grant, now),
		Replay:             result.Replay,
		ProjectionsChanged: refresh.ProjectionsChanged,
		WebhooksEnqueued:   refresh.WebhooksEnqueued,
	}
}
