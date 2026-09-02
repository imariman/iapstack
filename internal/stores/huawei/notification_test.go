package huawei

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

// TestValidateNotificationAcceptsNativeOrderV2 validates Huawei's direct order wrapper.
func TestValidateNotificationAcceptsNativeOrderV2(t *testing.T) {
	adapter := &Adapter{}
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(map[string]any{
		"version": "v2", "eventType": "ORDER", "notifyTime": now.UnixMilli(),
		"applicationId": "provider-app-1",
		"orderNotification": map[string]any{
			"version": "v2", "notificationType": 2,
			"purchaseToken": "purchase-token-1", "productId": "premium_lifetime",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	notification, err := adapter.ValidateNotification(context.Background(), huaweiApplication(), payload)
	if err != nil {
		t.Fatalf("ValidateNotification() error = %v", err)
	}
	if notification.EventType != orderEventType || notification.NotificationType != 2 ||
		notification.PurchaseToken != "purchase-token-1" || notification.ProviderProductID != "premium_lifetime" ||
		notification.ProductKind != core.ProductKindNonConsumable || !notification.NotifyTime.Equal(now) {
		t.Fatalf("ValidateNotification() = %#v", notification)
	}
}

// TestValidateNotificationAuthenticatesNativeSubscriptionV2 validates signed subscription status.
func TestValidateNotificationAuthenticatesNativeSubscriptionV2(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	adapter := notificationAdapter(t, privateKey)
	status := `{"notificationType":2,"subscriptionId":"premium_monthly","purchaseToken":"purchase-token-1"}`
	digest := sha256.Sum256([]byte(status))
	signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, digest[:], &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       crypto.SHA256,
	})
	if err != nil {
		t.Fatalf("SignPSS() error = %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"version": "v2", "eventType": "SUBSCRIPTION", "notifyTime": time.Now().UTC().UnixMilli(),
		"applicationId": "provider-app-1",
		"subNotification": map[string]any{
			"version": "v2", "statusUpdateNotification": status,
			"notificationSignature": base64.StdEncoding.EncodeToString(signature),
			"signatureAlgorithm":    "SHA256withRSA/PSS",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	notification, err := adapter.ValidateNotification(context.Background(), huaweiApplication(), payload)
	if err != nil {
		t.Fatalf("ValidateNotification() error = %v", err)
	}
	if notification.ProductKind != core.ProductKindSubscription || notification.ProviderProductID != "premium_monthly" ||
		notification.PurchaseToken != "purchase-token-1" || notification.NotificationType != 2 {
		t.Fatalf("ValidateNotification() = %#v", notification)
	}

	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	subscription := request["subNotification"].(map[string]any)
	subscription["statusUpdateNotification"] = status + " "
	tampered, _ := json.Marshal(request)
	if _, err := adapter.ValidateNotification(context.Background(), huaweiApplication(), tampered); err == nil {
		t.Fatal("ValidateNotification() tampered signature error = nil, want rejection")
	}
}

// TestValidateNotificationRejectsAmbiguousBodies verifies fail-closed event parsing.
func TestValidateNotificationRejectsAmbiguousBodies(t *testing.T) {
	adapter := &Adapter{}
	payload, _ := json.Marshal(map[string]any{
		"version": "v2", "eventType": "ORDER", "notifyTime": time.Now().UTC().UnixMilli(),
		"applicationId": "provider-app-1",
		"orderNotification": map[string]any{
			"version": "v2", "notificationType": 1,
			"purchaseToken": "purchase-token-1", "productId": "premium_lifetime",
		},
		"subNotification": map[string]any{"version": "v2"},
	})
	if _, err := adapter.ValidateNotification(context.Background(), huaweiApplication(), payload); err == nil {
		t.Fatal("ValidateNotification() ambiguous body error = nil, want rejection")
	}
}

// notificationAdapter builds an adapter with the supplied IAP signing key.
func notificationAdapter(t *testing.T, privateKey *rsa.PrivateKey) *Adapter {
	t.Helper()
	credentialJSON, err := json.Marshal(credentialPayload{
		ClientID: "client", ClientSecret: "secret", PublicKey: publicKeyFixture(t, &privateKey.PublicKey),
		TokenURL: "https://provider.example/token", OrderURL: "https://provider.example/order",
		SubscriptionURL: "https://provider.example/subscription",
	})
	if err != nil {
		t.Fatalf("Marshal() credential error = %v", err)
	}
	credential, err := stores.NewCredential(CredentialKind, CredentialContentType, CredentialSchemaVersion, credentialJSON)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	adapter, err := New(fakeCredentialSource{credential: credential}, time.Second, false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return adapter
}

// huaweiApplication returns the application scope shared by notification fixtures.
func huaweiApplication() core.Application {
	return core.Application{ID: "application-1", ProjectID: "project-1", Store: core.StoreApplication{
		Provider: core.ProviderHuaweiAppGallery, Environment: core.EnvironmentSandbox, ID: "provider-app-1",
	}}
}
