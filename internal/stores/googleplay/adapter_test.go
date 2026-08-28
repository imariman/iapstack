package googleplay

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
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// fixturePackageName identifies the Android application used by Google Play adapter tests.
	fixturePackageName = "com.example.iapstack"
	// fixtureProductID identifies the Google Play catalog item used by adapter tests.
	fixtureProductID = "iapstack.pro"
	// fixturePurchaseToken identifies the opaque Google Play query token used by adapter tests.
	fixturePurchaseToken = "purchase-token-0123456789"
	// fixtureAccountID identifies the obfuscated application customer used by adapter tests.
	fixtureAccountID = "account_7d9c7b1a3e7046c9"
	// fixtureRTDNSubscription identifies the Pub/Sub subscription bound to notification fixtures.
	fixtureRTDNSubscription = "projects/example-project/subscriptions/iapstack-google-play"
	// fixtureRTDNAudience identifies the configured OIDC audience for Pub/Sub push fixtures.
	fixtureRTDNAudience = "https://iapstack.example/v1/providers/google-play/projects/project-1/applications/application-1/notifications"
	// fixtureRTDNServiceAccount identifies the authenticated Pub/Sub push principal.
	fixtureRTDNServiceAccount = "iapstack-push@example-project.iam.gserviceaccount.com"
)

// fakeCredentialSource returns one protected Google Play credential fixture.
type fakeCredentialSource struct {
	credential stores.Credential
}

// roundTripFunc adapts a function into an HTTP transport without opening a listener.
type roundTripFunc func(*http.Request) (*http.Response, error)

// adapterFixture contains one configured adapter and its service-account signing key.
type adapterFixture struct {
	adapter    *Adapter
	privateKey *rsa.PrivateKey
	now        time.Time
}

// TestAdapterVerifiesAuthoritativeNonConsumable verifies the ProductPurchaseV2 flow and OAuth assertion.
func TestAdapterVerifiesAuthoritativeNonConsumable(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentProduction)
	purchase := productPurchase{
		Kind: "androidpublisher#productPurchaseV2",
		ProductLineItems: []productLineItem{{
			ProductID: fixtureProductID,
			ProductOfferDetails: productOfferDetails{
				Quantity: 1, RefundableQuantity: 1,
				ConsumptionState: "CONSUMPTION_STATE_YET_TO_BE_CONSUMED", PurchaseOptionID: "buy",
			},
		}},
		PurchaseStateContext:        purchaseStateContext{PurchaseState: productStatePurchased},
		OrderID:                     "GPA.1234-5678-9012-34567",
		ObfuscatedExternalAccountID: fixtureAccountID,
		PurchaseCompletionTime:      fixture.now.Add(-time.Hour),
		AcknowledgementState:        acknowledgementStatePending,
	}
	fixture.adapter.client.Transport = providerTransport(t, fixture, purchase)
	result, err := fixture.adapter.Verify(context.Background(), verificationRequest(t, core.EnvironmentProduction, core.ProductKindNonConsumable))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("Verify() observations = %#v", result.Observations)
	}
	observation := result.Observations[0]
	if observation.ProductID != fixtureProductID || observation.ProductKind != core.ProductKindNonConsumable ||
		observation.State != core.LifecycleActive || observation.Access != core.AccessAllowed || observation.Quantity != 1 {
		t.Fatalf("Verify() observation = %#v", observation)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Kind != "google_play_server_response" {
		t.Fatalf("Verify() artifacts = %#v", result.Artifacts)
	}
	if len(result.PostCommitActions) != 1 || result.PostCommitActions[0].Kind != acknowledgeActionKind ||
		result.PostCommitActions[0].ProductID != fixtureProductID ||
		result.PostCommitActions[0].ProductKind != core.ProductKindNonConsumable {
		t.Fatalf("Verify() post-commit actions = %#v", result.PostCommitActions)
	}
}

