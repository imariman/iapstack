package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// fixtureTestWebhookSecret authenticates the local delivery exercised by the handler test.
	fixtureTestWebhookSecret = "fixture-test-webhook-secret"
)

// TestFixtureHandlersServeSignedPurchaseAndWebhookJourney verifies the release fixture's complete HTTP success path.
func TestFixtureHandlersServeSignedPurchaseAndWebhookJourney(t *testing.T) {
	service := newFixtureTestService(t)
	control := service.controlHandler()
	provider := service.providerHandler()

	assertFixtureStatus(t, control, http.MethodGet, "/healthz", "", nil, http.StatusOK)
	scenarioResponse := assertFixtureStatus(t, control, http.MethodGet, "/scenario", "", nil, http.StatusOK)
	var activeScenario scenario
	decodeFixtureResponse(t, scenarioResponse, &activeScenario)
	if activeScenario.ProviderApplicationID != providerApplicationID ||
		activeScenario.ProviderProductID != providerProductID ||
		activeScenario.ExternalCustomerID != externalCustomerID {
		t.Fatalf("active scenario identity = %#v", activeScenario)
	}
	assertFixtureEvidence(t, service.privateKey, activeScenario.Evidence, 0)

	tokenForm := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {providerClientID},
		"client_secret": {providerClientSecret},
	}.Encode()
	tokenResponse := assertFixtureStatus(
		t,
		provider,
		http.MethodPost,
		"/token",
		"application/x-www-form-urlencoded",
		strings.NewReader(tokenForm),
		http.StatusOK,
	)
	var token map[string]any
	decodeFixtureResponse(t, tokenResponse, &token)
	if token["access_token"] != providerAccessToken {
		t.Fatalf("token response = %#v", token)
	}

	orderRequest := httptest.NewRequest(
		http.MethodPost,
		"/order",
		strings.NewReader(`{"purchaseToken":"`+purchaseToken+`","productId":"`+providerProductID+`"}`),
	)
	orderRequest.Header.Set("Authorization", "Bearer "+providerAccessToken)
	orderRequest.Header.Set("Content-Type", "application/json")
	orderRecorder := httptest.NewRecorder()
	provider.ServeHTTP(orderRecorder, orderRequest)
	if orderRecorder.Code != http.StatusOK {
		t.Fatalf("order status = %d: %s", orderRecorder.Code, orderRecorder.Body.String())
	}
	var order struct {
		ResponseCode      string `json:"responseCode"`
		PurchaseTokenData string `json:"purchaseTokenData"`
		DataSignature     string `json:"dataSignature"`
	}
	decodeFixtureResponse(t, orderRecorder, &order)
	if order.ResponseCode != "0" {
		t.Fatalf("order response = %#v", order)
	}
	assertFixtureSignature(t, &service.privateKey.PublicKey, order.PurchaseTokenData, order.DataSignature)

	assertFixtureStatus(t, control, http.MethodPut, "/state/refunded", "", nil, http.StatusOK)
	refundedResponse := assertFixtureStatus(t, control, http.MethodGet, "/scenario", "", nil, http.StatusOK)
	var refundedScenario scenario
	decodeFixtureResponse(t, refundedResponse, &refundedScenario)
	assertFixtureEvidence(t, service.privateKey, refundedScenario.Evidence, 2)

	webhookBody := []byte(`{"schema_version":1,"event_type":"entitlement.changed"}`)
	timestamp := time.Date(2026, time.September, 2, 14, 0, 0, 0, time.UTC).Unix()
	webhookRequest := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(webhookBody)))
	webhookRequest.Header.Set("IAPStack-Event-ID", "fixture-event-1")
	webhookRequest.Header.Set("IAPStack-Timestamp", strconv.FormatInt(timestamp, 10))
	webhookRequest.Header.Set("IAPStack-Signature", "v2="+webhookSignature([]byte(fixtureTestWebhookSecret), "fixture-event-1", timestamp, webhookBody))
	webhookRecorder := httptest.NewRecorder()
	provider.ServeHTTP(webhookRecorder, webhookRequest)
	if webhookRecorder.Code != http.StatusOK {
		t.Fatalf("webhook status = %d: %s", webhookRecorder.Code, webhookRecorder.Body.String())
	}

	deliveriesResponse := assertFixtureStatus(t, control, http.MethodGet, "/deliveries", "", nil, http.StatusOK)
	var deliveries deliveryReport
	decodeFixtureResponse(t, deliveriesResponse, &deliveries)
	if deliveries.Attempts != 1 || deliveries.UniqueEventIDs != 1 || !deliveries.AllSignaturesValid ||
		len(deliveries.Deliveries) != 1 || deliveries.Deliveries[0].EventID != "fixture-event-1" {
		t.Fatalf("delivery report = %#v", deliveries)
	}
}

// newFixtureTestService constructs deterministic signing and webhook state for handler tests.
func newFixtureTestService(t *testing.T) *fixture {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate fixture signing key: %v", err)
	}
	encodedPublicKey, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("encode fixture public key: %v", err)
	}
	return &fixture{
		privateKey: privateKey,
		publicKeyPEM: string(pem.EncodeToMemory(&pem.Block{
			Type: "PUBLIC KEY", Bytes: encodedPublicKey,
		})),
		webhookSecret: []byte(fixtureTestWebhookSecret),
		purchasedAt:   time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC),
		state:         stateActive,
	}
}

// assertFixtureStatus sends one routed fixture request and requires the expected response status.
func assertFixtureStatus(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	contentType string,
	body io.Reader,
	expectedStatus int,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != expectedStatus {
		t.Fatalf("%s %s status = %d, want %d: %s", method, path, recorder.Code, expectedStatus, recorder.Body.String())
	}
	return recorder
}

// decodeFixtureResponse decodes one fixture JSON response into the requested contract.
func decodeFixtureResponse(t *testing.T, recorder *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode fixture response: %v: %s", err, recorder.Body.String())
	}
}

// assertFixtureEvidence verifies the device envelope signature and expected purchase lifecycle value.
func assertFixtureEvidence(t *testing.T, privateKey *rsa.PrivateKey, value evidence, expectedState int) {
	t.Helper()
	if value.ProductKind != "non_consumable" {
		t.Fatalf("evidence product kind = %q", value.ProductKind)
	}
	assertFixtureSignature(t, &privateKey.PublicKey, value.PurchaseData, value.Signature)
	var purchase purchaseData
	if err := json.Unmarshal([]byte(value.PurchaseData), &purchase); err != nil {
		t.Fatalf("decode signed purchase: %v", err)
	}
	if purchase.PurchaseState != expectedState || purchase.ApplicationID != providerApplicationID ||
		purchase.ProductID != providerProductID || purchase.DeveloperPayload != externalCustomerID {
		t.Fatalf("signed purchase = %#v", purchase)
	}
}

// assertFixtureSignature verifies one base64 PKCS#1 v1.5 SHA-256 signature.
func assertFixtureSignature(t *testing.T, publicKey *rsa.PublicKey, payload, encodedSignature string) {
	t.Helper()
	signature, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil {
		t.Fatalf("decode fixture signature: %v", err)
	}
	digest := sha256.Sum256([]byte(payload))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("verify fixture signature: %v", err)
	}
}
