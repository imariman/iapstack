package apple

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// fixtureNotificationUUID identifies one App Store notification delivery across retries.
	fixtureNotificationUUID = "018f59d0-a200-7000-8000-000000000010"
)

// TestValidateNotificationAuthenticatesV2TransactionAndRenewal verifies all three signed layers.
func TestValidateNotificationAuthenticatesV2TransactionAndRenewal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	adapter, err := New(
		fakeCredentialSource{credential: credentialFixture(t, fixture, core.EnvironmentSandbox)},
		time.Second,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	transaction := notificationTransaction(now)
	renewal := renewalPayload{
		AppAccountToken: transaction.AppAccountToken, AutoRenewProductID: transaction.ProductID,
		AutoRenewStatus: autoRenewEnabled, Environment: transaction.Environment,
		OriginalTransactionID: transaction.OriginalTransactionID, ProductID: transaction.ProductID,
		RenewalDate: now.Add(24 * time.Hour).UnixMilli(), SignedDate: now.UnixMilli(),
	}
	payload := notificationRequestFixture(t, fixture, notificationPayload{
		NotificationType: "DID_RENEW", Subtype: "", NotificationUUID: fixtureNotificationUUID,
		Version: notificationVersion, SignedDate: now.UnixMilli(),
		Data: &notificationData{
			BundleID: fixtureBundleID, Environment: "Sandbox", Status: subscriptionStatusActive,
			SignedTransactionInfo: signTransactionFixture(t, fixture, transaction),
			SignedRenewalInfo:     signApplePayloadFixture(t, fixture, renewal),
		},
	})

	notification, err := adapter.ValidateNotification(
		context.Background(), appleApplication(core.EnvironmentSandbox), payload,
	)
	if err != nil {
		t.Fatalf("ValidateNotification() error = %v", err)
	}
	if notification.NotificationUUID != fixtureNotificationUUID || notification.NotificationType != "DID_RENEW" ||
		notification.ExternalCustomerID != fixtureAccountToken || notification.ProviderProductID != fixtureProductID ||
		notification.ProductKind != core.ProductKindSubscription {
		t.Fatalf("ValidateNotification() = %#v", notification)
	}
	evidence, process, err := notification.VerificationEvidence()
	if err != nil || !process {
		t.Fatalf("VerificationEvidence() = (%s, %t, %v)", evidence, process, err)
	}
	var decoded clientEvidence
	if err := json.Unmarshal(evidence, &decoded); err != nil {
		t.Fatalf("Unmarshal() evidence error = %v", err)
	}
	if decoded.ProductKind != core.ProductKindSubscription || decoded.SignedTransaction == "" {
		t.Fatalf("VerificationEvidence() decoded = %#v", decoded)
	}
}

