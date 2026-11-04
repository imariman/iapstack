//go:build integration

package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// linkedReplacementOldProduct identifies the Google Play product the customer verified first.
	linkedReplacementOldProduct = "premium_monthly"
	// linkedReplacementNewProduct identifies the Google Play product an immediate replacement switches to.
	linkedReplacementNewProduct = "premium_yearly"
	// linkedReplacementNewToken identifies the replacement purchase that no client ever submitted.
	linkedReplacementNewToken = "processing-replacement-token-2"
	// linkedReplacementOtherExternalID identifies a second project customer.
	linkedReplacementOtherExternalID = "processing-other-external-customer"
	// linkedReplacementOtherCustomerID identifies the second project customer's durable record.
	linkedReplacementOtherCustomerID = "processing-other-customer"
	// linkedReplacementNotificationPurchased is Google's SUBSCRIPTION_PURCHASED notification type.
	linkedReplacementNotificationPurchased = 4
	// linkedReplacementNotificationRenewed is Google's SUBSCRIPTION_RENEWED notification type.
	linkedReplacementNotificationRenewed = 2
	// linkedReplacementNotificationExpired is Google's SUBSCRIPTION_EXPIRED notification type.
	linkedReplacementNotificationExpired = 13
)

// linkedReplacementAdapter returns authoritative Google Play state per purchase token.
// It drops the acknowledgement action once a token is acknowledged, as Google reports the new state.
type linkedReplacementAdapter struct {
	verification     stores.VerificationResult
	reconciliations  map[string]stores.VerificationResult
	linked           stores.LinkedPurchase
	linkedCalls      int
	linkedRequest    stores.LinkedPurchaseRequest
	reconcileTokens  []string
	reconcileRequest stores.ReconciliationRequest
	acknowledged     map[string]bool
	postCommitCalls  int
	beforePostCommit func()
}

var (
	_ stores.Adapter                = (*linkedReplacementAdapter)(nil)
	_ stores.PostCommitter          = (*linkedReplacementAdapter)(nil)
	_ stores.LinkedPurchaseResolver = (*linkedReplacementAdapter)(nil)
)