// TestAdapterPostsProductAndSubscriptionAcknowledgements verifies both documented Android Publisher paths and request shape.
func TestAdapterPostsProductAndSubscriptionAcknowledgements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		kind         core.ProductKind
		pathFragment string
	}{
		{name: "non-consumable", kind: core.ProductKindNonConsumable, pathFragment: "/purchases/products/"},
		{name: "subscription", kind: core.ProductKindSubscription, pathFragment: "/purchases/subscriptions/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newAdapterFixture(t, core.EnvironmentProduction)
			acknowledgementSeen := false
			fixture.adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == fixture.adapter.tokenURL {
					verifyAssertionRequest(t, fixture, request)
					return jsonResponse(t, http.StatusOK, tokenResponse{
						AccessToken: "provider-access-token", TokenType: "Bearer", ExpiresIn: 3600,
					}, nil), nil
				}
				acknowledgementSeen = true
				if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer provider-access-token" ||
					request.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("acknowledgement request = %s, authorization = %q, content type = %q",
						request.Method, request.Header.Get("Authorization"), request.Header.Get("Content-Type"))
				}
				wantSuffix := test.pathFragment + fixtureProductID + "/tokens/" + fixturePurchaseToken + ":acknowledge"
				if !strings.Contains(request.URL.Path, "/applications/"+fixturePackageName) ||
					!strings.HasSuffix(request.URL.Path, wantSuffix) {
					t.Fatalf("acknowledgement path = %q, want suffix %q", request.URL.Path, wantSuffix)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatalf("ReadAll() acknowledgement body error = %v", err)
				}
				if string(body) != "{}" {
					t.Fatalf("acknowledgement body = %q, want {}", body)
				}
				return jsonResponse(t, http.StatusOK, map[string]any{}, nil), nil
			})

			if err := fixture.adapter.PostCommit(context.Background(), postCommitRequest(t, test.kind)); err != nil {
				t.Fatalf("PostCommit() error = %v", err)
			}
			if !acknowledgementSeen {
				t.Fatal("PostCommit() did not send an acknowledgement")
			}
		})
	}
}

// TestAdapterRetriesConcurrentAcknowledgement verifies Google-documented concurrent updates remain retryable.
func TestAdapterRetriesConcurrentAcknowledgement(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentProduction)
	fixture.adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == fixture.adapter.tokenURL {
			return jsonResponse(t, http.StatusOK, tokenResponse{
				AccessToken: "provider-access-token", TokenType: "Bearer", ExpiresIn: 3600,
			}, nil), nil
		}
		return jsonResponse(t, http.StatusConflict, map[string]any{"error": "concurrent update"}, nil), nil
	})

	err := fixture.adapter.PostCommit(
		context.Background(),
		postCommitRequest(t, core.ProductKindNonConsumable),
	)
	var failure *stores.Failure
	if !errors.As(err, &failure) || failure.Kind != stores.FailureTemporary || !failure.Retryable() {
		t.Fatalf("PostCommit() conflict error = %#v", err)
	}
}

// TestAcknowledgementActionsExcludePendingAndCompletedWork verifies only completed unacknowledged purchases create effects.
func TestAcknowledgementActionsExcludePendingAndCompletedWork(t *testing.T) {
	t.Parallel()

	reference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", fixturePurchaseToken)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	observation := core.PurchaseObservation{
		ProductID: fixtureProductID, ProductKind: core.ProductKindNonConsumable,
		References: []core.StoreReference{reference},
	}
	for _, test := range []struct {
		name                 string
		state                string
		acknowledgementState string
	}{
		{name: "pending purchase", state: productStatePending, acknowledgementState: acknowledgementStatePending},
		{name: "already acknowledged", state: productStatePurchased, acknowledgementState: acknowledgementStateAcknowledged},
	} {
		actions, err := acknowledgementActions(observation, test.state, test.acknowledgementState)
		if err != nil {
			t.Fatalf("%s acknowledgementActions() error = %v", test.name, err)
		}
		if len(actions) != 0 {
			t.Fatalf("%s acknowledgementActions() = %#v, want none", test.name, actions)
		}
	}
}

