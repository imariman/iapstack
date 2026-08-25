package googleplay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

// fakeNotificationTokenValidator returns deterministic Pub/Sub identity claims.
type fakeNotificationTokenValidator struct {
	claims   notificationTokenClaims
	err      error
	token    string
	audience string
}

// TestValidateNotificationAuthenticatesAndNormalizesSubscription verifies OIDC scope and RTDN decoding.
func TestValidateNotificationAuthenticatesAndNormalizesSubscription(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentProduction)
	validator := &fakeNotificationTokenValidator{claims: notificationTokenClaims{
		Email: fixtureRTDNServiceAccount, EmailVerified: true,
	}}
	fixture.adapter.notificationTokens = validator
	payload := notificationPush(t, map[string]any{
		"version": "1.0", "packageName": fixturePackageName,
		"eventTimeMillis": "1787666400000",
		"subscriptionNotification": map[string]any{
			"version": "1.0", "notificationType": 2, "purchaseToken": fixturePurchaseToken,
		},
	})
	notification, err := fixture.adapter.ValidateNotification(
		context.Background(), googleApplication(core.EnvironmentProduction), "signed-pubsub-token", payload,
	)
	if err != nil {
		t.Fatalf("ValidateNotification() error = %v", err)
	}
	if validator.token != "signed-pubsub-token" || validator.audience != fixtureRTDNAudience {
		t.Fatalf("token validation scope = (%q, %q)", validator.token, validator.audience)
	}
	if notification.MessageID != "pubsub-message-1" || notification.Kind != NotificationKindSubscription ||
		notification.NotificationType != 2 || notification.PurchaseToken != fixturePurchaseToken ||
		notification.ProductKind != core.ProductKindSubscription || notification.EventTime.IsZero() {
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
	if decoded.PurchaseToken != fixturePurchaseToken || decoded.ProductKind != core.ProductKindSubscription {
		t.Fatalf("VerificationEvidence() decoded = %#v", decoded)
	}
}

// TestValidateNotificationNormalizesLifecycleVariants verifies one-time and voided purchase lookup metadata.
func TestValidateNotificationNormalizesLifecycleVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		variant           map[string]any
		kind              NotificationKind
		productKind       core.ProductKind
		providerProductID core.ProviderProductID
	}{
		{
			name: "one-time product",
			variant: map[string]any{"oneTimeProductNotification": map[string]any{
				"version": "1.0", "notificationType": 1,
				"purchaseToken": fixturePurchaseToken, "sku": fixtureProductID,
			}},
			kind: NotificationKindOneTimeProduct, productKind: core.ProductKindNonConsumable,
			providerProductID: fixtureProductID,
		},
		{
			name: "voided subscription",
			variant: map[string]any{"voidedPurchaseNotification": map[string]any{
				"purchaseToken": fixturePurchaseToken, "orderId": "GPA.1234-5678-9012-34567",
				"productType": 1, "refundType": 1,
			}},
			kind: NotificationKindVoidedPurchase, productKind: core.ProductKindSubscription,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			developerPayload := map[string]any{
				"version": "1.0", "packageName": fixturePackageName, "eventTimeMillis": 1787666400000,
			}
			for key, value := range test.variant {
				developerPayload[key] = value
			}
			notification, err := decodeNotification(
				googleApplication(core.EnvironmentProduction),
				rtdnConfiguration{
					Subscription: fixtureRTDNSubscription, Audience: fixtureRTDNAudience,
					PushServiceAccountEmail: fixtureRTDNServiceAccount,
				},
				notificationPush(t, developerPayload),
			)
			if err != nil {
				t.Fatalf("decodeNotification() error = %v", err)
			}
			if notification.Kind != test.kind || notification.ProductKind != test.productKind ||
				notification.ProviderProductID != test.providerProductID {
				t.Fatalf("decodeNotification() = %#v", notification)
			}
		})
	}
}