// TestGooglePlayInboxBindsUnverifiedReplacementTokenWithPostgreSQL verifies the RTDN-only replacement path.
// The old token is verified by the client and then expires because an immediate replacement issued a new token.
// No client submits the new token. Its SUBSCRIPTION_PURCHASED notification follows linkedPurchaseToken to the
// old token's customer, stores the new purchase, acknowledges it after the commit, and keeps the customer's
// access. A redelivery and a later notification for the new token reuse the stored binding.
func TestGooglePlayInboxBindsUnverifiedReplacementTokenWithPostgreSQL(t *testing.T) {
	database := openProcessingIntegrationDatabase(t)
	now := time.Date(2026, time.November, 3, 10, 0, 0, 0, time.UTC)
	replacedAt := now.Add(time.Hour)
	application := linkedReplacementApplication()
	customer := core.Customer{
		ID: processingIntegrationCustomerID, ProjectID: processingIntegrationProjectID,
		ExternalID: processingIntegrationExternalCustomerID,
	}
	seedLinkedReplacementCatalog(t, database, application, customer)
	adapter := newLinkedReplacementAdapter(t, application, processingIntegrationExternalCustomerID, now, replacedAt)
	adapter.beforePostCommit = func() {
		var committed int
		if err := database.conn.QueryRow(database.ctx, `
			SELECT count(*) FROM purchase_observations WHERE id = 'processing-observation-replacement'
		`).Scan(&committed); err != nil {
			t.Errorf("read replacement observation before acknowledgement: %v", err)
		}
		if committed != 1 {
			t.Errorf("replacement observation committed before acknowledgement = %d, want 1", committed)
		}
	}
	processingService := newLinkedReplacementProcessing(t, database, adapter, replacedAt)

	expired := saveLinkedReplacementInbox(
		t, database, application, "processing-old-expired", processingIntegrationPurchaseToken,
		linkedReplacementNotificationExpired, replacedAt,
	)
	if err := processingService.HandleInbox(database.ctx, expired); err != nil {
		t.Fatalf("HandleInbox() old token expiry error = %v", err)
	}
	assertLinkedReplacementAccess(t, database, processingIntegrationCustomerID, core.AccessDenied, "processing-product-monthly")

	purchased := saveLinkedReplacementInbox(
		t, database, application, "processing-new-purchased", linkedReplacementNewToken,
		linkedReplacementNotificationPurchased, replacedAt,
	)
	if err := processingService.HandleInbox(database.ctx, purchased); err != nil {
		t.Fatalf("HandleInbox() replacement purchase error = %v, want binding through linkedPurchaseToken", err)
	}
	if adapter.linkedCalls != 1 || len(adapter.linkedRequest.QueryReferences) != 1 ||
		adapter.linkedRequest.QueryReferences[0].Value() != linkedReplacementNewToken ||
		adapter.linkedRequest.ProductKind != core.ProductKindSubscription {
		t.Fatalf("linked purchase lookup = (%d, %#v)", adapter.linkedCalls, adapter.linkedRequest)
	}
	assertLinkedReplacementReconcile(t, adapter, linkedReplacementNewProduct, processingIntegrationExternalCustomerID)
	if adapter.postCommitCalls != 1 {
		t.Fatalf("acknowledgements = %d, want 1", adapter.postCommitCalls)
	}
	assertLinkedReplacementAccess(t, database, processingIntegrationCustomerID, core.AccessAllowed, "processing-product-yearly")
	assertProcessingIntegrationCount(t, database, "purchase_observations", 3)
	assertLinkedReplacementReconciliationJobs(t, database, processingIntegrationCustomerID, 2)

	if err := processingService.HandleInbox(database.ctx, purchased); err != nil {
		t.Fatalf("HandleInbox() redelivered replacement purchase error = %v", err)
	}
	renewed := saveLinkedReplacementInbox(
		t, database, application, "processing-new-renewed", linkedReplacementNewToken,
		linkedReplacementNotificationRenewed, replacedAt.Add(time.Minute),
	)
	if err := processingService.HandleInbox(database.ctx, renewed); err != nil {
		t.Fatalf("HandleInbox() later replacement notification error = %v", err)
	}
	if adapter.linkedCalls != 1 {
		t.Fatalf("linked purchase lookups after the binding = %d, want 1", adapter.linkedCalls)
	}
	assertLinkedReplacementReconcile(t, adapter, linkedReplacementNewProduct, processingIntegrationExternalCustomerID)
	if adapter.postCommitCalls != 1 {
		t.Fatalf("acknowledgements after redelivery = %d, want 1", adapter.postCommitCalls)
	}
	assertLinkedReplacementAccess(t, database, processingIntegrationCustomerID, core.AccessAllowed, "processing-product-yearly")
	assertProcessingIntegrationCount(t, database, "purchase_observations", 3)
	assertLinkedReplacementReconciliationJobs(t, database, processingIntegrationCustomerID, 2)
}