// TestAdapterNormalizesSandboxGraceSubscription verifies test scope, grace access, and renewal state.
func TestAdapterNormalizesSandboxGraceSubscription(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentSandbox)
	purchase := subscriptionPurchase{
		Kind: "androidpublisher#subscriptionPurchaseV2",
		LineItems: []subscriptionLineItem{{
			ProductID: fixtureProductID, ExpiryTime: fixture.now.Add(24 * time.Hour),
			LatestSuccessfulOrderID: "GPA.2234-5678-9012-34567",
			AutoRenewingPlan:        &autoRenewingPlan{AutoRenewEnabled: true},
		}},
		StartTime: fixture.now.Add(-24 * time.Hour), SubscriptionState: subscriptionStateGrace,
		LatestOrderID: "GPA.2234-5678-9012-34567", TestPurchase: &struct{}{},
		AcknowledgementState: acknowledgementStatePending,
		ExternalAccountIdentifiers: externalAccountIdentifiers{
			ObfuscatedExternalAccountID: fixtureAccountID,
		},
	}
	fixture.adapter.client.Transport = providerTransport(t, fixture, purchase)
	result, err := fixture.adapter.Verify(context.Background(), verificationRequest(t, core.EnvironmentSandbox, core.ProductKindSubscription))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	observation := result.Observations[0]
	if observation.State != core.LifecycleGracePeriod || observation.Access != core.AccessAllowed ||
		observation.AccessReason != core.AccessReasonGracePeriod || observation.Renewal.Mode != core.RenewalAuto ||
		observation.Renewal.Status != core.RenewalEnabled {
		t.Fatalf("Verify() observation = %#v", observation)
	}
	if len(result.PostCommitActions) != 1 || result.PostCommitActions[0].ProductKind != core.ProductKindSubscription {
		t.Fatalf("Verify() post-commit actions = %#v", result.PostCommitActions)
	}
}

// TestAdapterReconcilesAuthoritativeAccountHold verifies missed RTDN delivery converges through Android Publisher.
func TestAdapterReconcilesAuthoritativeAccountHold(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentSandbox)
	purchase := subscriptionPurchase{
		Kind: "androidpublisher#subscriptionPurchaseV2",
		LineItems: []subscriptionLineItem{{
			ProductID: fixtureProductID, ExpiryTime: fixture.now.Add(time.Hour),
			LatestSuccessfulOrderID: "GPA.4234-5678-9012-34567",
			AutoRenewingPlan:        &autoRenewingPlan{AutoRenewEnabled: true},
		}},
		StartTime: fixture.now.Add(-24 * time.Hour), SubscriptionState: subscriptionStateOnHold,
		LatestOrderID: "GPA.4234-5678-9012-34567", TestPurchase: &struct{}{},
		AcknowledgementState: acknowledgementStateAcknowledged,
		ExternalAccountIdentifiers: externalAccountIdentifiers{
			ObfuscatedExternalAccountID: fixtureAccountID,
		},
	}
	fixture.adapter.client.Transport = providerTransport(t, fixture, purchase)
	verification := verificationRequest(t, core.EnvironmentSandbox, core.ProductKindSubscription)
	queryReference, err := core.NewStoreReference(
		core.ReferenceQuery,
		"purchase_token",
		fixturePurchaseToken,
	)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	result, err := fixture.adapter.Reconcile(context.Background(), stores.ReconciliationRequest{
		Application: verification.Application, CustomerID: verification.CustomerID,
		ExpectedProducts:         []core.ProviderProductID{fixtureProductID},
		ExpectedCustomerBindings: verification.ExpectedCustomerBindings,
		QueryReferences:          []core.StoreReference{queryReference},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	observation := result.Observations[0]
	if observation.State != core.LifecycleOnHold || observation.Access != core.AccessDenied ||
		observation.AccessReason != core.AccessReasonBillingIssue || len(result.PostCommitActions) != 0 {
		t.Fatalf("Reconcile() result = %#v", result)
	}
}

// TestAdapterRejectsInvalidPurchaseToken verifies an unknown authoritative token cannot create provider output.
func TestAdapterRejectsInvalidPurchaseToken(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentSandbox)
	fixture.adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == fixture.adapter.tokenURL {
			return jsonResponse(t, http.StatusOK, tokenResponse{
				AccessToken: "provider-access-token", TokenType: "Bearer", ExpiresIn: 3600,
			}, nil), nil
		}
		return jsonResponse(t, http.StatusNotFound, map[string]any{"error": "not found"}, nil), nil
	})
	_, err := fixture.adapter.Verify(
		context.Background(),
		verificationRequest(t, core.EnvironmentSandbox, core.ProductKindSubscription),
	)
	if !isFailureKind(err, stores.FailureNotFound) {
		t.Fatalf("Verify() error = %v, want provider not found", err)
	}
}

