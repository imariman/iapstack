package verification_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// verificationProjectID identifies the project shared by verification unit fixtures.
	verificationProjectID = "project-1"
	// verificationApplicationID identifies the application shared by verification unit fixtures.
	verificationApplicationID = "application-1"
	// verificationCustomerID identifies the internal customer shared by verification unit fixtures.
	verificationCustomerID = "customer-1"
	// verificationExternalCustomerID identifies the public customer shared by verification unit fixtures.
	verificationExternalCustomerID = "customer-external"
	// verificationProductID identifies the internal product shared by verification unit fixtures.
	verificationProductID = "product-1"
	// verificationProviderProductID identifies the store product shared by verification unit fixtures.
	verificationProviderProductID = "provider-product-1"
	// verificationEntitlementID identifies the entitlement shared by verification unit fixtures.
	verificationEntitlementID = "entitlement-1"
	// verificationEntitlementKey identifies the public entitlement shared by verification unit fixtures.
	verificationEntitlementKey = "pro"
	// verificationKeyID identifies the deterministic fake protector key.
	verificationKeyID = "test-key"
)

// fakeClock returns one deterministic trusted time.
type fakeClock struct {
	now time.Time
}

// fakeProtector returns deterministic protected values and counts protection calls.
type fakeProtector struct {
	calls int
	err   error
}

// fakeAdapter returns one configured provider result or failure.
type fakeAdapter struct {
	result          stores.VerificationResult
	err             error
	calls           int
	reconcileCalls  int
	postCommitCalls int
	postCommit      func(stores.PostCommitRequest) error
}

// fakeStore applies copy-on-commit semantics around an in-memory repository.
type fakeStore struct {
	repository   *fakeTransaction
	transactions int
}

// fakeTransaction implements provider-neutral persistence for orchestration unit tests.
type fakeTransaction struct {
	application  core.Application
	customer     core.Customer
	catalog      map[core.ProviderProductID]persistence.CatalogProduct
	evidence     map[[32]byte]persistence.EvidenceWrite
	evidenceIDs  map[[32]byte]int64
	artifacts    []persistence.ArtifactWrite
	observations map[core.ObservationID]persistence.ObservationWrite
	entitlements map[core.EntitlementID]persistence.CustomerEntitlement
	outbox       map[[32]byte]persistence.OutboxEvent
	failOutbox   bool
}

// verificationFixture contains one coherent command and provider result.
type verificationFixture struct {
	command     verification.Command
	application core.Application
	customer    core.Customer
	catalog     persistence.CatalogProduct
	result      stores.VerificationResult
	clock       fakeClock
}

var (
	// errFakeOutbox indicates an injected durable outbox failure.
	errFakeOutbox = errors.New("fake outbox failure")
)

