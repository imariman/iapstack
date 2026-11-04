//go:build integration

package processing

import (
	"encoding/json"
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
	// replacementIntegrationOldProduct identifies the Google Play product a deferred replacement leaves.
	replacementIntegrationOldProduct = "premium_monthly"
	// replacementIntegrationNewProduct identifies the Google Play product a deferred replacement switches to.
	replacementIntegrationNewProduct = "basic_monthly"
	// replacementIntegrationNewEntitlementID identifies the access granted by the replacement product.
	replacementIntegrationNewEntitlementID = "processing-entitlement-basic"
)

// TestGooglePlayInboxFollowsDeferredReplacementWithPostgreSQL verifies RTDN reconciliation after a product change.
// The first notification after the change still expects the stored product. Its result stores the replacement,
// so the next notification for the same token expects the replacement product.
func TestGooglePlayInboxFollowsDeferredReplacementWithPostgreSQL(t *testing.T) {
	database := openProcessingIntegrationDatabase(t)
	now := time.Date(2026, time.November, 3, 10, 0, 0, 0, time.UTC)
	application := core.Application{
		ID: processingIntegrationApplicationID, ProjectID: processingIntegrationProjectID,
		Store: core.StoreApplication{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest, ID: "com.example.iapstack",
		},
	}
	customer := core.Customer{
		ID: processingIntegrationCustomerID, ProjectID: processingIntegrationProjectID,
		ExternalID: processingIntegrationExternalCustomerID,
	}
	seedReplacementIntegrationCatalog(t, database, application, customer)
	references := []core.StoreReference{
		processingIntegrationReference(t, core.ReferenceQuery, "purchase_token", processingIntegrationPurchaseToken),
		processingIntegrationReference(
			t, core.ReferenceCustomerBinding, "obfuscated_external_account_id", processingIntegrationExternalCustomerID,
		),
		processingIntegrationReference(t, core.ReferenceTransaction, "order_id", "GPA.3333-4444-5555-66666"),
	}
	renewedAt := now.Add(30 * 24 * time.Hour)
	completed := replacementIntegrationResult(
		t, application, "processing-observation-completed-replacement", replacementIntegrationNewProduct,
		core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalEnabled},
		renewedAt, references...,
	)
	replaced := replacementIntegrationResult(
		t, application, "processing-observation-replaced-item", replacementIntegrationOldProduct,
		core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalDisabled},
		renewedAt.Add(-time.Microsecond), references...,
	).Observations[0]
	replaced.State, replaced.ProviderState = core.LifecycleExpired, "SUBSCRIPTION_STATE_EXPIRED"
	replaced.Access, replaced.AccessReason = core.AccessDenied, core.AccessReasonExpired
	replacedEnd := renewedAt.Add(-30 * time.Second)
	replaced.EffectivePeriod.EndsAt = &replacedEnd
	completed.Observations = append(completed.Observations, replaced)
	namedProducts := []core.ProviderProductID{replacementIntegrationOldProduct, replacementIntegrationNewProduct}
	completed.NamedProducts = namedProducts
	pending := replacementIntegrationResult(
		t, application, "processing-observation-pending-replacement", replacementIntegrationOldProduct,
		core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalEnabled, NextProductID: replacementIntegrationNewProduct},
		now, references...,
	)
	pending.NamedProducts = namedProducts
	adapter := &processingIntegrationAdapter{verificationResult: pending, reconciliationResult: completed}
	registry, err := stores.NewRegistry(adapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	protector := processingIntegrationProtection{}
	verificationService, err := verification.NewService(
		database.store, registry, protector, verification.NewDefaultProjector(),
		processingIntegrationClock{now: now.Add(30*24*time.Hour + time.Minute)},
	)
	if err != nil {
		t.Fatalf("NewService() verification error = %v", err)
	}
	if _, err := verificationService.Verify(database.ctx, verification.Command{
		ProjectID: processingIntegrationProjectID, ApplicationID: processingIntegrationApplicationID,
		ExternalCustomerID:       processingIntegrationExternalCustomerID,
		ClaimedProducts:          []core.ProviderProductID{replacementIntegrationOldProduct},
		ExpectedCustomerBindings: []core.StoreReference{references[1]},
		Evidence: processingIntegrationEvidence(
			t, "application/json", []byte(`{"purchase_token":"processing-purchase-token-1"}`),
		),
	}); err != nil {
		t.Fatalf("Verify() pending replacement error = %v", err)
	}
	processingService, err := New(database.store, protector, verificationService)
	if err != nil {
		t.Fatalf("New() processing error = %v", err)
	}

	for index, wantExpected := range []core.ProviderProductID{
		replacementIntegrationOldProduct,
		replacementIntegrationNewProduct,
	} {
		message := saveReplacementIntegrationInbox(
			t, database, protector, application, fmt.Sprintf("processing-replacement-inbox-%d", index),
			renewedAt.Add(time.Duration(index)*time.Minute),
		)
		if err := processingService.HandleInbox(database.ctx, message); err != nil {
			t.Fatalf("HandleInbox() notification %d error = %v", index, err)
		}
		if len(adapter.reconcileRequest.ExpectedProducts) != 1 ||
			adapter.reconcileRequest.ExpectedProducts[0] != wantExpected {
			t.Fatalf("notification %d expected products = %#v, want %q",
				index, adapter.reconcileRequest.ExpectedProducts, wantExpected)
		}
	}
	for _, want := range []struct {
		entitlementID core.EntitlementID
		access        core.AccessStatus
		sourceProduct string
	}{
		{entitlementID: processingIntegrationEntitlementID, access: core.AccessDenied, sourceProduct: "processing-product-premium"},
		{entitlementID: replacementIntegrationNewEntitlementID, access: core.AccessAllowed, sourceProduct: "processing-product-basic"},
	} {
		var access core.AccessStatus
		var sourceProduct string
		if err := database.conn.QueryRow(database.ctx, `
			SELECT access_status, source_product_id
			FROM customer_entitlements
			WHERE customer_id = $1 AND entitlement_id = $2
		`, processingIntegrationCustomerID, want.entitlementID).Scan(&access, &sourceProduct); err != nil {
			t.Fatalf("load entitlement %q: %v", want.entitlementID, err)
		}
		if access != want.access || sourceProduct != want.sourceProduct {
			t.Fatalf("entitlement %q = (%q, %q), want (%q, %q)",
				want.entitlementID, access, sourceProduct, want.access, want.sourceProduct)
		}
	}
	assertProcessingIntegrationCount(t, database, "purchase_observations", 3)
}