// TestAdapterRejectsCustomerAndEnvironmentMismatch verifies cross-customer and live/test scope isolation.
func TestAdapterRejectsCustomerAndEnvironmentMismatch(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentProduction)
	purchase := productPurchase{
		ProductLineItems:            []productLineItem{{ProductID: fixtureProductID}},
		PurchaseStateContext:        purchaseStateContext{PurchaseState: productStatePurchased},
		OrderID:                     "GPA.3234-5678-9012-34567",
		ObfuscatedExternalAccountID: "different-account",
		PurchaseCompletionTime:      fixture.now.Add(-time.Hour),
	}
	fixture.adapter.client.Transport = providerTransport(t, fixture, purchase)
	if _, err := fixture.adapter.Verify(
		context.Background(),
		verificationRequest(t, core.EnvironmentProduction, core.ProductKindNonConsumable),
	); !isFailureKind(err, stores.FailureInvalidEvidence) {
		t.Fatalf("Verify() customer mismatch error = %v", err)
	}

	purchase.ObfuscatedExternalAccountID = fixtureAccountID
	purchase.TestPurchaseContext = &struct{}{}
	fixture.adapter.client.Transport = providerTransport(t, fixture, purchase)
	if _, err := fixture.adapter.Verify(
		context.Background(),
		verificationRequest(t, core.EnvironmentProduction, core.ProductKindNonConsumable),
	); !isFailureKind(err, stores.FailureInvalidEvidence) {
		t.Fatalf("Verify() environment mismatch error = %v", err)
	}
}

// TestAdapterClassifiesRateLimitAndTimeout verifies retryable provider failures stay bounded.
func TestAdapterClassifiesRateLimitAndTimeout(t *testing.T) {
	t.Parallel()

	fixture := newAdapterFixture(t, core.EnvironmentProduction)
	fixture.adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == fixture.adapter.tokenURL {
			verifyAssertionRequest(t, fixture, request)
			return jsonResponse(t, http.StatusOK, tokenResponse{
				AccessToken: "provider-access-token", TokenType: "Bearer", ExpiresIn: 3600,
			}, nil), nil
		}
		return jsonResponse(t, http.StatusTooManyRequests, map[string]any{"error": "rate limited"}, http.Header{
			"Retry-After": []string{"19"},
		}), nil
	})
	_, err := fixture.adapter.Verify(context.Background(), verificationRequest(t, core.EnvironmentProduction, core.ProductKindNonConsumable))
	var failure *stores.Failure
	if !errors.As(err, &failure) || failure.Kind != stores.FailureRateLimited || failure.RetryAfter != 19*time.Second {
		t.Fatalf("Verify() rate-limit error = %#v", err)
	}

	fixture.adapter.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	_, err = fixture.adapter.Verify(context.Background(), verificationRequest(t, core.EnvironmentProduction, core.ProductKindNonConsumable))
	if !errors.As(err, &failure) || failure.Kind != stores.FailureTemporary || !failure.Retryable() {
		t.Fatalf("Verify() timeout error = %#v", err)
	}
}