// TestServiceReconcilePersistsProviderNotificationSignal verifies token-only refresh orchestration.
func TestServiceReconcilePersistsProviderNotificationSignal(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", "sensitive-purchase-token")
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	customerBinding, err := core.NewStoreReference(
		core.ReferenceCustomerBinding,
		"obfuscated_external_account_id",
		verificationExternalCustomerID,
	)
	if err != nil {
		t.Fatalf("NewStoreReference() customer binding error = %v", err)
	}
	fixture.result.Observations[0].References = append(
		fixture.result.Observations[0].References,
		queryReference,
		customerBinding,
	)
	fixture.result.PostCommitActions = []stores.PostCommitAction{{
		Kind: "acknowledge_purchase", ProductID: verificationProviderProductID,
		ProductKind: core.ProductKindNonConsumable, QueryReferences: []core.StoreReference{queryReference},
	}}
	signal, err := stores.NewEvidence(
		"application/vnd.iapstack.huawei-notification-v2+json",
		[]byte(`{"event_type":"ORDER","purchase_token":"sensitive-purchase-token"}`),
	)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	store := newFakeStore(fixture)
	adapter := &fakeAdapter{result: fixture.result}
	adapter.postCommit = func(request stores.PostCommitRequest) error {
		if len(store.repository.entitlements) != 1 || len(store.repository.outbox) != 1 {
			t.Fatalf("PostCommit() observed reconciliation before durable commit: %#v", store.repository)
		}
		if !reflect.DeepEqual(request.Actions, fixture.result.PostCommitActions) {
			t.Fatalf("PostCommit() actions = %#v", request.Actions)
		}
		return nil
	}
	service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)

	result, err := service.Reconcile(context.Background(), verification.ReconciliationCommand{
		ProjectID:                verificationProjectID,
		ApplicationID:            verificationApplicationID,
		ExternalCustomerID:       verificationExternalCustomerID,
		ExpectedProducts:         []core.ProviderProductID{verificationProviderProductID},
		ExpectedProductKind:      core.ProductKindNonConsumable,
		ExpectedCustomerBindings: []core.StoreReference{customerBinding},
		QueryReferences:          []core.StoreReference{queryReference},
		Signal:                   signal,
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if adapter.reconcileCalls != 1 || adapter.postCommitCalls != 1 || len(result.Entitlements) != 1 {
		t.Fatalf("Reconcile() = (%#v, reconcile calls=%d, post-commit calls=%d)", result, adapter.reconcileCalls, adapter.postCommitCalls)
	}
	for _, evidence := range store.repository.evidence {
		if evidence.Kind != "provider_notification_reconciliation" {
			t.Fatalf("evidence kind = %q", evidence.Kind)
		}
	}
}

// TestServiceReconcilePreservesDurableResultWhenPostCommitFails verifies retryable acknowledgement ordering.
func TestServiceReconcilePreservesDurableResultWhenPostCommitFails(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	queryReference, err := core.NewStoreReference(
		core.ReferenceQuery,
		"purchase_token",
		"sensitive-purchase-token",
	)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	customerBinding, err := core.NewStoreReference(
		core.ReferenceCustomerBinding,
		"obfuscated_external_account_id",
		verificationExternalCustomerID,
	)
	if err != nil {
		t.Fatalf("NewStoreReference() customer binding error = %v", err)
	}
	fixture.result.Observations[0].References = append(
		fixture.result.Observations[0].References,
		queryReference,
		customerBinding,
	)
	fixture.result.PostCommitActions = []stores.PostCommitAction{{
		Kind: "acknowledge_purchase", ProductID: verificationProviderProductID,
		ProductKind:     core.ProductKindNonConsumable,
		QueryReferences: []core.StoreReference{queryReference},
	}}
	signal, err := stores.NewEvidence(
		"application/vnd.iapstack.google-play-rtdn+json",
		[]byte(`{"kind":"one_time_product","purchase_token":"sensitive-purchase-token"}`),
	)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	store := newFakeStore(fixture)
	providerFailure := stores.NewFailure(
		core.ProviderHuaweiAppGallery,
		"acknowledge",
		stores.FailureTemporary,
		0,
		context.DeadlineExceeded,
	)
	adapter := &fakeAdapter{result: fixture.result}
	adapter.postCommit = func(request stores.PostCommitRequest) error {
		if len(store.repository.evidence) != 1 ||
			len(store.repository.observations) != 1 ||
			len(store.repository.entitlements) != 1 ||
			len(store.repository.outbox) != 1 {
			t.Fatalf("PostCommit() observed reconciliation before durable commit: %#v", store.repository)
		}
		return providerFailure
	}
	service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)

	_, err = service.Reconcile(context.Background(), verification.ReconciliationCommand{
		ProjectID:                verificationProjectID,
		ApplicationID:            verificationApplicationID,
		ExternalCustomerID:       verificationExternalCustomerID,
		ExpectedProducts:         []core.ProviderProductID{verificationProviderProductID},
		ExpectedProductKind:      core.ProductKindNonConsumable,
		ExpectedCustomerBindings: []core.StoreReference{customerBinding},
		QueryReferences:          []core.StoreReference{queryReference},
		Signal:                   signal,
	})
	var actualFailure *stores.Failure
	if !errors.As(err, &actualFailure) || actualFailure != providerFailure {
		t.Fatalf("Reconcile() error = %v, want post-commit provider failure", err)
	}
	if adapter.reconcileCalls != 1 || adapter.postCommitCalls != 1 {
		t.Fatalf(
			"Reconcile() calls = %d, post-commit calls = %d",
			adapter.reconcileCalls,
			adapter.postCommitCalls,
		)
	}
	if len(store.repository.entitlements) != 1 || len(store.repository.outbox) != 1 {
		t.Fatalf("repository lost durable reconciliation after post-commit failure: %#v", store.repository)
	}
}