// seedReplacementIntegrationCatalog creates two subscription products that grant different entitlements.
func seedReplacementIntegrationCatalog(
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
		for _, product := range []struct {
			productID     core.ProductID
			providerID    core.ProviderProductID
			entitlementID core.EntitlementID
			key           string
		}{
			{"processing-product-premium", replacementIntegrationOldProduct, processingIntegrationEntitlementID, "premium"},
			{"processing-product-basic", replacementIntegrationNewProduct, replacementIntegrationNewEntitlementID, "basic"},
		} {
			if err := repository.PutEntitlementDefinition(database.ctx, core.Entitlement{
				ID: product.entitlementID, ProjectID: processingIntegrationProjectID, Key: product.key,
			}); err != nil {
				return err
			}
			if err := repository.PutProduct(database.ctx, core.Product{
				ID: product.productID, ProjectID: processingIntegrationProjectID, Kind: core.ProductKindSubscription,
				EntitlementIDs: []core.EntitlementID{product.entitlementID},
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
		t.Fatalf("seed replacement integration catalog: %v", err)
	}
}

// replacementIntegrationResult builds one active Google Play subscription snapshot for a chosen product.
func replacementIntegrationResult(
	t *testing.T,
	application core.Application,
	observationID core.ObservationID,
	product core.ProviderProductID,
	renewal core.Renewal,
	observedAt time.Time,
	references ...core.StoreReference,
) stores.VerificationResult {
	t.Helper()
	endsAt := observedAt.Add(30 * 24 * time.Hour)
	return stores.VerificationResult{
		VerifiedAt: observedAt,
		Artifacts: []stores.VerifiedArtifact{{
			Kind: "google_play_server_response",
			Evidence: processingIntegrationEvidence(
				t, "application/json", []byte(fmt.Sprintf(`{"observation_id":%q}`, observationID)),
			),
		}},
		Observations: []core.PurchaseObservation{{
			ID: observationID, ApplicationID: application.ID, Store: application.Store,
			ProductID: product, ProductKind: core.ProductKindSubscription,
			State: core.LifecycleActive, ProviderState: "SUBSCRIPTION_STATE_ACTIVE",
			Access: core.AccessAllowed, AccessReason: core.AccessReasonPurchaseValid,
			Ownership: core.OwnershipPurchased, Quantity: 1,
			OccurredAt: observedAt.Add(-time.Minute), ObservedAt: observedAt,
			EffectivePeriod: core.EffectivePeriod{StartsAt: observedAt.Add(-time.Minute), EndsAt: &endsAt},
			Renewal:         renewal,
			References:      append([]core.StoreReference(nil), references...),
		}},
	}
}

// saveReplacementIntegrationInbox persists one protected subscription RTDN for the integration purchase token.
func saveReplacementIntegrationInbox(
	t *testing.T,
	database *processingIntegrationDatabase,
	protector protection.Service,
	application core.Application,
	messageID string,
	eventTime time.Time,
) persistence.QueueMessage {
	t.Helper()
	payload, err := json.Marshal(googleplay.NotificationEnvelope{
		MessageID: messageID, Kind: googleplay.NotificationKindSubscription,
		NotificationType: 2, EventTime: eventTime,
		PurchaseToken: processingIntegrationPurchaseToken, ProductKind: core.ProductKindSubscription,
	})
	if err != nil {
		t.Fatalf("Marshal() notification error = %v", err)
	}
	protected, err := protection.Protect(database.ctx, protector, protection.Scope{
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
		t.Fatalf("save and load replacement inbox: %v", err)
	}
	return message
}