// TestGooglePlayInboxBindsReplacementOnlyForTheLinkedCustomerWithPostgreSQL verifies the account check.
// A replacement whose obfuscated account id names an unrelated customer is rejected without a provider refresh
// or a stored observation. Once that customer is the canonical customer of the old token's customer, the
// replacement binds to it.
func TestGooglePlayInboxBindsReplacementOnlyForTheLinkedCustomerWithPostgreSQL(t *testing.T) {
	database := openProcessingIntegrationDatabase(t)
	now := time.Date(2026, time.November, 3, 10, 0, 0, 0, time.UTC)
	replacedAt := now.Add(time.Hour)
	application := linkedReplacementApplication()
	customer := core.Customer{
		ID: processingIntegrationCustomerID, ProjectID: processingIntegrationProjectID,
		ExternalID: processingIntegrationExternalCustomerID,
	}
	seedLinkedReplacementCatalog(t, database, application, customer)
	if err := database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, putErr := repository.PutCustomer(database.ctx, core.Customer{
			ID: linkedReplacementOtherCustomerID, ProjectID: processingIntegrationProjectID,
			ExternalID: linkedReplacementOtherExternalID,
		})
		return putErr
	}); err != nil {
		t.Fatalf("PutCustomer() other customer error = %v", err)
	}
	adapter := newLinkedReplacementAdapter(t, application, linkedReplacementOtherExternalID, now, replacedAt)
	processingService := newLinkedReplacementProcessing(t, database, adapter, replacedAt)

	rejected := saveLinkedReplacementInbox(
		t, database, application, "processing-new-other-account", linkedReplacementNewToken,
		linkedReplacementNotificationPurchased, replacedAt,
	)
	err := processingService.HandleInbox(database.ctx, rejected)
	var failure *stores.Failure
	if !errors.As(err, &failure) || failure.Kind != stores.FailureInvalidEvidence || failure.Retryable() {
		t.Fatalf("HandleInbox() other account error = %#v, want permanent invalid evidence", err)
	}
	if len(adapter.reconcileTokens) != 0 || adapter.postCommitCalls != 0 {
		t.Fatalf("rejected replacement reached the provider: reconciles %v, acknowledgements %d",
			adapter.reconcileTokens, adapter.postCommitCalls)
	}
	assertProcessingIntegrationCount(t, database, "purchase_observations", 1)

	if err := database.store.Transact(database.ctx, func(repository persistence.Transaction) error {
		if _, mergeErr := repository.MergeCustomers(database.ctx, persistence.CustomerMergeWrite{
			ProjectID: processingIntegrationProjectID, CanonicalCustomerID: linkedReplacementOtherCustomerID,
			SourceCustomerID: processingIntegrationCustomerID, IdempotencyKey: "merge-linked-replacement",
			ActorKeyID: "processing-admin-key",
		}); mergeErr != nil {
			return mergeErr
		}
		return verification.RefreshCustomerProjection(
			database.ctx, repository, verification.NewDefaultProjector(), processingIntegrationProjectID,
			linkedReplacementOtherCustomerID, now,
		)
	}); err != nil {
		t.Fatalf("MergeCustomers() error = %v", err)
	}
	merged := saveLinkedReplacementInbox(
		t, database, application, "processing-new-merged-account", linkedReplacementNewToken,
		linkedReplacementNotificationPurchased, replacedAt.Add(time.Minute),
	)
	if err := processingService.HandleInbox(database.ctx, merged); err != nil {
		t.Fatalf("HandleInbox() merged account error = %v", err)
	}
	assertLinkedReplacementReconcile(t, adapter, linkedReplacementNewProduct, linkedReplacementOtherExternalID)
	assertLinkedReplacementAccess(t, database, linkedReplacementOtherCustomerID, core.AccessAllowed, "processing-product-yearly")
	assertProcessingIntegrationCount(t, database, "purchase_observations", 2)
}

// linkedReplacementApplication returns the Google Play test application.
func linkedReplacementApplication() core.Application {
	return core.Application{
		ID: processingIntegrationApplicationID, ProjectID: processingIntegrationProjectID,
		Store: core.StoreApplication{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest, ID: "com.example.iapstack",
		},
	}
}