// TestServiceVerifyPersistsAndReplays verifies one atomic success and logical replay.
func TestServiceVerifyPersistsAndReplays(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	store := newFakeStore(fixture)
	protector := &fakeProtector{}
	adapter := &fakeAdapter{result: fixture.result}
	service := newVerificationService(t, store, adapter, protector, fixture.clock)

	first, err := service.Verify(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("first Verify() error = %v", err)
	}
	second, err := service.Verify(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("second Verify() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replayed Result = %#v, want %#v", second, first)
	}
	if len(first.Entitlements) != 1 || first.Entitlements[0].Version != 1 {
		t.Fatalf("entitlements = %#v, want one version-one projection", first.Entitlements)
	}
	if adapter.calls != 2 {
		t.Fatalf("adapter Verify() calls = %d, want 2", adapter.calls)
	}
	if len(store.repository.evidence) != 1 ||
		len(store.repository.observations) != 1 ||
		len(store.repository.entitlements) != 1 ||
		len(store.repository.outbox) != 1 {
		t.Fatalf("replayed repository state = %#v, want one logical record per category", store.repository)
	}
	for _, evidence := range store.repository.evidence {
		if bytes.Contains(evidence.Payload.Ciphertext, fixture.command.Evidence.Bytes()) {
			t.Fatal("persisted evidence ciphertext contains plaintext evidence")
		}
	}
}

// TestServiceReconcileReusesUnchangedObservationProjection verifies provider completion does not churn entitlement events.
func TestServiceReconcileReusesUnchangedObservationProjection(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", "sensitive-purchase-token")
	if err != nil {
		t.Fatalf("NewStoreReference() query error = %v", err)
	}
	customerBinding, err := core.NewStoreReference(
		core.ReferenceCustomerBinding,
		"obfuscated_external_account_id",
		verificationExternalCustomerID,
	)
	if err != nil {
		t.Fatalf("NewStoreReference() customer binding error = %v", err)
	}
	fixture.command.ExpectedCustomerBindings = []core.StoreReference{customerBinding}
	fixture.result.Observations[0].References = append(
		fixture.result.Observations[0].References,
		queryReference,
		customerBinding,
	)
	fixture.result.PostCommitActions = []stores.PostCommitAction{{
		Kind: "acknowledge_purchase", ProductID: verificationProviderProductID,
		ProductKind: core.ProductKindNonConsumable, QueryReferences: []core.StoreReference{queryReference},
	}}
	store := newFakeStore(fixture)
	adapter := &fakeAdapter{result: fixture.result}
	service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)

	first, err := service.Verify(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if adapter.postCommitCalls != 1 {
		t.Fatalf("PostCommit() calls = %d, want 1", adapter.postCommitCalls)
	}

	acknowledgedArtifact, err := stores.NewEvidence(repositoryContentType(), []byte(`{"acknowledged":true}`))
	if err != nil {
		t.Fatalf("NewEvidence() acknowledged artifact error = %v", err)
	}
	adapter.result.PostCommitActions = nil
	adapter.result.VerifiedAt = fixture.result.VerifiedAt.Add(time.Minute)
	adapter.result.Artifacts = []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: acknowledgedArtifact}}
	adapter.result.Observations[0].ObservedAt = fixture.result.Observations[0].ObservedAt.Add(time.Minute)
	signal, err := stores.NewEvidence(repositoryContentType(), []byte(`{"acknowledgement_changed":true}`))
	if err != nil {
		t.Fatalf("NewEvidence() signal error = %v", err)
	}
	second, err := service.Reconcile(context.Background(), verification.ReconciliationCommand{
		ProjectID:                verificationProjectID,
		ApplicationID:            verificationApplicationID,
		ExternalCustomerID:       verificationExternalCustomerID,
		ExpectedProducts:         []core.ProviderProductID{verificationProviderProductID},
		ExpectedProductKind:      core.ProductKindNonConsumable,
		ExpectedCustomerBindings: []core.StoreReference{customerBinding},
		QueryReferences:          []core.StoreReference{queryReference},
		Signal:                   signal,
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(first.Entitlements) != 1 || len(second.Entitlements) != 1 ||
		first.Entitlements[0].Version != 1 || second.Entitlements[0].Version != 1 {
		t.Fatalf("entitlement versions = (%#v, %#v), want one stable version-one projection", first.Entitlements, second.Entitlements)
	}
	if len(store.repository.observations) != 1 || len(store.repository.entitlements) != 1 || len(store.repository.outbox) != 1 {
		t.Fatalf("post-reconciliation repository state = %#v, want one observation, entitlement, and outbox event", store.repository)
	}
	if len(store.repository.evidence) != 2 || len(store.repository.artifacts) != 2 {
		t.Fatalf("audit records = (%d evidence, %d artifacts), want acknowledged provider snapshot retained",
			len(store.repository.evidence), len(store.repository.artifacts))
	}
}

// TestServiceRejectsCatalogMismatchBeforeWrites verifies catalog authority over provider output.
func TestServiceRejectsCatalogMismatchBeforeWrites(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	end := fixture.result.VerifiedAt.Add(time.Hour)
	fixture.result.Observations[0].ProductKind = core.ProductKindSubscription
	fixture.result.Observations[0].EffectivePeriod.EndsAt = &end
	fixture.result.Observations[0].Renewal = core.Renewal{
		Mode:   core.RenewalAuto,
		Status: core.RenewalEnabled,
	}
	store := newFakeStore(fixture)
	service := newVerificationService(
		t,
		store,
		&fakeAdapter{result: fixture.result},
		&fakeProtector{},
		fixture.clock,
	)

	if _, err := service.Verify(context.Background(), fixture.command); err == nil {
		t.Fatal("Verify() error = nil, want catalog product kind mismatch")
	}
	assertNoDurableWrites(t, store.repository)
}

// TestServiceRejectsUnknownClaimBeforeProviderCall verifies catalog fail-fast behavior.
func TestServiceRejectsUnknownClaimBeforeProviderCall(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	fixture.command.ClaimedProducts = []core.ProviderProductID{"unknown-product"}
	store := newFakeStore(fixture)
	adapter := &fakeAdapter{result: fixture.result}
	service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)

	_, err := service.Verify(context.Background(), fixture.command)
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Verify() error = %v, want ErrNotFound", err)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter Verify() calls = %d, want 0", adapter.calls)
	}
	assertNoDurableWrites(t, store.repository)
}

