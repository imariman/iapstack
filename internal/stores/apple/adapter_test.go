package apple

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// fixtureBundleID is the application scope used by Apple adapter tests.
	fixtureBundleID = "com.example.iapstack"
	// fixtureProductID is the provider product identity used by Apple adapter tests.
	fixtureProductID = "iapstack.pro"
	// fixtureAccountToken is the StoreKit customer binding used by Apple adapter tests.
	fixtureAccountToken = "4f753b4a-32c1-4fe4-a8e6-06efb0b35db7"
)

// fakeCredentialSource returns one protected credential fixture to the adapter.
type fakeCredentialSource struct {
	credential stores.Credential
}

// roundTripFunc adapts a function into an HTTP transport without opening a listener.
type roundTripFunc func(*http.Request) (*http.Response, error)

// signingFixture contains a trusted Apple-style certificate chain and signing material.
type signingFixture struct {
	rootPEM   string
	chain     []string
	leafKey   *ecdsa.PrivateKey
	apiKey    *ecdsa.PrivateKey
	apiKeyPEM string
}

// TestAdapterVerifiesAuthoritativeLifetimeTransaction verifies signed client and server JWS scope end to end.
func TestAdapterVerifiesAuthoritativeLifetimeTransaction(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 10, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	transaction := transactionPayload{
		OriginalTransactionID: "2000000123456789", TransactionID: "2000000123456790",
		BundleID: fixtureBundleID, ProductID: fixtureProductID, PurchaseDate: now.Add(-time.Hour).UnixMilli(),
		Quantity: 1, Type: appleNonConsumableType, AppAccountToken: fixtureAccountToken,
		OwnershipType: applePurchasedOwnership, SignedDate: now.UnixMilli(), Environment: "Sandbox",
	}
	configuration := credentialFixture(t, fixture, core.EnvironmentSandbox)
	adapter, err := New(fakeCredentialSource{credential: configuration}, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	serverJWS := signTransactionFixture(t, fixture, transaction)
	adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != sandboxBaseURL+"/inApps/v1/transactions/"+transaction.TransactionID {
			t.Fatalf("provider request = %s %s", request.Method, request.URL)
		}
		verifyAuthorizationFixture(t, strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "), &fixture.apiKey.PublicKey, now)
		return jsonResponse(t, http.StatusOK, transactionInfoResponse{SignedTransactionInfo: serverJWS}, nil), nil
	})
	evidenceBytes, err := json.Marshal(clientEvidence{
		SignedTransaction: signTransactionFixture(t, fixture, transaction),
		ProductKind:       core.ProductKindNonConsumable,
	})
	if err != nil {
		t.Fatalf("Marshal() evidence error = %v", err)
	}
	evidence, err := stores.NewEvidence(EvidenceContentType, evidenceBytes)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	binding, err := core.NewStoreReference(core.ReferenceCustomerBinding, "app_account_token", fixtureAccountToken)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	result, err := adapter.Verify(context.Background(), stores.VerificationRequest{
		Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
		ClaimedProducts:          []core.ProviderProductID{fixtureProductID},
		ExpectedCustomerBindings: []core.StoreReference{binding}, Evidence: evidence,
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(result.Observations) != 1 || result.Observations[0].Access != core.AccessAllowed ||
		result.Observations[0].ProductKind != core.ProductKindNonConsumable {
		t.Fatalf("Verify() observations = %#v", result.Observations)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Kind != "apple_server_transaction" {
		t.Fatalf("Verify() artifacts = %#v", result.Artifacts)
	}
}

// TestAdapterNormalizesExpiredSubscription verifies subscription expiry denies access safely.
func TestAdapterNormalizesExpiredSubscription(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 11, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	transaction := transactionPayload{
		OriginalTransactionID: "2000000223456789", TransactionID: "2000000223456790",
		BundleID: fixtureBundleID, ProductID: fixtureProductID, PurchaseDate: now.Add(-48 * time.Hour).UnixMilli(),
		ExpiresDate: now.Add(-time.Hour).UnixMilli(), Quantity: 1, Type: appleSubscriptionType,
		AppAccountToken: fixtureAccountToken, OwnershipType: applePurchasedOwnership,
		SignedDate: now.UnixMilli(), Environment: "Sandbox",
	}
	adapter := adapterFixture(t, fixture, now, transaction, http.StatusOK, nil)
	evidence := evidenceFixture(t, fixture, transaction, core.ProductKindSubscription)
	binding, err := core.NewStoreReference(core.ReferenceCustomerBinding, "app_account_token", fixtureAccountToken)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	result, err := adapter.Verify(context.Background(), stores.VerificationRequest{
		Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
		ClaimedProducts:          []core.ProviderProductID{fixtureProductID},
		ExpectedCustomerBindings: []core.StoreReference{binding}, Evidence: evidence,
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	observation := result.Observations[0]
	if observation.State != core.LifecycleExpired || observation.Access != core.AccessDenied ||
		observation.AccessReason != core.AccessReasonExpired || observation.Renewal.Status != core.RenewalStatusUnknown ||
		observation.ProviderState != string(core.LifecycleExpired) {
		t.Fatalf("Verify() observation = %#v", observation)
	}
}

// TestObservationIdentityIgnoresJWSSigningTime verifies provider re-signing does not create logical duplicates.
func TestObservationIdentityIgnoresJWSSigningTime(t *testing.T) {
	t.Parallel()

	transaction := transactionPayload{
		OriginalTransactionID: "2000000523456789", TransactionID: "2000000523456790",
		BundleID: fixtureBundleID, ProductID: fixtureProductID, PurchaseDate: 1_777_111_200_000,
		Type: appleNonConsumableType, AppAccountToken: fixtureAccountToken,
		OwnershipType: applePurchasedOwnership, SignedDate: 1_777_114_800_000, Environment: "Sandbox",
	}
	renewal := core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable}
	first := observationSnapshotIdentity(
		"application-1", core.ProductKindNonConsumable, transaction,
		core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid,
		renewal, core.OwnershipPurchased, 1,
	)
	transaction.SignedDate += time.Hour.Milliseconds()
	second := observationSnapshotIdentity(
		"application-1", core.ProductKindNonConsumable, transaction,
		core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid,
		renewal, core.OwnershipPurchased, 1,
	)
	if first != second {
		t.Fatalf("observation identities differ: %x != %x", first, second)
	}
}

// TestAdapterRejectsTamperedJWSAndCustomerBinding verifies forged or cross-customer evidence never reaches Apple.
func TestAdapterRejectsTamperedJWSAndCustomerBinding(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	transaction := transactionPayload{
		OriginalTransactionID: "2000000323456789", TransactionID: "2000000323456790",
		BundleID: fixtureBundleID, ProductID: fixtureProductID, PurchaseDate: now.Add(-time.Hour).UnixMilli(),
		Type: appleNonConsumableType, AppAccountToken: fixtureAccountToken,
		OwnershipType: applePurchasedOwnership, SignedDate: now.UnixMilli(), Environment: "Sandbox",
	}
	credential := credentialFixture(t, fixture, core.EnvironmentSandbox)
	adapter, err := New(fakeCredentialSource{credential: credential}, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	adapter.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("provider request occurred for invalid evidence")
		return nil, nil
	})
	validJWS := signTransactionFixture(t, fixture, transaction)
	parts := strings.Split(validJWS, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"transactionId":"forged"}`))
	tamperedEvidence := evidenceFromJWS(t, strings.Join(parts, "."), core.ProductKindNonConsumable)
	validBinding, err := core.NewStoreReference(core.ReferenceCustomerBinding, "app_account_token", fixtureAccountToken)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	request := stores.VerificationRequest{
		Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
		ClaimedProducts:          []core.ProviderProductID{fixtureProductID},
		ExpectedCustomerBindings: []core.StoreReference{validBinding}, Evidence: tamperedEvidence,
	}
	if _, err := adapter.Verify(context.Background(), request); !isFailureKind(err, stores.FailureInvalidEvidence) {
		t.Fatalf("Verify() tampered error = %v", err)
	}

	wrongBinding, err := core.NewStoreReference(core.ReferenceCustomerBinding, "app_account_token", "e2282771-732e-4c60-a39b-e5280657714c")
	if err != nil {
		t.Fatalf("NewStoreReference() wrong binding error = %v", err)
	}
	request.Evidence = evidenceFromJWS(t, validJWS, core.ProductKindNonConsumable)
	request.ExpectedCustomerBindings = []core.StoreReference{wrongBinding}
	if _, err := adapter.Verify(context.Background(), request); !isFailureKind(err, stores.FailureInvalidEvidence) {
		t.Fatalf("Verify() binding error = %v", err)
	}
}

// TestAdapterClassifiesRateLimitAndTimeout verifies provider retry signals remain bounded and retryable.
func TestAdapterClassifiesRateLimitAndTimeout(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 13, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	transaction := transactionPayload{
		OriginalTransactionID: "2000000423456789", TransactionID: "2000000423456790",
		BundleID: fixtureBundleID, ProductID: fixtureProductID, PurchaseDate: now.Add(-time.Hour).UnixMilli(),
		Type: appleNonConsumableType, AppAccountToken: fixtureAccountToken,
		OwnershipType: applePurchasedOwnership, SignedDate: now.UnixMilli(), Environment: "Sandbox",
	}
	evidence := evidenceFixture(t, fixture, transaction, core.ProductKindNonConsumable)
	binding, err := core.NewStoreReference(core.ReferenceCustomerBinding, "app_account_token", fixtureAccountToken)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	request := stores.VerificationRequest{
		Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
		ClaimedProducts:          []core.ProviderProductID{fixtureProductID},
		ExpectedCustomerBindings: []core.StoreReference{binding}, Evidence: evidence,
	}

	rateLimited := adapterFixture(t, fixture, now, transaction, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"17"}})
	_, err = rateLimited.Verify(context.Background(), request)
	var failure *stores.Failure
	if !errors.As(err, &failure) || failure.Kind != stores.FailureRateLimited || failure.RetryAfter != 17*time.Second {
		t.Fatalf("Verify() rate limit error = %#v", err)
	}

	timedOut := adapterFixture(t, fixture, now, transaction, http.StatusOK, nil)
	timedOut.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	_, err = timedOut.Verify(context.Background(), request)
	if !errors.As(err, &failure) || failure.Kind != stores.FailureTemporary || !failure.Retryable() {
		t.Fatalf("Verify() timeout error = %#v", err)
	}
}

// Credential returns the configured opaque Apple fixture.
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

// newSigningFixture creates an Apple-style P-256 root, WWDR intermediate, leaf, and API key.
func newSigningFixture(t *testing.T, now time.Time) signingFixture {
	t.Helper()

	rootKey := generateKey(t)
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Apple Root CA Test"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER := createCertificate(t, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	rootCertificate := parseCertificateFixture(t, rootDER)

	intermediateKey := generateKey(t)
	intermediateTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Apple WWDR Test"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(180 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true,
		KeyUsage:        x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier(appleWWDRIntermediateOID), Value: []byte{0x05, 0x00}}},
	}
	intermediateDER := createCertificate(t, intermediateTemplate, rootCertificate, &intermediateKey.PublicKey, rootKey)
	intermediateCertificate := parseCertificateFixture(t, intermediateDER)

	leafKey := generateKey(t)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "App Store Transaction Test"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(30 * 24 * time.Hour),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	leafDER := createCertificate(t, leafTemplate, intermediateCertificate, &leafKey.PublicKey, intermediateKey)
	apiKey := generateKey(t)
	apiDER, err := x509.MarshalPKCS8PrivateKey(apiKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	return signingFixture{
		rootPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})),
		chain: []string{
			base64.StdEncoding.EncodeToString(leafDER),
			base64.StdEncoding.EncodeToString(intermediateDER),
			base64.StdEncoding.EncodeToString(rootDER),
		},
		leafKey: leafKey, apiKey: apiKey,
		apiKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: apiDER})),
	}
}

// generateKey creates one P-256 test key.
func generateKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return key
}

// createCertificate signs one certificate fixture.
func createCertificate(
	t *testing.T,
	template *x509.Certificate,
	parent *x509.Certificate,
	publicKey *ecdsa.PublicKey,
	parentKey *ecdsa.PrivateKey,
) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, publicKey, parentKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	return der
}

// parseCertificateFixture parses one generated certificate.
func parseCertificateFixture(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	return certificate
}

// credentialFixture builds one opaque Apple credential package.
func credentialFixture(t *testing.T, fixture signingFixture, environment core.Environment) stores.Credential {
	t.Helper()
	payload := credentialPayload{
		IssuerID: "99b16628-15e4-4668-972b-eeff55eeff55", KeyID: "ABCDEFGHIJ",
		BundleID: fixtureBundleID, PrivateKey: fixture.apiKeyPEM, RootCertificates: []string{fixture.rootPEM},
	}
	if environment == core.EnvironmentProduction {
		payload.AppAppleID = 123456789
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() credential error = %v", err)
	}
	credential, err := stores.NewCredential(CredentialKind, CredentialContentType, CredentialSchemaVersion, encoded)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	return credential
}

// signTransactionFixture signs one transaction with the Apple-style leaf certificate chain.
func signTransactionFixture(t *testing.T, fixture signingFixture, transaction transactionPayload) string {
	t.Helper()
	header, err := json.Marshal(jwsHeader{Algorithm: "ES256", Chain: fixture.chain})
	if err != nil {
		t.Fatalf("Marshal() JWS header error = %v", err)
	}
	payload, err := json.Marshal(transaction)
	if err != nil {
		t.Fatalf("Marshal() JWS payload error = %v", err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	input := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, fixture.leafKey, digest[:])
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	signature := append(paddedInteger(r, 32), paddedInteger(s, 32)...)
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// verifyAuthorizationFixture verifies the adapter JWT signature and required Apple claims.
func verifyAuthorizationFixture(
	t *testing.T,
	token string,
	publicKey *ecdsa.PublicKey,
	now time.Time,
) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("authorization token = %q", token)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		t.Fatalf("authorization signature error = %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(publicKey, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Fatal("authorization signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("DecodeString() authorization payload error = %v", err)
	}
	var claims struct {
		Issuer   string `json:"iss"`
		IssuedAt int64  `json:"iat"`
		Expires  int64  `json:"exp"`
		Audience string `json:"aud"`
		BundleID string `json:"bid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("Unmarshal() authorization claims error = %v", err)
	}
	if claims.Issuer == "" || claims.IssuedAt != now.Unix() || claims.Expires != now.Add(tokenLifetime).Unix() ||
		claims.Audience != appleAudience || claims.BundleID != fixtureBundleID {
		t.Fatalf("authorization claims = %#v", claims)
	}
}

// adapterFixture creates an adapter that returns one controlled provider response.
func adapterFixture(
	t *testing.T,
	fixture signingFixture,
	now time.Time,
	transaction transactionPayload,
	status int,
	header http.Header,
) *Adapter {
	t.Helper()
	adapter, err := New(fakeCredentialSource{credential: credentialFixture(t, fixture, core.EnvironmentSandbox)}, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	adapter.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(t, status, transactionInfoResponse{SignedTransactionInfo: signTransactionFixture(t, fixture, transaction)}, header), nil
	})
	return adapter
}

// evidenceFixture signs and wraps one transaction evidence fixture.
func evidenceFixture(
	t *testing.T,
	fixture signingFixture,
	transaction transactionPayload,
	kind core.ProductKind,
) stores.Evidence {
	t.Helper()
	return evidenceFromJWS(t, signTransactionFixture(t, fixture, transaction), kind)
}

// evidenceFromJWS wraps one signed transaction in the versioned Apple client contract.
func evidenceFromJWS(t *testing.T, signed string, kind core.ProductKind) stores.Evidence {
	t.Helper()
	payload, err := json.Marshal(clientEvidence{SignedTransaction: signed, ProductKind: kind})
	if err != nil {
		t.Fatalf("Marshal() evidence error = %v", err)
	}
	evidence, err := stores.NewEvidence(EvidenceContentType, payload)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	return evidence
}

// appleApplication returns one provider-scoped Apple application fixture.
func appleApplication(environment core.Environment) core.Application {
	return core.Application{
		ID: "application-1", ProjectID: "project-1",
		Store: core.StoreApplication{Provider: core.ProviderAppleAppStore, Environment: environment, ID: fixtureBundleID},
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