// newLinkedReplacementAdapter builds the old token's client verification, its expiry, and the replacement.
// accountID is the obfuscated account id Google reports on the replacement purchase.
func newLinkedReplacementAdapter(
	t *testing.T,
	application core.Application,
	accountID string,
	now time.Time,
	replacedAt time.Time,
) *linkedReplacementAdapter {
	t.Helper()
	oldReferences := linkedReplacementReferences(
		t, processingIntegrationPurchaseToken, "", processingIntegrationExternalCustomerID, "GPA.1111-2222-3333-44444",
	)
	newReferences := linkedReplacementReferences(
		t, linkedReplacementNewToken, processingIntegrationPurchaseToken, accountID, "GPA.5555-6666-7777-88888",
	)
	active := replacementIntegrationResult(
		t, application, "processing-observation-old-active", linkedReplacementOldProduct,
		core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalEnabled}, now, oldReferences...,
	)
	expired := replacementIntegrationResult(
		t, application, "processing-observation-old-expired", linkedReplacementOldProduct,
		core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalDisabled}, replacedAt, oldReferences...,
	)
	expiredEnd := replacedAt.Add(-time.Second)
	expired.Observations[0].State, expired.Observations[0].ProviderState = core.LifecycleExpired, "SUBSCRIPTION_STATE_EXPIRED"
	expired.Observations[0].Access, expired.Observations[0].AccessReason = core.AccessDenied, core.AccessReasonExpired
	expired.Observations[0].EffectivePeriod.EndsAt = &expiredEnd
	replacement := replacementIntegrationResult(
		t, application, "processing-observation-replacement", linkedReplacementNewProduct,
		core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalEnabled}, replacedAt, newReferences...,
	)
	replacement.PostCommitActions = []stores.PostCommitAction{{
		Kind: "acknowledge_purchase", ProductID: linkedReplacementNewProduct, ProductKind: core.ProductKindSubscription,
		QueryReferences: replacement.Observations[0].ReferencesFor(core.ReferenceQuery),
	}}
	// An immediate replacement names the replaced product in itemReplacement, as the Google Play adapter reports.
	replacement.NamedProducts = []core.ProviderProductID{linkedReplacementNewProduct, linkedReplacementOldProduct}
	return &linkedReplacementAdapter{
		verification: active,
		reconciliations: map[string]stores.VerificationResult{
			processingIntegrationPurchaseToken: expired,
			linkedReplacementNewToken:          replacement,
		},
		linked: stores.LinkedPurchase{
			ProductID: linkedReplacementNewProduct,
			LinkedReferences: []core.StoreReference{processingIntegrationReference(
				t, core.ReferenceQuery, "purchase_token", processingIntegrationPurchaseToken,
			)},
			CustomerBindings: []core.StoreReference{processingIntegrationReference(
				t, core.ReferenceCustomerBinding, "obfuscated_external_account_id", accountID,
			)},
		},
		acknowledged: map[string]bool{},
	}
}

// linkedReplacementReferences builds the protected references Google Play normalization stores for one token.
func linkedReplacementReferences(
	t *testing.T,
	token string,
	linkedToken string,
	accountID string,
	orderID string,
) []core.StoreReference {
	t.Helper()
	references := []core.StoreReference{
		processingIntegrationReference(t, core.ReferenceTransaction, "order_id", orderID),
		processingIntegrationReference(t, core.ReferenceLineage, "purchase_token", token),
		processingIntegrationReference(t, core.ReferenceQuery, "purchase_token", token),
		processingIntegrationReference(t, core.ReferenceCustomerBinding, "obfuscated_external_account_id", accountID),
	}
	if linkedToken != "" {
		references = append(references, processingIntegrationReference(
			t, core.ReferenceLinkedLineage, "purchase_token", linkedToken,
		))
	}
	return references
}