// TestServicePreservesProviderFailureAndWritesNothing verifies failure classification and isolation.
func TestServicePreservesProviderFailureAndWritesNothing(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	providerFailure := stores.NewFailure(
		core.ProviderHuaweiAppGallery,
		"verify",
		stores.FailureTemporary,
		time.Minute,
		errors.New("provider unavailable"),
	)
	store := newFakeStore(fixture)
	service := newVerificationService(
		t,
		store,
		&fakeAdapter{err: providerFailure},
		&fakeProtector{},
		fixture.clock,
	)

	_, err := service.Verify(context.Background(), fixture.command)
	var actualFailure *stores.Failure
	if !errors.As(err, &actualFailure) || actualFailure != providerFailure {
		t.Fatalf("Verify() error = %v, want original provider failure", err)
	}
	assertNoDurableWrites(t, store.repository)
}

// TestServiceRollsBackWhenOutboxFails verifies evidence and projections do not commit partially.
func TestServiceRollsBackWhenOutboxFails(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	store := newFakeStore(fixture)
	store.repository.failOutbox = true
	service := newVerificationService(
		t,
		store,
		&fakeAdapter{result: fixture.result},
		&fakeProtector{},
		fixture.clock,
	)

	_, err := service.Verify(context.Background(), fixture.command)
	if !errors.Is(err, errFakeOutbox) {
		t.Fatalf("Verify() error = %v, want fake outbox failure", err)
	}
	assertNoDurableWrites(t, store.repository)
}

// TestServiceRunsPostCommitAfterDurablePersistence verifies retryable provider completion never precedes entitlement commit.
func TestServiceRunsPostCommitAfterDurablePersistence(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", "sensitive-purchase-token")
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	fixture.result.Observations[0].References = append(fixture.result.Observations[0].References, queryReference)
	fixture.result.PostCommitActions = []stores.PostCommitAction{{
		Kind: "acknowledge_purchase", ProductID: verificationProviderProductID,
		ProductKind: core.ProductKindNonConsumable, QueryReferences: []core.StoreReference{queryReference},
	}}
	store := newFakeStore(fixture)
	providerFailure := stores.NewFailure(
		core.ProviderHuaweiAppGallery,
		"acknowledge",
		stores.FailureTemporary,
		0,
		context.DeadlineExceeded,
	)
	adapter := &fakeAdapter{result: fixture.result}
	adapter.postCommit = func(request stores.PostCommitRequest) error {
		if len(store.repository.entitlements) != 1 || len(store.repository.outbox) != 1 {
			t.Fatalf("PostCommit() observed repository before durable commit: %#v", store.repository)
		}
		if request.Application != fixture.application || !reflect.DeepEqual(request.Actions, fixture.result.PostCommitActions) {
			t.Fatalf("PostCommit() request = %#v", request)
		}
		return providerFailure
	}
	service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)

	_, err = service.Verify(context.Background(), fixture.command)
	var actualFailure *stores.Failure
	if !errors.As(err, &actualFailure) || actualFailure != providerFailure {
		t.Fatalf("Verify() error = %v, want post-commit provider failure", err)
	}
	if adapter.postCommitCalls != 1 {
		t.Fatalf("PostCommit() calls = %d, want 1", adapter.postCommitCalls)
	}
	if len(store.repository.entitlements) != 1 || len(store.repository.outbox) != 1 {
		t.Fatalf("repository lost durable result after post-commit failure: %#v", store.repository)
	}
}