// TestSubscriptionStateNormalization verifies every documented Google Play lifecycle mapping.
func TestSubscriptionStateNormalization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		providerState string
		state         core.LifecycleState
		access        core.AccessStatus
		reason        core.AccessReason
	}{
		{providerState: subscriptionStatePending, state: core.LifecyclePending, access: core.AccessUnresolved, reason: core.AccessReasonPendingPayment},
		{providerState: subscriptionStateActive, state: core.LifecycleActive, access: core.AccessAllowed, reason: core.AccessReasonPurchaseValid},
		{providerState: subscriptionStatePaused, state: core.LifecyclePaused, access: core.AccessDenied, reason: core.AccessReasonPaused},
		{providerState: subscriptionStateGrace, state: core.LifecycleGracePeriod, access: core.AccessAllowed, reason: core.AccessReasonGracePeriod},
		{providerState: subscriptionStateOnHold, state: core.LifecycleOnHold, access: core.AccessDenied, reason: core.AccessReasonBillingIssue},
		{providerState: subscriptionStateCanceled, state: core.LifecycleCanceled, access: core.AccessAllowed, reason: core.AccessReasonCanceledAtPeriodEnd},
		{providerState: subscriptionStateExpired, state: core.LifecycleExpired, access: core.AccessDenied, reason: core.AccessReasonExpired},
		{providerState: subscriptionStatePendingCanceled, state: core.LifecycleRevoked, access: core.AccessDenied, reason: core.AccessReasonRevoked},
	}
	for _, test := range tests {
		state, access, reason := normalizeSubscriptionState(test.providerState)
		if state != test.state || access != test.access || reason != test.reason {
			t.Errorf("normalizeSubscriptionState(%q) = (%q, %q, %q), want (%q, %q, %q)",
				test.providerState, state, access, reason, test.state, test.access, test.reason)
		}
	}
}

// TestProductStateNormalization verifies pending and refunded non-consumables never grant access.
func TestProductStateNormalization(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		providerState string
		state         core.LifecycleState
		access        core.AccessStatus
		reason        core.AccessReason
	}{
		{providerState: productStatePurchased, state: core.LifecycleActive, access: core.AccessAllowed, reason: core.AccessReasonPurchaseValid},
		{providerState: productStatePending, state: core.LifecyclePending, access: core.AccessUnresolved, reason: core.AccessReasonPendingPayment},
		{providerState: productStateCancelled, state: core.LifecycleRefunded, access: core.AccessDenied, reason: core.AccessReasonRefunded},
	} {
		state, access, reason := normalizeProductState(test.providerState)
		if state != test.state || access != test.access || reason != test.reason {
			t.Errorf("normalizeProductState(%q) = (%q, %q, %q), want (%q, %q, %q)",
				test.providerState, state, access, reason, test.state, test.access, test.reason)
		}
	}
}

// Credential returns the configured opaque Google Play fixture.
func (source fakeCredentialSource) Credential(
	_ context.Context,
	_ core.Application,
	_ stores.CredentialKind,
) (stores.Credential, error) {
	return source.credential, nil
}

// RoundTrip executes the fixture transport function.
func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// newAdapterFixture creates one service-account credential and deterministic adapter clock.
func newAdapterFixture(t *testing.T, environment core.Environment) adapterFixture {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	payload, err := json.Marshal(credentialPayload{
		ClientEmail: "iapstack@example-project.iam.gserviceaccount.com", PrivateKeyID: "0123456789abcdef",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})),
		RTDN: &rtdnConfiguration{
			Subscription: fixtureRTDNSubscription, Audience: fixtureRTDNAudience,
			PushServiceAccountEmail: fixtureRTDNServiceAccount,
		},
	})
	if err != nil {
		t.Fatalf("Marshal() credential error = %v", err)
	}
	credential, err := stores.NewCredential(CredentialKind, CredentialContentType, CredentialSchemaVersion, payload)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	adapter, err := New(fakeCredentialSource{credential: credential}, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	now := time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC)
	adapter.clock = func() time.Time { return now }
	return adapterFixture{adapter: adapter, privateKey: privateKey, now: now}
}

// providerTransport verifies OAuth and Publisher calls and returns one authoritative fixture.
func providerTransport(t *testing.T, fixture adapterFixture, purchase any) http.RoundTripper {
	t.Helper()
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == fixture.adapter.tokenURL {
			verifyAssertionRequest(t, fixture, request)
			return jsonResponse(t, http.StatusOK, tokenResponse{
				AccessToken: "provider-access-token", TokenType: "Bearer", ExpiresIn: 3600,
			}, nil), nil
		}
		if request.Method != http.MethodGet || request.Header.Get("Authorization") != "Bearer provider-access-token" {
			t.Fatalf("publisher request = %s %s, authorization = %q", request.Method, request.URL, request.Header.Get("Authorization"))
		}
		if !strings.Contains(request.URL.Path, "/applications/"+fixturePackageName+"/purchases/") ||
			!strings.HasSuffix(request.URL.Path, "/tokens/"+fixturePurchaseToken) {
			t.Fatalf("publisher path = %q", request.URL.Path)
		}
		return jsonResponse(t, http.StatusOK, purchase, nil), nil
	})
}