// newLinkedReplacementProcessing verifies the old token for the customer and returns the RTDN processor.
func newLinkedReplacementProcessing(
	t *testing.T,
	database *processingIntegrationDatabase,
	adapter *linkedReplacementAdapter,
	now time.Time,
) *Service {
	t.Helper()
	registry, err := stores.NewRegistry(adapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	protector := processingIntegrationProtection{}
	verificationService, err := verification.NewService(
		database.store, registry, protector, verification.NewDefaultProjector(), processingIntegrationClock{now: now},
	)
	if err != nil {
		t.Fatalf("NewService() verification error = %v", err)
	}
	if _, err := verificationService.Verify(database.ctx, verification.Command{
		ProjectID: processingIntegrationProjectID, ApplicationID: processingIntegrationApplicationID,
		ExternalCustomerID: processingIntegrationExternalCustomerID,
		ClaimedProducts:    []core.ProviderProductID{linkedReplacementOldProduct},
		ExpectedCustomerBindings: []core.StoreReference{processingIntegrationReference(
			t, core.ReferenceCustomerBinding, "obfuscated_external_account_id", processingIntegrationExternalCustomerID,
		)},
		Evidence: processingIntegrationEvidence(
			t, "application/json", []byte(`{"purchase_token":"processing-purchase-token-1"}`),
		),
	}); err != nil {
		t.Fatalf("Verify() old token error = %v", err)
	}
	processingService, err := New(database.store, protector, verificationService)
	if err != nil {
		t.Fatalf("New() processing error = %v", err)
	}
	return processingService
}

// seedLinkedReplacementCatalog creates two subscription products that grant the same entitlement.
func seedLinkedReplacementCatalog(
	t *testing.T,
	database *processingIntegrationDatabase,
	application core.Application,
	customer core.Customer,
) {
	t.Helper()
	err := database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		if err := repository.PutProject(database.ctx, processingIntegrationProjectID); err != nil {
			return err
		}
		if err := repository.PutApplication(database.ctx, application); err != nil {
			return err
		}
		if _, err := repository.PutCustomer(database.ctx, customer); err != nil {
			return err
		}
		if err := repository.PutEntitlementDefinition(database.ctx, core.Entitlement{
			ID: processingIntegrationEntitlementID, ProjectID: processingIntegrationProjectID, Key: "premium",
		}); err != nil {
			return err
		}
		for _, product := range []struct {
			productID  core.ProductID
			providerID core.ProviderProductID
		}{
			{"processing-product-monthly", linkedReplacementOldProduct},
			{"processing-product-yearly", linkedReplacementNewProduct},
		} {
			if err := repository.PutProduct(database.ctx, core.Product{
				ID: product.productID, ProjectID: processingIntegrationProjectID, Kind: core.ProductKindSubscription,
				EntitlementIDs: []core.EntitlementID{processingIntegrationEntitlementID},
			}); err != nil {
				return err
			}
			if err := repository.PutStoreProduct(database.ctx, processingIntegrationProjectID, core.StoreProduct{
				ApplicationID: processingIntegrationApplicationID, ProductID: product.productID,
				ProviderID: product.providerID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed linked replacement catalog: %v", err)
	}
}

// saveLinkedReplacementInbox persists one protected subscription RTDN for a chosen purchase token.
func saveLinkedReplacementInbox(
	t *testing.T,
	database *processingIntegrationDatabase,
	application core.Application,
	messageID string,
	token string,
	notificationType int,
	eventTime time.Time,
) persistence.QueueMessage {
	t.Helper()
	payload, err := json.Marshal(googleplay.NotificationEnvelope{
		MessageID: messageID, Kind: googleplay.NotificationKindSubscription,
		NotificationType: notificationType, EventTime: eventTime,
		PurchaseToken: token, ProductKind: core.ProductKindSubscription,
	})
	if err != nil {
		t.Fatalf("Marshal() notification error = %v", err)
	}
	protected, err := protection.Protect(database.ctx, processingIntegrationProtection{}, protection.Scope{
		ProjectID: application.ProjectID, ApplicationID: application.ID, Purpose: inboxProtectionPurpose,
	}, payload)
	if err != nil {
		t.Fatalf("Protect() inbox error = %v", err)
	}
	var message persistence.QueueMessage
	err = database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		id, saveErr := repository.SaveInboxMessage(database.ctx, persistence.InboxMessage{
			ID: messageID, ProjectID: application.ProjectID, ApplicationID: application.ID,
			Provider: application.Store.Provider, Kind: "google_play_rtdn",
			ContentType: googleplay.NotificationContentType, Payload: protected,
			ReceivedAt: eventTime, AvailableAt: eventTime,
		})
		if saveErr != nil {
			return saveErr
		}
		message, saveErr = repository.QueueMessage(database.ctx, persistence.QueueInbox, id)
		return saveErr
	})
	if err != nil {
		t.Fatalf("save and load linked replacement inbox: %v", err)
	}
	return message
}

// assertLinkedReplacementReconcile checks the last refresh used the replacement token, product, and account.
func assertLinkedReplacementReconcile(
	t *testing.T,
	adapter *linkedReplacementAdapter,
	wantProduct core.ProviderProductID,
	wantAccountID string,
) {
	t.Helper()
	request := adapter.reconcileRequest
	if len(adapter.reconcileTokens) == 0 || adapter.reconcileTokens[len(adapter.reconcileTokens)-1] != linkedReplacementNewToken {
		t.Fatalf("reconciled tokens = %v, want the replacement token last", adapter.reconcileTokens)
	}
	if len(request.ExpectedProducts) != 1 || request.ExpectedProducts[0] != wantProduct ||
		request.ExpectedProductKind != core.ProductKindSubscription ||
		len(request.ExpectedCustomerBindings) != 1 || request.ExpectedCustomerBindings[0].Value() != wantAccountID {
		t.Fatalf("replacement reconcile request = %#v", request)
	}
}

// assertLinkedReplacementAccess checks one customer's premium projection and its source product.
func assertLinkedReplacementAccess(
	t *testing.T,
	database *processingIntegrationDatabase,
	customerID core.CustomerID,
	wantAccess core.AccessStatus,
	wantProduct core.ProductID,
) {
	t.Helper()
	var access core.AccessStatus
	var product core.ProductID
	if err := database.conn.QueryRow(database.ctx, `
		SELECT access_status, source_product_id
		FROM customer_entitlements
		WHERE customer_id = $1 AND entitlement_id = $2
	`, customerID, processingIntegrationEntitlementID).Scan(&access, &product); err != nil {
		t.Fatalf("load customer %q entitlement: %v", customerID, err)
	}
	if access != wantAccess || product != wantProduct {
		t.Fatalf("customer %q entitlement = (%q, %q), want (%q, %q)", customerID, access, product, wantAccess, wantProduct)
	}
}

// assertLinkedReplacementReconciliationJobs counts one customer's scheduled daily refreshes.
func assertLinkedReplacementReconciliationJobs(
	t *testing.T,
	database *processingIntegrationDatabase,
	customerID core.CustomerID,
	want int,
) {
	t.Helper()
	var got int
	if err := database.conn.QueryRow(database.ctx, `
		SELECT count(*) FROM reconciliation_jobs WHERE customer_id = $1
	`, customerID).Scan(&got); err != nil {
		t.Fatalf("count reconciliation jobs: %v", err)
	}
	if got != want {
		t.Fatalf("reconciliation jobs = %d, want %d", got, want)
	}
}

// Provider identifies the Google Play test adapter.
func (adapter *linkedReplacementAdapter) Provider() core.Provider {
	return core.ProviderGooglePlay
}

// Verify returns the old token's client verification.
func (adapter *linkedReplacementAdapter) Verify(
	_ context.Context,
	_ stores.VerificationRequest,
) (stores.VerificationResult, error) {
	return adapter.verification, nil
}

// Reconcile returns the configured state of the queried purchase token.
func (adapter *linkedReplacementAdapter) Reconcile(
	_ context.Context,
	request stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	token := ""
	for _, reference := range request.QueryReferences {
		if reference.Kind == "purchase_token" {
			token = reference.Value()
		}
	}
	adapter.reconcileTokens = append(adapter.reconcileTokens, token)
	adapter.reconcileRequest = request
	result, exists := adapter.reconciliations[token]
	if !exists {
		return stores.VerificationResult{}, fmt.Errorf("unexpected purchase token %q", token)
	}
	if adapter.acknowledged[token] {
		result.PostCommitActions = nil
	}
	return result, nil
}

// PostCommit records one acknowledgement after checking that the purchase was committed first.
func (adapter *linkedReplacementAdapter) PostCommit(
	_ context.Context,
	request stores.PostCommitRequest,
) error {
	if adapter.beforePostCommit != nil {
		adapter.beforePostCommit()
	}
	adapter.postCommitCalls++
	for _, action := range request.Actions {
		for _, reference := range action.QueryReferences {
			adapter.acknowledged[reference.Value()] = true
		}
	}
	return nil
}

// LinkedPurchase returns the configured replacement link without a customer.
func (adapter *linkedReplacementAdapter) LinkedPurchase(
	_ context.Context,
	request stores.LinkedPurchaseRequest,
) (stores.LinkedPurchase, error) {
	adapter.linkedCalls++
	adapter.linkedRequest = request
	return adapter.linked, nil
}