// TestValidateNotificationRejectsTamperingAndCrossApplicationData verifies signed scope isolation.
func TestValidateNotificationRejectsTamperingAndCrossApplicationData(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 26, 13, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	adapter, err := New(
		fakeCredentialSource{credential: credentialFixture(t, fixture, core.EnvironmentSandbox)},
		time.Second,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	transaction := notificationTransaction(now)
	valid := notificationPayload{
		NotificationType: "EXPIRED", NotificationUUID: fixtureNotificationUUID,
		Version: notificationVersion, SignedDate: now.UnixMilli(),
		Data: &notificationData{
			BundleID: fixtureBundleID, Environment: "Sandbox", Status: subscriptionStatusExpired,
			SignedTransactionInfo: signTransactionFixture(t, fixture, transaction),
		},
	}
	payload := notificationRequestFixture(t, fixture, valid)
	var wrapper notificationRequest
	if err := json.Unmarshal(payload, &wrapper); err != nil {
		t.Fatalf("Unmarshal() wrapper error = %v", err)
	}
	parts := strings.Split(wrapper.SignedPayload, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"notificationType":"forged"}`))
	wrapper.SignedPayload = strings.Join(parts, ".")
	tampered, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatalf("Marshal() tampered wrapper error = %v", err)
	}
	if _, err := adapter.ValidateNotification(context.Background(), appleApplication(core.EnvironmentSandbox), tampered); !errors.Is(err, ErrNotificationInvalid) {
		t.Fatalf("ValidateNotification() tampered error = %v", err)
	}

	valid.Data.BundleID = "com.example.other"
	wrongScope := notificationRequestFixture(t, fixture, valid)
	if _, err := adapter.ValidateNotification(context.Background(), appleApplication(core.EnvironmentSandbox), wrongScope); !errors.Is(err, ErrNotificationInvalid) {
		t.Fatalf("ValidateNotification() wrong scope error = %v", err)
	}

	valid.Data.BundleID = fixtureBundleID
	renewal := renewalPayload{
		AppAccountToken: transaction.AppAccountToken, AutoRenewProductID: transaction.ProductID,
		AutoRenewStatus: autoRenewEnabled, Environment: transaction.Environment,
		OriginalTransactionID: transaction.OriginalTransactionID, ProductID: transaction.ProductID,
		SignedDate: now.UnixMilli(),
	}
	tamperedRenewal := strings.Split(signApplePayloadFixture(t, fixture, renewal), ".")
	tamperedRenewal[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"signedDate":1}`))
	valid.Data.SignedRenewalInfo = strings.Join(tamperedRenewal, ".")
	wrongRenewal := notificationRequestFixture(t, fixture, valid)
	if _, err := adapter.ValidateNotification(context.Background(), appleApplication(core.EnvironmentSandbox), wrongRenewal); !errors.Is(err, ErrNotificationInvalid) {
		t.Fatalf("ValidateNotification() tampered renewal error = %v", err)
	}
}

// TestValidateNotificationAcceptsAuditOnlySignal verifies signed test deliveries do not trigger verification.
func TestValidateNotificationAcceptsAuditOnlySignal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 26, 14, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	adapter, err := New(
		fakeCredentialSource{credential: credentialFixture(t, fixture, core.EnvironmentSandbox)},
		time.Second,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	payload := notificationRequestFixture(t, fixture, notificationPayload{
		NotificationType: "TEST", NotificationUUID: fixtureNotificationUUID,
		Version: notificationVersion, SignedDate: now.UnixMilli(),
	})
	notification, err := adapter.ValidateNotification(
		context.Background(), appleApplication(core.EnvironmentSandbox), payload,
	)
	if err != nil {
		t.Fatalf("ValidateNotification() error = %v", err)
	}
	evidence, process, err := notification.VerificationEvidence()
	if err != nil || process || evidence != nil {
		t.Fatalf("VerificationEvidence() = (%s, %t, %v)", evidence, process, err)
	}
}

// notificationTransaction creates one valid subscription transaction for V2 notification tests.
func notificationTransaction(now time.Time) transactionPayload {
	return transactionPayload{
		OriginalTransactionID: "2000000823456789", TransactionID: "2000000823456790",
		BundleID: fixtureBundleID, ProductID: fixtureProductID,
		PurchaseDate: now.Add(-time.Hour).UnixMilli(), ExpiresDate: now.Add(time.Hour).UnixMilli(),
		Quantity: 1, Type: appleSubscriptionType, AppAccountToken: fixtureAccountToken,
		OwnershipType: applePurchasedOwnership, SignedDate: now.UnixMilli(), Environment: "Sandbox",
	}
}

// notificationRequestFixture signs one outer V2 payload and wraps it in Apple's JSON request body.
func notificationRequestFixture(t *testing.T, fixture signingFixture, payload notificationPayload) []byte {
	t.Helper()
	encoded, err := json.Marshal(notificationRequest{
		SignedPayload: signApplePayloadFixture(t, fixture, payload),
	})
	if err != nil {
		t.Fatalf("Marshal() notification request error = %v", err)
	}
	return encoded
}