// verifyAssertionRequest verifies the form, RS256 signature, and required Google OAuth claims.
func verifyAssertionRequest(t *testing.T, fixture adapterFixture, request *http.Request) {
	t.Helper()
	if request.Method != http.MethodPost {
		t.Fatalf("token method = %s", request.Method)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("ReadAll() token request error = %v", err)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("ParseQuery() token form error = %v", err)
	}
	if form.Get("grant_type") != serviceAccountGrantType {
		t.Fatalf("token grant type = %q", form.Get("grant_type"))
	}
	parts := strings.Split(form.Get("assertion"), ".")
	if len(parts) != 3 {
		t.Fatalf("assertion parts = %d", len(parts))
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("DecodeString() assertion signature error = %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&fixture.privateKey.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("VerifyPKCS1v15() assertion error = %v", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("DecodeString() assertion payload error = %v", err)
	}
	var claims struct {
		Issuer   string `json:"iss"`
		Scope    string `json:"scope"`
		Audience string `json:"aud"`
		IssuedAt int64  `json:"iat"`
		Expires  int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("Unmarshal() assertion claims error = %v", err)
	}
	if claims.Issuer == "" || claims.Scope != androidPublisherScope || claims.Audience != fixture.adapter.tokenURL ||
		claims.IssuedAt != fixture.now.Unix() || claims.Expires != fixture.now.Add(assertionLifetime).Unix() {
		t.Fatalf("assertion claims = %#v", claims)
	}
}

// verificationRequest builds one provider-scoped Google Play request fixture.
func verificationRequest(
	t *testing.T,
	environment core.Environment,
	kind core.ProductKind,
) stores.VerificationRequest {
	t.Helper()
	evidenceBytes, err := json.Marshal(clientEvidence{PurchaseToken: fixturePurchaseToken, ProductKind: kind})
	if err != nil {
		t.Fatalf("Marshal() evidence error = %v", err)
	}
	evidence, err := stores.NewEvidence(EvidenceContentType, evidenceBytes)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	binding, err := core.NewStoreReference(core.ReferenceCustomerBinding, "obfuscated_external_account_id", fixtureAccountID)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	return stores.VerificationRequest{
		Application: core.Application{
			ID: "application-1", ProjectID: "project-1",
			Store: core.StoreApplication{Provider: core.ProviderGooglePlay, Environment: environment, ID: fixturePackageName},
		},
		CustomerID: "customer-1", ClaimedProducts: []core.ProviderProductID{fixtureProductID},
		ExpectedCustomerBindings: []core.StoreReference{binding}, Evidence: evidence,
	}
}

// postCommitRequest builds one validated Google Play acknowledgement request fixture.
func postCommitRequest(t *testing.T, kind core.ProductKind) stores.PostCommitRequest {
	t.Helper()
	reference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", fixturePurchaseToken)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	return stores.PostCommitRequest{
		Application: verificationRequest(t, core.EnvironmentProduction, kind).Application,
		Actions: []stores.PostCommitAction{{
			Kind: acknowledgeActionKind, ProductID: fixtureProductID, ProductKind: kind,
			QueryReferences: []core.StoreReference{reference},
		}},
	}
}

// jsonResponse builds one in-memory HTTP response.
func jsonResponse(t *testing.T, status int, value any, header http.Header) *http.Response {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() response error = %v", err)
	}
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(string(payload)))}
}

// isFailureKind reports whether an error contains the expected redacted provider category.
func isFailureKind(err error, kind stores.FailureKind) bool {
	var failure *stores.Failure
	return errors.As(err, &failure) && failure.Kind == kind
}
