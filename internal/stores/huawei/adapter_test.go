package huawei

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

// fakeCredentialSource returns one opaque Huawei credential fixture.
type fakeCredentialSource struct {
	credential stores.Credential
}

// TestAdapterVerifiesAuthoritativeLifetimePurchase exercises OAuth, server query, RSA, and normalization.
func TestAdapterVerifiesAuthoritativeLifetimePurchase(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	purchasedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	purchase := purchaseData{
		ApplicationID: "provider-app-1", ProductID: "lifetime", OrderID: "order-1",
		PurchaseToken: "opaque-purchase-token", PurchaseState: 0,
		PurchaseTime: purchasedAt.UnixMilli(), Quantity: 1,
	}
	purchaseJSON, _ := json.Marshal(purchase)
	signature := signFixture(t, privateKey, purchaseJSON)

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			writeFixtureJSON(writer, map[string]any{"access_token": "provider-access", "expires_in": 300})
		case "/order":
			if request.Header.Get("Authorization") != "Bearer provider-access" {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeFixtureJSON(writer, map[string]any{
				"responseCode": "0", "purchaseTokenData": string(purchaseJSON), "dataSignature": signature,
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	credentialJSON, _ := json.Marshal(credentialPayload{
		ClientID: "client", ClientSecret: "secret", PublicKey: publicKeyFixture(t, &privateKey.PublicKey),
		TokenURL: server.URL + "/token", OrderURL: server.URL + "/order", SubscriptionURL: server.URL + "/subscription",
	})
	credential, err := stores.NewCredential(CredentialKind, CredentialContentType, CredentialSchemaVersion, credentialJSON)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	adapter, err := New(fakeCredentialSource{credential: credential}, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.client = server.Client()

	evidenceJSON, _ := json.Marshal(clientEvidence{
		PurchaseData: string(purchaseJSON), Signature: signature, ProductKind: core.ProductKindNonConsumable,
	})
	evidence, _ := stores.NewEvidence(EvidenceContentType, evidenceJSON)
	application := core.Application{ID: "application-1", ProjectID: "project-1", Store: core.StoreApplication{
		Provider: core.ProviderHuaweiAppGallery, Environment: core.EnvironmentSandbox, ID: "provider-app-1",
	}}
	request := stores.VerificationRequest{
		Application: application, CustomerID: "customer-1",
		ClaimedProducts: []core.ProviderProductID{"lifetime"}, Evidence: evidence,
	}
	result, err := adapter.Verify(context.Background(), request)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := result.ValidateForVerification(request); err != nil {
		t.Fatalf("ValidateForVerification() error = %v", err)
	}
	if result.Observations[0].Access != core.AccessAllowed || result.Observations[0].State != core.LifecycleActive {
		t.Fatalf("observation = %#v, want active allowed", result.Observations[0])
	}
}

// TestNormalizeSubscriptionLifecycle covers renewal, cancellation, expiration, grace, and refund outcomes.
func TestNormalizeSubscriptionLifecycle(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	tests := []struct {
		name     string
		purchase purchaseData
		state    core.LifecycleState
		access   core.AccessStatus
	}{
		{name: "active renewal", purchase: purchaseData{PurchaseState: 0, SubIsValid: true,
			RenewStatus: 1, ExpirationDate: now.Add(time.Hour).UnixMilli()}, state: core.LifecycleActive, access: core.AccessAllowed},
		{name: "canceled at period end", purchase: purchaseData{PurchaseState: 0, SubIsValid: true,
			RenewStatus: 0, ExpirationDate: now.Add(time.Hour).UnixMilli()}, state: core.LifecycleCanceled, access: core.AccessAllowed},
		{name: "expired", purchase: purchaseData{PurchaseState: 0, SubIsValid: false,
			ExpirationDate: now.Add(-time.Hour).UnixMilli()}, state: core.LifecycleExpired, access: core.AccessDenied},
		{name: "grace", purchase: purchaseData{PurchaseState: 0, RetryFlag: 1,
			GraceExpirationTime: now.Add(time.Hour).UnixMilli(), ExpirationDate: now.Add(-time.Minute).UnixMilli()},
			state: core.LifecycleGracePeriod, access: core.AccessAllowed},
		{name: "refunded", purchase: purchaseData{PurchaseState: 2}, state: core.LifecycleRefunded, access: core.AccessDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, access, _ := normalizeState(core.ProductKindSubscription, tt.purchase, now)
			if state != tt.state || access != tt.access {
				t.Fatalf("normalizeState() = (%q, %q), want (%q, %q)", state, access, tt.state, tt.access)
			}
		})
	}
}

// TestObservationSnapshotIdentitySeparatesLifecycleChangesButNotReceiptTime verifies durable replay semantics.
func TestObservationSnapshotIdentitySeparatesLifecycleChangesButNotReceiptTime(t *testing.T) {
	application := core.Application{ID: "application-1", ProjectID: "project-1", Store: core.StoreApplication{
		Provider: core.ProviderHuaweiAppGallery, Environment: core.EnvironmentSandbox, ID: "provider-app-1",
	}}
	firstTime := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	purchase := purchaseData{
		ApplicationID: "provider-app-1", ProductID: "subscription", OrderID: "order-1",
		PurchaseToken: "token-1", PurchaseState: 0, PurchaseTime: firstTime.Add(-time.Hour).UnixMilli(),
		ExpirationDate: firstTime.Add(time.Hour).UnixMilli(), RenewStatus: 1, SubIsValid: true, Quantity: 1,
	}
	artifact, err := stores.NewEvidence("application/json", []byte(`{"authoritative":true}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	adapter := &Adapter{clock: func() time.Time { return firstTime }}
	first, err := adapter.result(application, core.ProductKindSubscription, purchase, artifact)
	if err != nil {
		t.Fatalf("first result() error = %v", err)
	}
	adapter.clock = func() time.Time { return firstTime.Add(time.Minute) }
	replayed, err := adapter.result(application, core.ProductKindSubscription, purchase, artifact)
	if err != nil {
		t.Fatalf("replayed result() error = %v", err)
	}
	if first.Observations[0].ID != replayed.Observations[0].ID {
		t.Fatalf("replayed observation IDs = (%q, %q), want stable identity",
			first.Observations[0].ID, replayed.Observations[0].ID)
	}
	if first.Observations[0].ObservedAt.Equal(replayed.Observations[0].ObservedAt) {
		t.Fatal("replayed observation times are equal, want distinct receipt times")
	}

	adapter.clock = func() time.Time { return firstTime.Add(2 * time.Hour) }
	expired, err := adapter.result(application, core.ProductKindSubscription, purchase, artifact)
	if err != nil {
		t.Fatalf("expired result() error = %v", err)
	}
	if expired.Observations[0].State != core.LifecycleExpired {
		t.Fatalf("expired lifecycle = %q, want %q", expired.Observations[0].State, core.LifecycleExpired)
	}
	if first.Observations[0].ID == expired.Observations[0].ID {
		t.Fatal("active and expired observations share one identity")
	}

	canceledPurchase := purchase
	canceledPurchase.RenewStatus = 0
	adapter.clock = func() time.Time { return firstTime }
	canceled, err := adapter.result(application, core.ProductKindSubscription, canceledPurchase, artifact)
	if err != nil {
		t.Fatalf("canceled result() error = %v", err)
	}
	if first.Observations[0].ID == canceled.Observations[0].ID {
		t.Fatal("renewing and canceled observations share one identity")
	}
}

// TestVerifySignatureRejectsTampering verifies that exact signed JSON bytes cannot be changed.
func TestVerifySignatureRejectsTampering(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	data := []byte(`{"productId":"pro"}`)
	signature := signFixture(t, privateKey, data)
	if err := verifySignature(&privateKey.PublicKey, string(data), signature); err != nil {
		t.Fatalf("verifySignature() error = %v", err)
	}
	if err := verifySignature(&privateKey.PublicKey, string(append(data, ' ')), signature); err == nil {
		t.Fatal("verifySignature() accepted tampered JSON")
	}
}

// Credential returns the configured opaque Huawei fixture.
func (source fakeCredentialSource) Credential(
	_ context.Context,
	_ core.Application,
	_ stores.CredentialKind,
) (stores.Credential, error) {
	return source.credential, nil
}

// signFixture creates one Huawei-compatible detached RSA-SHA256 signature.
func signFixture(t *testing.T, privateKey *rsa.PrivateKey, data []byte) string {
	t.Helper()
	digest := sha256.Sum256(data)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15() error = %v", err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}

// publicKeyFixture encodes one RSA public key as PEM PKIX.
func publicKeyFixture(t *testing.T, publicKey *rsa.PublicKey) string {
	t.Helper()
	encoded, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}))
}

// writeFixtureJSON writes one provider fixture response.
func writeFixtureJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
