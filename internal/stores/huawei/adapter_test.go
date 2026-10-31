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
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

// fakeCredentialSource returns one opaque Huawei credential fixture.
type fakeCredentialSource struct {
	credential stores.Credential
}

// rewriteTransport redirects one public test hostname to a local TLS fixture.
type rewriteTransport struct {
	destination *url.URL
	next        http.RoundTripper
}

// RoundTrip rewrites only the connection origin while preserving the provider request path.
func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	cloneURL := *request.URL
	cloneURL.Scheme = transport.destination.Scheme
	cloneURL.Host = transport.destination.Host
	clone.URL = &cloneURL
	return transport.next.RoundTrip(clone)
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
		PurchaseTime: purchasedAt.UnixMilli(), Quantity: 1, Currency: "USD", Price: 499,
	}
	purchaseJSON, _ := json.Marshal(purchase)
	signature := signFixture(t, privateKey, purchaseJSON)

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			writeFixtureJSON(writer, map[string]any{"access_token": "provider-access", "expires_in": 300})
		case "/order":
			if request.Header.Get("Authorization") != appAccessTokenAuthorization("provider-access") {
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
		TokenURL: "https://provider.example/token", OrderURL: "https://provider.example/order",
		SubscriptionURL: "https://provider.example/subscription",
	})
	credential, err := stores.NewCredential(CredentialKind, CredentialContentType, CredentialSchemaVersion, credentialJSON)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	adapter, err := New(fakeCredentialSource{credential: credential}, time.Second, false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.client = server.Client()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse() server URL error = %v", err)
	}
	adapter.client.Transport = rewriteTransport{destination: serverURL, next: adapter.client.Transport}

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
	if result.Observations[0].Price == nil || result.Observations[0].Price.Milliunits != 4990 ||
		result.Observations[0].Price.Currency != "USD" {
		t.Fatalf("observation price = %#v, want 4990 USD", result.Observations[0].Price)
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
		{name: "cancellation timestamp at period end", purchase: purchaseData{PurchaseState: 0, SubIsValid: true,
			RenewStatus: 1, CancellationTime: now.Add(-time.Minute).UnixMilli(), ExpirationDate: now.Add(time.Hour).UnixMilli()},
			state: core.LifecycleCanceled, access: core.AccessAllowed},
		{name: "expired", purchase: purchaseData{PurchaseState: 0, SubIsValid: false,
			ExpirationDate: now.Add(-time.Hour).UnixMilli()}, state: core.LifecycleExpired, access: core.AccessDenied},
		{name: "grace", purchase: purchaseData{PurchaseState: 0, RetryFlag: 1,
			GraceExpirationTime: now.Add(time.Hour).UnixMilli(), ExpirationDate: now.Add(-time.Minute).UnixMilli()},
			state: core.LifecycleGracePeriod, access: core.AccessAllowed},
		{name: "refunded and still valid", purchase: purchaseData{PurchaseState: 2, SubIsValid: true,
			RenewStatus: 1, ExpirationDate: now.Add(time.Hour).UnixMilli()}, state: core.LifecycleRefunded, access: core.AccessAllowed},
		{name: "refunded and expired", purchase: purchaseData{PurchaseState: 2, SubIsValid: false,
			ExpirationDate: now.Add(-time.Hour).UnixMilli()}, state: core.LifecycleRefunded, access: core.AccessDenied},
		{name: "revoked after refund", purchase: purchaseData{PurchaseState: 2,
			CancelTime: now.Add(-time.Minute).UnixMilli()}, state: core.LifecycleRevoked, access: core.AccessDenied},
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

// TestComparePurchasesRejectsPriceMismatch prevents client/server signed amount drift.
func TestComparePurchasesRejectsPriceMismatch(t *testing.T) {
	submitted := purchaseData{
		ApplicationID: "provider-app-1", ProductID: "lifetime", PurchaseToken: "token-1",
		Currency: "USD", Price: 499,
	}
	authoritative := submitted
	authoritative.Price = 599
	if err := comparePurchases(submitted, authoritative); err == nil {
		t.Fatal("comparePurchases() price mismatch error = nil, want rejection")
	}
	authoritative = submitted
	authoritative.Currency = "EUR"
	if err := comparePurchases(submitted, authoritative); err == nil {
		t.Fatal("comparePurchases() currency mismatch error = nil, want rejection")
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
		Currency: "USD", Price: 499,
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

	revokedPurchase := purchase
	revokedPurchase.PurchaseState = 2
	revokedPurchase.CancelTime = firstTime.Add(30 * time.Minute).UnixMilli()
	revoked, err := adapter.result(application, core.ProductKindSubscription, revokedPurchase, artifact)
	if err != nil {
		t.Fatalf("revoked result() error = %v", err)
	}
	if revoked.Observations[0].State != core.LifecycleRevoked {
		t.Fatalf("revoked lifecycle = %q, want %q", revoked.Observations[0].State, core.LifecycleRevoked)
	}
	if first.Observations[0].ID == revoked.Observations[0].ID {
		t.Fatal("active and revoked observations share one identity")
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
	if err := verifySignature(&privateKey.PublicKey, string(data), signature, ""); err != nil {
		t.Fatalf("verifySignature() error = %v", err)
	}
	if err := verifySignature(&privateKey.PublicKey, string(append(data, ' ')), signature, ""); err == nil {
		t.Fatal("verifySignature() accepted tampered JSON")
	}
	if err := verifySignature(&privateKey.PublicKey, string(data), signature, "SHA256WithRSA/PSS"); err == nil {
		t.Fatal("verifySignature() accepted a PKCS1 signature as PSS")
	}
}

// TestAppAccessTokenAuthorizationMatchesHuaweiExample locks Huawei's documented Basic APPAT header.
func TestAppAccessTokenAuthorizationMatchesHuaweiExample(t *testing.T) {
	got := appAccessTokenAuthorization("thisIsAppAtValue")
	if got != "Basic QVBQQVQ6dGhpc0lzQXBwQXRWYWx1ZQ==" {
		t.Fatalf("Authorization = %q", got)
	}
}

// TestHuaweiTextAcceptsStringAndInteger verifies AppGallery application IDs in both JSON forms.
func TestHuaweiTextAcceptsStringAndInteger(t *testing.T) {
	var numeric purchaseData
	if err := json.Unmarshal([]byte(`{"applicationId":100860000}`), &numeric); err != nil {
		t.Fatalf("Unmarshal() number error = %v", err)
	}
	if numeric.ApplicationID != "100860000" {
		t.Fatalf("numeric applicationId = %q", numeric.ApplicationID)
	}
	var text purchaseData
	if err := json.Unmarshal([]byte(`{"applicationId":"provider-app-1"}`), &text); err != nil {
		t.Fatalf("Unmarshal() string error = %v", err)
	}
	if text.ApplicationID != "provider-app-1" {
		t.Fatalf("string applicationId = %q", text.ApplicationID)
	}
	if err := json.Unmarshal([]byte(`{"applicationId":true}`), &numeric); err == nil {
		t.Fatal("Unmarshal() boolean applicationId error = nil")
	}
}

// TestAdapterVerifiesAuthoritativeSubscription exercises Huawei's subscription response shape.
func TestAdapterVerifiesAuthoritativeSubscription(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	purchaseJSON := mustPurchaseJSON(t, map[string]any{
		"applicationId":  100860000,
		"productId":      "pro_monthly",
		"subscriptionId": "1581789719266.D40972AC.3089",
		"orderId":        "order-sub-1",
		"purchaseToken":  "opaque-subscription-token",
		"purchaseState":  0,
		"purchaseTime":   now.Add(-time.Minute).UnixMilli(),
		"expirationDate": now.Add(time.Hour).UnixMilli(),
		"renewStatus":    1,
		"subIsvalid":     true,
		"currency":       "EUR",
		"price":          199,
		"quantity":       1,
	})
	signature := signFixture(t, privateKey, purchaseJSON)
	adapter := newHuaweiAdapter(t, privateKey, huaweiTokenAnd("/subscription", func(writer http.ResponseWriter, request *http.Request) {
		if !expectPurchaseQuery(writer, request, "opaque-subscription-token", "", "1581789719266.D40972AC.3089") {
			return
		}
		writeFixtureJSON(writer, map[string]any{
			"responseCode": "0", "inappPurchaseData": string(purchaseJSON),
			"dataSignature": signature, "signatureAlgorithm": "SHA256WithRSA",
		})
	}))
	application := huaweiVerificationApplication("100860000")
	request := stores.VerificationRequest{
		Application: application, CustomerID: "customer-1",
		ClaimedProducts: []core.ProviderProductID{"pro_monthly"},
		Evidence:        signedHuaweiEvidence(t, privateKey, purchaseJSON, core.ProductKindSubscription),
	}
	result, err := adapter.Verify(context.Background(), request)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := result.ValidateForVerification(request); err != nil {
		t.Fatalf("ValidateForVerification() error = %v", err)
	}
	observation := result.Observations[0]
	if observation.Access != core.AccessAllowed || observation.State != core.LifecycleActive ||
		observation.ProductKind != core.ProductKindSubscription {
		t.Fatalf("observation = %#v, want active allowed subscription", observation)
	}
	if observation.Price == nil || observation.Price.Milliunits != 1990 || observation.Price.Currency != "EUR" {
		t.Fatalf("observation price = %#v, want 1990 EUR", observation.Price)
	}

	token, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", "opaque-subscription-token")
	if err != nil {
		t.Fatalf("NewStoreReference() token error = %v", err)
	}
	subscription, err := core.NewStoreReference(core.ReferenceQuery, "subscription_id", "1581789719266.D40972AC.3089")
	if err != nil {
		t.Fatalf("NewStoreReference() subscription error = %v", err)
	}
	reconcile := stores.ReconciliationRequest{
		Application: application, CustomerID: "customer-1",
		ExpectedProducts:    []core.ProviderProductID{"pro_monthly"},
		ExpectedProductKind: core.ProductKindSubscription,
		QueryReferences:     []core.StoreReference{token, subscription},
	}
	reconciled, err := adapter.Reconcile(context.Background(), reconcile)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if err := reconciled.ValidateForReconciliation(reconcile); err != nil {
		t.Fatalf("ValidateForReconciliation() error = %v", err)
	}
	if reconciled.Observations[0].Access != core.AccessAllowed || reconciled.Observations[0].State != core.LifecycleActive {
		t.Fatalf("reconciled observation = %#v, want active allowed", reconciled.Observations[0])
	}
}

// TestAdapterRejectsInvalidServerSignature verifies a mocked Huawei signature cannot grant access.
func TestAdapterRejectsInvalidServerSignature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	purchaseJSON := mustPurchaseJSON(t, map[string]any{
		"applicationId": "provider-app-1", "productId": "pro_lifetime", "orderId": "order-1",
		"purchaseToken": "opaque-purchase-token", "purchaseState": 0,
		"purchaseTime": now.Add(-time.Minute).UnixMilli(), "currency": "USD", "price": 499, "quantity": 1,
	})
	badSignature := signFixture(t, privateKey, []byte(`{"productId":"other"}`))
	adapter := newHuaweiAdapter(t, privateKey, huaweiTokenAnd("/order", func(writer http.ResponseWriter, request *http.Request) {
		if !expectPurchaseQuery(writer, request, "opaque-purchase-token", "pro_lifetime", "") {
			return
		}
		writeFixtureJSON(writer, map[string]any{
			"responseCode": "0", "purchaseTokenData": string(purchaseJSON),
			"dataSignature": badSignature, "signatureAlgorithm": "SHA256WithRSA",
		})
	}))
	_, err = adapter.Verify(context.Background(), stores.VerificationRequest{
		Application: huaweiVerificationApplication("provider-app-1"), CustomerID: "customer-1",
		ClaimedProducts: []core.ProviderProductID{"pro_lifetime"},
		Evidence:        signedHuaweiEvidence(t, privateKey, purchaseJSON, core.ProductKindNonConsumable),
	})
	var failure *stores.Failure
	if !errors.As(err, &failure) || failure.Kind != stores.FailureInvalidEvidence {
		t.Fatalf("Verify() error = %#v, want invalid evidence", err)
	}
}

// TestAdapterMapsCanceledAndRefundedPurchases maps Huawei purchaseState onto shared access.
func TestAdapterMapsCanceledAndRefundedPurchases(t *testing.T) {
	tests := []struct {
		name          string
		purchaseState int
		state         core.LifecycleState
		access        core.AccessStatus
	}{
		{name: "canceled", purchaseState: 1, state: core.LifecycleRevoked, access: core.AccessDenied},
		{name: "refunded", purchaseState: 2, state: core.LifecycleRefunded, access: core.AccessDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatalf("GenerateKey() error = %v", err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			fields := map[string]any{
				"applicationId": "provider-app-1", "productId": "pro_lifetime", "orderId": "order-1",
				"purchaseToken": "opaque-purchase-token", "purchaseState": 0,
				"purchaseTime": now.Add(-time.Minute).UnixMilli(), "currency": "USD", "price": 499, "quantity": 1,
			}
			clientJSON := mustPurchaseJSON(t, fields)
			fields["purchaseState"] = tt.purchaseState
			serverJSON := mustPurchaseJSON(t, fields)
			signature := signFixture(t, privateKey, serverJSON)
			adapter := newHuaweiAdapter(t, privateKey, huaweiTokenAnd("/order", func(writer http.ResponseWriter, request *http.Request) {
				if !expectPurchaseQuery(writer, request, "opaque-purchase-token", "pro_lifetime", "") {
					return
				}
				writeFixtureJSON(writer, map[string]any{
					"responseCode": "0", "purchaseTokenData": string(serverJSON), "dataSignature": signature,
				})
			}))
			request := stores.VerificationRequest{
				Application: huaweiVerificationApplication("provider-app-1"), CustomerID: "customer-1",
				ClaimedProducts: []core.ProviderProductID{"pro_lifetime"},
				Evidence:        signedHuaweiEvidence(t, privateKey, clientJSON, core.ProductKindNonConsumable),
			}
			result, err := adapter.Verify(context.Background(), request)
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if err := result.ValidateForVerification(request); err != nil {
				t.Fatalf("ValidateForVerification() error = %v", err)
			}
			observation := result.Observations[0]
			if observation.State != tt.state || observation.Access != tt.access {
				t.Fatalf("observation = (%q, %q), want (%q, %q)", observation.State, observation.Access, tt.state, tt.access)
			}
		})
	}
}

// TestAdapterMapsExpiredSubscription maps a signed subscription past its expiration to denied access.
func TestAdapterMapsExpiredSubscription(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	fields := map[string]any{
		"applicationId": 100860000, "productId": "pro_monthly",
		"subscriptionId": "1581789719266.D40972AC.3089", "orderId": "order-sub-1",
		"purchaseToken": "opaque-subscription-token", "purchaseState": 0,
		"purchaseTime":   now.Add(-2 * time.Hour).UnixMilli(),
		"expirationDate": now.Add(time.Hour).UnixMilli(), "renewStatus": 1, "subIsvalid": true,
		"currency": "EUR", "price": 199, "quantity": 1,
	}
	clientJSON := mustPurchaseJSON(t, fields)
	fields["expirationDate"] = now.Add(-time.Hour).UnixMilli()
	fields["renewStatus"] = 0
	fields["subIsvalid"] = false
	serverJSON := mustPurchaseJSON(t, fields)
	serverSignature := signPSSFixture(t, privateKey, serverJSON)
	adapter := newHuaweiAdapter(t, privateKey, huaweiTokenAnd("/subscription", func(writer http.ResponseWriter, request *http.Request) {
		if !expectPurchaseQuery(writer, request, "opaque-subscription-token", "", "1581789719266.D40972AC.3089") {
			return
		}
		writeFixtureJSON(writer, map[string]any{
			"responseCode": "0", "inappPurchaseData": string(serverJSON),
			"dataSignature": serverSignature, "signatureAlgorithm": "SHA256WithRSA/PSS",
		})
	}))
	request := stores.VerificationRequest{
		Application: huaweiVerificationApplication("100860000"), CustomerID: "customer-1",
		ClaimedProducts: []core.ProviderProductID{"pro_monthly"},
		Evidence:        signedHuaweiEvidence(t, privateKey, clientJSON, core.ProductKindSubscription),
	}
	result, err := adapter.Verify(context.Background(), request)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := result.ValidateForVerification(request); err != nil {
		t.Fatalf("ValidateForVerification() error = %v", err)
	}
	observation := result.Observations[0]
	if observation.State != core.LifecycleExpired || observation.Access != core.AccessDenied {
		t.Fatalf("observation = (%q, %q), want expired denied", observation.State, observation.Access)
	}
}

// TestAdapterRejectsSubscriptionWithoutSubscriptionID does not call Huawei when the subscription id is missing.
func TestAdapterRejectsSubscriptionWithoutSubscriptionID(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	called := make(chan struct{}, 1)
	adapter := newHuaweiAdapter(t, privateKey, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		select {
		case called <- struct{}{}:
		default:
		}
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	now := time.Now().UTC().Truncate(time.Millisecond)
	purchaseJSON := mustPurchaseJSON(t, map[string]any{
		"applicationId": "provider-app-1", "productId": "pro_monthly", "orderId": "order-sub-1",
		"purchaseToken": "opaque-subscription-token", "purchaseState": 0,
		"purchaseTime": now.Add(-time.Minute).UnixMilli(), "expirationDate": now.Add(time.Hour).UnixMilli(),
		"renewStatus": 1, "subIsvalid": true, "currency": "EUR", "price": 199, "quantity": 1,
	})
	_, err = adapter.Verify(context.Background(), stores.VerificationRequest{
		Application: huaweiVerificationApplication("provider-app-1"), CustomerID: "customer-1",
		ClaimedProducts: []core.ProviderProductID{"pro_monthly"},
		Evidence:        signedHuaweiEvidence(t, privateKey, purchaseJSON, core.ProductKindSubscription),
	})
	var failure *stores.Failure
	if !errors.As(err, &failure) || failure.Kind != stores.FailureInvalidEvidence {
		t.Fatalf("Verify() error = %#v, want invalid evidence", err)
	}
	select {
	case <-called:
		t.Fatal("provider was called without subscriptionId")
	default:
	}
}

// TestValidateNotificationCanonicalizesEquivalentPayloads verifies replay identity ignores harmless JSON formatting.
func TestValidateNotificationCanonicalizesEquivalentPayloads(t *testing.T) {
	adapter := &Adapter{}
	application := core.Application{ID: "application-1", ProjectID: "project-1", Store: core.StoreApplication{
		Provider: core.ProviderHuaweiAppGallery, Environment: core.EnvironmentSandbox, ID: "provider-app-1",
	}}
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	firstPayload, err := json.Marshal(map[string]any{
		"version": "v2", "eventType": "ORDER", "notifyTime": now.UnixMilli(), "applicationId": "provider-app-1",
		"orderNotification": map[string]any{
			"version": "v2", "notificationType": 2, "purchaseToken": "purchase-token-1", "productId": "lifetime",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() first notification error = %v", err)
	}
	secondPayload, err := json.Marshal(map[string]any{
		"applicationId": "provider-app-1", "notifyTime": now.UnixMilli(), "eventType": "ORDER", "version": "v2",
		"orderNotification": map[string]any{
			"productId": "lifetime", "purchaseToken": "purchase-token-1", "notificationType": 2, "version": "v2",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() second notification error = %v", err)
	}
	first, err := adapter.ValidateNotification(context.Background(), application, firstPayload)
	if err != nil {
		t.Fatalf("ValidateNotification() first error = %v", err)
	}
	second, err := adapter.ValidateNotification(context.Background(), application, secondPayload)
	if err != nil {
		t.Fatalf("ValidateNotification() second error = %v", err)
	}
	firstCanonical, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("Marshal() first canonical notification error = %v", err)
	}
	secondCanonical, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("Marshal() second canonical notification error = %v", err)
	}
	if string(firstCanonical) != string(secondCanonical) {
		t.Fatalf("canonical notifications differ:\nfirst:  %s\nsecond: %s", firstCanonical, secondCanonical)
	}

	wrongScope, err := json.Marshal(map[string]any{
		"version": "v2", "eventType": "ORDER", "notifyTime": now.UnixMilli(), "applicationId": "wrong-app",
		"orderNotification": map[string]any{
			"version": "v2", "notificationType": 2, "purchaseToken": "purchase-token-1", "productId": "lifetime",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() wrong-scope notification error = %v", err)
	}
	if _, err := adapter.ValidateNotification(context.Background(), application, wrongScope); err == nil {
		t.Fatal("ValidateNotification() wrong scope error = nil, want rejection")
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

// mustPurchaseJSON serializes one purchase object with stable JSON object key order.
func mustPurchaseJSON(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("Marshal() purchase error = %v", err)
	}
	return encoded
}

// newHuaweiAdapter points one adapter at a TLS stand-in for Huawei's token and purchase APIs.
func newHuaweiAdapter(t *testing.T, privateKey *rsa.PrivateKey, handler http.Handler) *Adapter {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
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
	adapter.client = server.Client()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse() server URL error = %v", err)
	}
	adapter.client.Transport = rewriteTransport{destination: serverURL, next: adapter.client.Transport}
	return adapter
}

// huaweiTokenAnd serves the OAuth token fixture and one authorized purchase endpoint.
func huaweiTokenAnd(path string, handle http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			writeFixtureJSON(writer, map[string]any{"access_token": "provider-access", "expires_in": 300})
		case path:
			if request.Header.Get("Authorization") != appAccessTokenAuthorization("provider-access") {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			handle(writer, request)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}
}

// expectPurchaseQuery rejects a purchase-token body that does not match the expected identifiers.
func expectPurchaseQuery(writer http.ResponseWriter, request *http.Request, token, productID, subscriptionID string) bool {
	var body map[string]string
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
		body["purchaseToken"] != token || body["productId"] != productID || body["subscriptionId"] != subscriptionID {
		writer.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

// signedHuaweiEvidence builds one device envelope signed with SHA256WithRSA.
func signedHuaweiEvidence(t *testing.T, privateKey *rsa.PrivateKey, purchaseJSON []byte, kind core.ProductKind) stores.Evidence {
	t.Helper()
	evidenceJSON, err := json.Marshal(clientEvidence{
		PurchaseData: string(purchaseJSON), Signature: signFixture(t, privateKey, purchaseJSON), ProductKind: kind,
	})
	if err != nil {
		t.Fatalf("Marshal() evidence error = %v", err)
	}
	evidence, err := stores.NewEvidence(EvidenceContentType, evidenceJSON)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	return evidence
}

// huaweiVerificationApplication returns one Huawei application scope for adapter tests.
func huaweiVerificationApplication(providerApplicationID string) core.Application {
	return core.Application{ID: "application-1", ProjectID: "project-1", Store: core.StoreApplication{
		Provider: core.ProviderHuaweiAppGallery, Environment: core.EnvironmentSandbox,
		ID: core.ProviderApplicationID(providerApplicationID),
	}}
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

// signPSSFixture creates one SHA256WithRSA/PSS signature using a hash-length salt.
func signPSSFixture(t *testing.T, privateKey *rsa.PrivateKey, data []byte) string {
	t.Helper()
	digest := sha256.Sum256(data)
	signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, digest[:], &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       crypto.SHA256,
	})
	if err != nil {
		t.Fatalf("SignPSS() error = %v", err)
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