// TestDefaultProjectorPreservesIndependentAllowedSource verifies conservative multi-product access.
func TestDefaultProjectorPreservesIndependentAllowedSource(t *testing.T) {
	t.Parallel()

	projector := verification.NewDefaultProjector()
	current := persistence.CustomerEntitlement{
		Projection:          projectionFor("product-existing", "observation-existing", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		Key:                 verificationEntitlementKey,
		Version:             1,
	}
	candidate := verification.ProjectionCandidate{
		Projection:          projectionFor("product-new", "observation-new", core.AccessDenied),
		SourceApplicationID: "application-other",
		ObservedAt:          time.Now().UTC(),
	}
	projections, err := projector.Project([]persistence.CustomerEntitlement{current}, []verification.ProjectionCandidate{candidate})
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 0 {
		t.Fatalf("Project() = %#v, want current independent allowed source preserved", projections)
	}

	candidate.Projection.SourceProductID = current.Projection.SourceProductID
	candidate.SourceApplicationID = current.SourceApplicationID
	projections, err = projector.Project([]persistence.CustomerEntitlement{current}, []verification.ProjectionCandidate{candidate})
	if err != nil {
		t.Fatalf("Project() same-source error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessDenied {
		t.Fatalf("Project() same-source = %#v, want denied replacement", projections)
	}
}

// Now returns the fake clock's deterministic time.
func (clock fakeClock) Now() time.Time {
	return clock.now
}

// Protect returns deterministic ciphertext metadata without retaining plaintext.
func (protector *fakeProtector) Protect(
	_ context.Context,
	request protection.Request,
) (protection.Value, error) {
	protector.calls++
	if protector.err != nil {
		return protection.Value{}, protector.err
	}
	plaintext := request.Bytes()
	fingerprintInput := append(
		[]byte(string(request.Scope.ApplicationID)+":"+request.Scope.Purpose+":"),
		plaintext...,
	)
	return protection.Value{
		Ciphertext:  []byte("protected-ciphertext"),
		Fingerprint: sha256.Sum256(fingerprintInput),
		KeyID:       verificationKeyID,
	}, nil
}

// Provider identifies the provider implemented by the fake adapter.
func (adapter *fakeAdapter) Provider() core.Provider {
	return core.ProviderHuaweiAppGallery
}

// Verify returns the configured fake provider result or error.
func (adapter *fakeAdapter) Verify(
	_ context.Context,
	_ stores.VerificationRequest,
) (stores.VerificationResult, error) {
	adapter.calls++
	return adapter.result, adapter.err
}

// Reconcile returns the configured authoritative provider result.
func (adapter *fakeAdapter) Reconcile(
	_ context.Context,
	_ stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	adapter.reconcileCalls++
	return adapter.result, adapter.err
}

// PostCommit runs the configured provider completion callback after persistence.
func (adapter *fakeAdapter) PostCommit(_ context.Context, request stores.PostCommitRequest) error {
	adapter.postCommitCalls++
	if adapter.postCommit == nil {
		return nil
	}
	return adapter.postCommit(request)
}

// Ping reports that the in-memory fake store is available.
func (store *fakeStore) Ping(context.Context) error {
	return nil
}

// Transact applies one operation to a clone and commits only successful state.
func (store *fakeStore) Transact(_ context.Context, operation persistence.TransactionFunc) error {
	store.transactions++
	working := store.repository.clone()
	if err := operation(working); err != nil {
		return err
	}
	store.repository = working
	return nil
}

// Close releases no resources for the in-memory fake store.
func (store *fakeStore) Close() {}

// Application returns the configured application inside its exact scope.
func (repository *fakeTransaction) Application(
	_ context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (core.Application, error) {
	if repository.application.ProjectID != projectID || repository.application.ID != applicationID {
		return core.Application{}, persistence.ErrNotFound
	}
	return repository.application, nil
}

// Credential is not used by purchase verification unit tests.
func (repository *fakeTransaction) Credential(
	_ context.Context,
	_ persistence.CredentialKey,
) (persistence.CredentialRecord, error) {
	return persistence.CredentialRecord{}, persistence.ErrNotFound
}

// PutCredential is not used by purchase verification unit tests.
func (repository *fakeTransaction) PutCredential(
	_ context.Context,
	_ persistence.CredentialWrite,
) (persistence.CredentialRecord, error) {
	return persistence.CredentialRecord{}, persistence.ErrNotFound
}

// CustomerByExternalID returns the configured customer inside its exact scope.
func (repository *fakeTransaction) CustomerByExternalID(
	_ context.Context,
	projectID core.ProjectID,
	externalID string,
) (core.Customer, error) {
	if repository.customer.ProjectID != projectID || repository.customer.ExternalID != externalID {
		return core.Customer{}, persistence.ErrNotFound
	}
	return repository.customer, nil
}

// CatalogProducts returns requested mappings in request order.
func (repository *fakeTransaction) CatalogProducts(
	_ context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	providerProductIDs []core.ProviderProductID,
) ([]persistence.CatalogProduct, error) {
	if repository.application.ProjectID != projectID || repository.application.ID != applicationID {
		return nil, persistence.ErrNotFound
	}
	products := make([]persistence.CatalogProduct, 0, len(providerProductIDs))
	for _, productID := range providerProductIDs {
		product, exists := repository.catalog[productID]
		if !exists {
			return nil, persistence.ErrNotFound
		}
		products = append(products, product)
	}
	return products, nil
}

// SaveEvidence stores one logical protected evidence value idempotently.
func (repository *fakeTransaction) SaveEvidence(
	_ context.Context,
	write persistence.EvidenceWrite,
) (int64, error) {
	if evidenceID, exists := repository.evidenceIDs[write.Payload.Fingerprint]; exists {
		return evidenceID, nil
	}
	evidenceID := int64(len(repository.evidenceIDs) + 1)
	repository.evidence[write.Payload.Fingerprint] = write
	repository.evidenceIDs[write.Payload.Fingerprint] = evidenceID
	return evidenceID, nil
}

// SaveArtifact stores one protected provider artifact.
func (repository *fakeTransaction) SaveArtifact(_ context.Context, write persistence.ArtifactWrite) error {
	for _, artifact := range repository.artifacts {
		if artifact.EvidenceID == write.EvidenceID &&
			artifact.Kind == write.Kind &&
			artifact.Payload.Fingerprint == write.Payload.Fingerprint {
			return nil
		}
	}
	repository.artifacts = append(repository.artifacts, write)
	return nil
}

// SaveObservation stores one logical immutable observation idempotently.
func (repository *fakeTransaction) SaveObservation(
	_ context.Context,
	write persistence.ObservationWrite,
) error {
	if existing, exists := repository.observations[write.Observation.ID]; exists {
		existing.EvidenceID = write.EvidenceID
		existing.Observation.ObservedAt = write.Observation.ObservedAt
		if !reflect.DeepEqual(existing, write) {
			return persistence.ErrConflict
		}
		return nil
	}
	repository.observations[write.Observation.ID] = write
	return nil
}

// PutEntitlement creates or replaces one in-memory current projection.
func (repository *fakeTransaction) PutEntitlement(
	_ context.Context,
	projection persistence.EntitlementProjection,
) (persistence.EntitlementWriteResult, error) {
	current, exists := repository.entitlements[projection.EntitlementID]
	if exists && reflect.DeepEqual(current.Projection, projection) {
		return persistence.EntitlementWriteResult{Entitlement: current, Changed: false}, nil
	}
	version := int64(1)
	if exists {
		version = current.Version + 1
	}
	entitlement := persistence.CustomerEntitlement{
		Projection:          projection,
		SourceApplicationID: repository.sourceApplicationID(projection.SourceObservationID),
		Key:                 repository.entitlementKey(projection.EntitlementID),
		Version:             version,
	}
	repository.entitlements[projection.EntitlementID] = entitlement
	return persistence.EntitlementWriteResult{Entitlement: entitlement, Changed: true}, nil
}

// CustomerEntitlements returns a stable sorted copy of current in-memory projections.
func (repository *fakeTransaction) CustomerEntitlements(
	_ context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) ([]persistence.CustomerEntitlement, error) {
	entitlements := make([]persistence.CustomerEntitlement, 0, len(repository.entitlements))
	for _, entitlement := range repository.entitlements {
		if entitlement.Projection.ProjectID == projectID && entitlement.Projection.CustomerID == customerID {
			entitlements = append(entitlements, entitlement)
		}
	}
	sort.Slice(entitlements, func(left, right int) bool {
		return entitlements[left].Key < entitlements[right].Key
	})
	return entitlements, nil
}

// SaveOutboxEvent stores one logical event or returns an injected failure.
func (repository *fakeTransaction) SaveOutboxEvent(
	_ context.Context,
	event persistence.OutboxEvent,
) (string, error) {
	if repository.failOutbox {
		return "", errFakeOutbox
	}
	if existing, exists := repository.outbox[event.PayloadFingerprint]; exists {
		return existing.ID, nil
	}
	repository.outbox[event.PayloadFingerprint] = event
	return event.ID, nil
}

// clone returns a deep-enough transaction copy for copy-on-commit unit tests.
func (repository *fakeTransaction) clone() *fakeTransaction {
	clone := &fakeTransaction{
		application:  repository.application,
		customer:     repository.customer,
		catalog:      make(map[core.ProviderProductID]persistence.CatalogProduct, len(repository.catalog)),
		evidence:     make(map[[32]byte]persistence.EvidenceWrite, len(repository.evidence)),
		evidenceIDs:  make(map[[32]byte]int64, len(repository.evidenceIDs)),
		artifacts:    append([]persistence.ArtifactWrite(nil), repository.artifacts...),
		observations: make(map[core.ObservationID]persistence.ObservationWrite, len(repository.observations)),
		entitlements: make(map[core.EntitlementID]persistence.CustomerEntitlement, len(repository.entitlements)),
		outbox:       make(map[[32]byte]persistence.OutboxEvent, len(repository.outbox)),
		failOutbox:   repository.failOutbox,
	}
	for key, value := range repository.catalog {
		clone.catalog[key] = value
	}
	for key, value := range repository.evidence {
		clone.evidence[key] = value
	}
	for key, value := range repository.evidenceIDs {
		clone.evidenceIDs[key] = value
	}
	for key, value := range repository.observations {
		clone.observations[key] = value
	}
	for key, value := range repository.entitlements {
		clone.entitlements[key] = value
	}
	for key, value := range repository.outbox {
		clone.outbox[key] = value
	}
	return clone
}

// entitlementKey returns the configured public key for one entitlement identity.
func (repository *fakeTransaction) entitlementKey(entitlementID core.EntitlementID) string {
	for _, product := range repository.catalog {
		for _, entitlement := range product.Entitlements {
			if entitlement.ID == entitlementID {
				return entitlement.Key
			}
		}
	}
	return ""
}

// sourceApplicationID returns the application that produced one stored observation.
func (repository *fakeTransaction) sourceApplicationID(observationID core.ObservationID) core.ApplicationID {
	observation, exists := repository.observations[observationID]
	if !exists {
		return ""
	}
	return observation.Observation.ApplicationID
}

// newVerificationFixture builds one valid provider-neutral verification scenario.
func newVerificationFixture(t *testing.T) verificationFixture {
	t.Helper()

	verifiedAt := time.Date(2026, time.August, 24, 2, 0, 0, 0, time.UTC)
	application := core.Application{
		ID:        verificationApplicationID,
		ProjectID: verificationProjectID,
		Store: core.StoreApplication{
			Provider:    core.ProviderHuaweiAppGallery,
			Environment: core.EnvironmentSandbox,
			ID:          "provider-application-1",
		},
	}
	customer := core.Customer{
		ID:         verificationCustomerID,
		ProjectID:  verificationProjectID,
		ExternalID: verificationExternalCustomerID,
	}
	entitlement := core.Entitlement{
		ID:        verificationEntitlementID,
		ProjectID: verificationProjectID,
		Key:       verificationEntitlementKey,
	}
	catalog := persistence.CatalogProduct{
		Mapping: core.StoreProduct{
			ApplicationID: verificationApplicationID,
			ProductID:     verificationProductID,
			ProviderID:    verificationProviderProductID,
		},
		Product: core.Product{
			ID:             verificationProductID,
			ProjectID:      verificationProjectID,
			Kind:           core.ProductKindNonConsumable,
			EntitlementIDs: []core.EntitlementID{verificationEntitlementID},
		},
		Entitlements: []core.Entitlement{entitlement},
	}
	evidence, err := stores.NewEvidence(repositoryContentType(), []byte(`{"order":"sensitive-order"}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	reference, err := core.NewStoreReference(core.ReferenceTransaction, "order_id", "sensitive-order")
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	observation := core.PurchaseObservation{
		ID:              "observation-1",
		ApplicationID:   application.ID,
		Store:           application.Store,
		ProductID:       verificationProviderProductID,
		ProductKind:     core.ProductKindNonConsumable,
		State:           core.LifecycleActive,
		ProviderState:   "purchased",
		Access:          core.AccessAllowed,
		AccessReason:    core.AccessReasonPurchaseValid,
		Ownership:       core.OwnershipPurchased,
		Quantity:        1,
		OccurredAt:      verifiedAt.Add(-time.Minute),
		ObservedAt:      verifiedAt,
		EffectivePeriod: core.EffectivePeriod{StartsAt: verifiedAt.Add(-time.Minute)},
		Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
		References:      []core.StoreReference{reference},
	}
	return verificationFixture{
		command: verification.Command{
			ProjectID:          verificationProjectID,
			ApplicationID:      verificationApplicationID,
			ExternalCustomerID: verificationExternalCustomerID,
			ClaimedProducts:    []core.ProviderProductID{verificationProviderProductID},
			Evidence:           evidence,
		},
		application: application,
		customer:    customer,
		catalog:     catalog,
		result: stores.VerificationResult{
			VerifiedAt:   verifiedAt,
			Artifacts:    []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: evidence}},
			Observations: []core.PurchaseObservation{observation},
		},
		clock: fakeClock{now: verifiedAt.Add(time.Second)},
	}
}

// newFakeStore builds an empty transactional repository around one fixture catalog.
func newFakeStore(fixture verificationFixture) *fakeStore {
	return &fakeStore{repository: &fakeTransaction{
		application:  fixture.application,
		customer:     fixture.customer,
		catalog:      map[core.ProviderProductID]persistence.CatalogProduct{fixture.catalog.Mapping.ProviderID: fixture.catalog},
		evidence:     make(map[[32]byte]persistence.EvidenceWrite),
		evidenceIDs:  make(map[[32]byte]int64),
		observations: make(map[core.ObservationID]persistence.ObservationWrite),
		entitlements: make(map[core.EntitlementID]persistence.CustomerEntitlement),
		outbox:       make(map[[32]byte]persistence.OutboxEvent),
	}}
}

// newVerificationService assembles a service with one fake adapter registry.
func newVerificationService(
	t *testing.T,
	store persistence.Store,
	adapter stores.Adapter,
	protector protection.Protector,
	clock verification.Clock,
) *verification.Service {
	t.Helper()

	registry, err := stores.NewRegistry(adapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	service, err := verification.NewService(
		store,
		registry,
		protector,
		verification.NewDefaultProjector(),
		clock,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// projectionFor builds one valid test projection with a chosen source and access status.
func projectionFor(
	productID core.ProductID,
	observationID core.ObservationID,
	access core.AccessStatus,
) persistence.EntitlementProjection {
	reason := core.AccessReasonExpired
	period := core.EffectivePeriod{}
	if access == core.AccessAllowed {
		reason = core.AccessReasonPurchaseValid
		period.StartsAt = time.Now().UTC().Add(-time.Hour)
	}
	return persistence.EntitlementProjection{
		ProjectID:           verificationProjectID,
		CustomerID:          verificationCustomerID,
		EntitlementID:       verificationEntitlementID,
		SourceObservationID: observationID,
		SourceProductID:     productID,
		Access:              access,
		AccessReason:        reason,
		EffectivePeriod:     period,
	}
}

// assertNoDurableWrites verifies that a failed use case committed no purchase state.
func assertNoDurableWrites(t *testing.T, repository *fakeTransaction) {
	t.Helper()

	if len(repository.evidence) != 0 ||
		len(repository.artifacts) != 0 ||
		len(repository.observations) != 0 ||
		len(repository.entitlements) != 0 ||
		len(repository.outbox) != 0 {
		t.Fatalf("repository contains partial writes: %#v", repository)
	}
}

// repositoryContentType returns the JSON media type used by verification fixtures.
func repositoryContentType() string {
	return "application/json"
}