// TestValidateNotificationRejectsAuthenticationAndScopeMismatch verifies fail-closed Pub/Sub binding.
func TestValidateNotificationRejectsAuthenticationAndScopeMismatch(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentProduction)
	payload := notificationPush(t, map[string]any{
		"version": "1.0", "packageName": fixturePackageName, "eventTimeMillis": "1787666400000",
		"testNotification": map[string]any{"version": "1.0"},
	})
	fixture.adapter.notificationTokens = &fakeNotificationTokenValidator{claims: notificationTokenClaims{
		Email: "wrong@example-project.iam.gserviceaccount.com", EmailVerified: true,
	}}
	_, err := fixture.adapter.ValidateNotification(
		context.Background(), googleApplication(core.EnvironmentProduction), "signed-pubsub-token", payload,
	)
	if !errors.Is(err, ErrNotificationUnauthorized) {
		t.Fatalf("ValidateNotification() email error = %v", err)
	}

	fixture.adapter.notificationTokens = &fakeNotificationTokenValidator{claims: notificationTokenClaims{
		Email: fixtureRTDNServiceAccount, EmailVerified: true,
	}}
	wrongPackage := notificationPush(t, map[string]any{
		"version": "1.0", "packageName": "com.example.other", "eventTimeMillis": "1787666400000",
		"testNotification": map[string]any{"version": "1.0"},
	})
	_, err = fixture.adapter.ValidateNotification(
		context.Background(), googleApplication(core.EnvironmentProduction), "signed-pubsub-token", wrongPackage,
	)
	if !errors.Is(err, ErrNotificationInvalid) {
		t.Fatalf("ValidateNotification() package error = %v", err)
	}
}

// TestNotificationRejectsMultipleVariantsAndSkipsAuditOnlySignals verifies exclusivity and no-op signal handling.
func TestNotificationRejectsMultipleVariantsAndSkipsAuditOnlySignals(t *testing.T) {
	t.Parallel()

	configuration := rtdnConfiguration{
		Subscription: fixtureRTDNSubscription, Audience: fixtureRTDNAudience,
		PushServiceAccountEmail: fixtureRTDNServiceAccount,
	}
	multiple := notificationPush(t, map[string]any{
		"version": "1.0", "packageName": fixturePackageName, "eventTimeMillis": "1787666400000",
		"testNotification": map[string]any{"version": "1.0"},
		"subscriptionNotification": map[string]any{
			"version": "1.0", "notificationType": 2, "purchaseToken": fixturePurchaseToken,
		},
	})
	if _, err := decodeNotification(googleApplication(core.EnvironmentProduction), configuration, multiple); !errors.Is(err, ErrNotificationInvalid) {
		t.Fatalf("decodeNotification() multiple variants error = %v", err)
	}

	testEnvelope := NotificationEnvelope{
		MessageID: "pubsub-test", Kind: NotificationKindTest,
		EventTime: time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC),
	}
	if evidence, process, err := testEnvelope.VerificationEvidence(); err != nil || process || evidence != nil {
		t.Fatalf("VerificationEvidence() test signal = (%s, %t, %v)", evidence, process, err)
	}
}

// Validate records the supplied token and audience before returning deterministic claims.
func (validator *fakeNotificationTokenValidator) Validate(
	_ context.Context,
	token string,
	audience string,
) (notificationTokenClaims, error) {
	validator.token = token
	validator.audience = audience
	return validator.claims, validator.err
}

// googleApplication builds the Google Play application scope used by notification tests.
func googleApplication(environment core.Environment) core.Application {
	return core.Application{
		ID: "application-1", ProjectID: "project-1",
		Store: core.StoreApplication{
			Provider: core.ProviderGooglePlay, Environment: environment, ID: fixturePackageName,
		},
	}
}

// notificationPush wraps one developer notification in the documented Pub/Sub JSON envelope.
func notificationPush(t *testing.T, developerPayload map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(developerPayload)
	if err != nil {
		t.Fatalf("Marshal() developer notification error = %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"data": base64.StdEncoding.EncodeToString(data), "messageId": "pubsub-message-1",
		},
		"subscription": fixtureRTDNSubscription,
	})
	if err != nil {
		t.Fatalf("Marshal() Pub/Sub push error = %v", err)
	}
	return payload
}
