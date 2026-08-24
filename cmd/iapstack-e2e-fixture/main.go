// Package main runs the isolated Huawei and webhook fixture used by the Compose release gate.
package main

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// controlAddress exposes non-production fixture controls to the host-side gate client.
	controlAddress = ":8082"
	// providerAddress exposes TLS-only Huawei and webhook endpoints to IAPStack containers.
	providerAddress = ":8443"
	// providerApplicationID is the signed Huawei application identity used by the gate.
	providerApplicationID = "provider-app-e2e"
	// providerProductID is the signed lifetime product identity used by the gate.
	providerProductID = "lifetime-e2e"
	// externalCustomerID is the customer binding carried in signed purchase data.
	externalCustomerID = "customer-external-e2e"
	// purchaseToken is the opaque provider query handle used by the gate.
	purchaseToken = "purchase-token-e2e"
	// orderID is the immutable provider transaction identity used by the gate.
	orderID = "order-e2e-1"
	// providerAccessToken is the deterministic bearer issued by the OAuth fixture.
	providerAccessToken = "provider-access-e2e"
	// providerClientID is the application credential accepted by the OAuth fixture.
	providerClientID = "client-e2e"
	// providerClientSecret is the application secret accepted by the OAuth fixture.
	providerClientSecret = "client-secret-e2e"
	// fixtureBaseURL is the Compose-network TLS origin written into protected credentials.
	fixtureBaseURL = "https://fixture:8443"
	// stateActive returns an authoritative valid lifetime purchase.
	stateActive lifecycleState = "active"
	// stateRefunded returns an authoritative refunded lifetime purchase.
	stateRefunded lifecycleState = "refunded"
	// maximumFixtureBody bounds every provider, webhook, and control request body.
	maximumFixtureBody int64 = 2 << 20
	// shutdownTimeout bounds fixture server shutdown.
	shutdownTimeout = 5 * time.Second
)

// lifecycleState selects the authoritative Huawei fixture response.
type lifecycleState string

// fixture owns signing material, lifecycle state, and observed webhook deliveries.
type fixture struct {
	privateKey    *rsa.PrivateKey
	publicKeyPEM  string
	webhookSecret []byte
	purchasedAt   time.Time
	mu            sync.RWMutex
	state         lifecycleState
	deliveries    []delivery
}

// purchaseData mirrors the Huawei fields consumed by the production adapter.
type purchaseData struct {
	ApplicationID    string `json:"applicationId"`
	ProductID        string `json:"productId"`
	OrderID          string `json:"orderId"`
	PurchaseToken    string `json:"purchaseToken"`
	PurchaseState    int    `json:"purchaseState"`
	PurchaseTime     int64  `json:"purchaseTime"`
	DeveloperPayload string `json:"developerPayload"`
	Quantity         uint32 `json:"quantity"`
}

// evidence contains one Huawei SDK-compatible detached signature envelope.
type evidence struct {
	PurchaseData string `json:"purchase_data"`
	Signature    string `json:"signature"`
	ProductKind  string `json:"product_kind"`
}

// credential contains one Huawei server API package accepted by the production adapter.
type credential struct {
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
	PublicKey       string `json:"public_key"`
	TokenURL        string `json:"token_url"`
	OrderURL        string `json:"order_url"`
	SubscriptionURL string `json:"subscription_url"`
}

// notification contains one signed application-scoped provider notification.
type notification struct {
	ExternalCustomerID string   `json:"external_customer_id"`
	ClaimedProducts    []string `json:"claimed_products"`
	Purchase           evidence `json:"purchase"`
}

// scenario exposes safe configuration and signed evidence to the host-side gate client.
type scenario struct {
	ProviderApplicationID string       `json:"provider_application_id"`
	ProviderProductID     string       `json:"provider_product_id"`
	ExternalCustomerID    string       `json:"external_customer_id"`
	Credential            credential   `json:"credential"`
	Evidence              evidence     `json:"evidence"`
	Notification          notification `json:"notification"`
}

// providerQuery contains the two fields sent by the production Huawei adapter.
type providerQuery struct {
	PurchaseToken string `json:"purchaseToken"`
	ProductID     string `json:"productId"`
}

// delivery records only bounded webhook verification metadata and the exact event body.
type delivery struct {
	EventID        string          `json:"event_id"`
	Timestamp      int64           `json:"timestamp"`
	SignatureValid bool            `json:"signature_valid"`
	Body           json.RawMessage `json:"body"`
}

// deliveryReport summarizes attempts while preserving event identities for deduplication assertions.
type deliveryReport struct {
	Attempts           int        `json:"attempts"`
	UniqueEventIDs     int        `json:"unique_event_ids"`
	AllSignaturesValid bool       `json:"all_signatures_valid"`
	Deliveries         []delivery `json:"deliveries"`
}

// main runs both fixture servers until a signal or listener failure occurs.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run constructs signing material, starts both listeners, and coordinates graceful shutdown.
func run() error {
	certificateFile := strings.TrimSpace(os.Getenv("IAPSTACK_E2E_TLS_CERT_FILE"))
	privateKeyFile := strings.TrimSpace(os.Getenv("IAPSTACK_E2E_TLS_KEY_FILE"))
	webhookSecret := []byte(strings.TrimSpace(os.Getenv("IAPSTACK_E2E_WEBHOOK_SECRET")))
	if certificateFile == "" || privateKeyFile == "" || len(webhookSecret) < 16 {
		return errors.New("fixture TLS files and a webhook secret of at least 16 bytes are required")
	}
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate fixture signing key: %w", err)
	}
	encodedPublicKey, err := x509.MarshalPKIXPublicKey(&signingKey.PublicKey)
	if err != nil {
		return fmt.Errorf("encode fixture public key: %w", err)
	}
	service := &fixture{
		privateKey: signingKey,
		publicKeyPEM: string(pem.EncodeToMemory(&pem.Block{
			Type: "PUBLIC KEY", Bytes: encodedPublicKey,
		})),
		webhookSecret: append([]byte(nil), webhookSecret...),
		purchasedAt:   time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond),
		state:         stateActive,
	}
	controlServer := &http.Server{
		Addr: controlAddress, Handler: service.controlHandler(), ReadHeaderTimeout: 5 * time.Second,
	}
	providerServer := &http.Server{
		Addr: providerAddress, Handler: service.providerHandler(), ReadHeaderTimeout: 5 * time.Second,
	}
	serverErrors := make(chan error, 2)
	go func() {
		serverErrors <- controlServer.ListenAndServe()
	}()
	go func() {
		serverErrors <- providerServer.ListenAndServeTLS(certificateFile, privateKeyFile)
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serverErrors:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return errors.Join(serveErr, controlServer.Shutdown(shutdownContext), providerServer.Shutdown(shutdownContext))
}

// controlHandler registers host-only scenario, lifecycle, delivery, and health endpoints.
func (service *fixture) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", service.health)
	mux.HandleFunc("GET /scenario", service.getScenario)
	mux.HandleFunc("PUT /state/{state}", service.putState)
	mux.HandleFunc("GET /deliveries", service.getDeliveries)
	return mux
}

// providerHandler registers TLS Huawei OAuth/query endpoints and the application webhook.
func (service *fixture) providerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", service.health)
	mux.HandleFunc("POST /token", service.token)
	mux.HandleFunc("POST /order", service.order)
	mux.HandleFunc("POST /subscription", service.subscription)
	mux.HandleFunc("POST /webhook", service.webhook)
	return mux
}

// health reports fixture process liveness.
func (service *fixture) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

// getScenario returns signed evidence for the current authoritative lifecycle state.
func (service *fixture) getScenario(writer http.ResponseWriter, _ *http.Request) {
	evidenceValue, err := service.signedEvidence()
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "fixture_signing_failed"})
		return
	}
	writeJSON(writer, http.StatusOK, scenario{
		ProviderApplicationID: providerApplicationID,
		ProviderProductID:     providerProductID,
		ExternalCustomerID:    externalCustomerID,
		Credential: credential{
			ClientID: providerClientID, ClientSecret: providerClientSecret, PublicKey: service.publicKeyPEM,
			TokenURL: fixtureBaseURL + "/token", OrderURL: fixtureBaseURL + "/order",
			SubscriptionURL: fixtureBaseURL + "/subscription",
		},
		Evidence: evidenceValue,
		Notification: notification{
			ExternalCustomerID: externalCustomerID,
			ClaimedProducts:    []string{providerProductID},
			Purchase:           evidenceValue,
		},
	})
}

// putState changes the authoritative purchase lifecycle for the next query.
func (service *fixture) putState(writer http.ResponseWriter, request *http.Request) {
	requested := lifecycleState(request.PathValue("state"))
	if requested != stateActive && requested != stateRefunded {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "unsupported_state"})
		return
	}
	service.mu.Lock()
	service.state = requested
	service.mu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]lifecycleState{"state": requested})
}

// getDeliveries returns a defensive snapshot of received webhook attempts.
func (service *fixture) getDeliveries(writer http.ResponseWriter, _ *http.Request) {
	service.mu.RLock()
	deliveries := append([]delivery(nil), service.deliveries...)
	service.mu.RUnlock()
	unique := make(map[string]struct{}, len(deliveries))
	allValid := true
	for _, value := range deliveries {
		unique[value.EventID] = struct{}{}
		allValid = allValid && value.SignatureValid
	}
	writeJSON(writer, http.StatusOK, deliveryReport{
		Attempts: len(deliveries), UniqueEventIDs: len(unique), AllSignaturesValid: allValid, Deliveries: deliveries,
	})
}

// token validates OAuth client credentials and returns a bounded access token response.
func (service *fixture) token(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "client_credentials" ||
		request.Form.Get("client_id") != providerClientID || request.Form.Get("client_secret") != providerClientSecret {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"access_token": providerAccessToken, "expires_in": 300})
}

// order returns the currently authoritative signed lifetime purchase.
func (service *fixture) order(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+providerAccessToken {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var query providerQuery
	if !readJSON(writer, request, &query) {
		return
	}
	if query.PurchaseToken != purchaseToken || query.ProductID != providerProductID {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	payload, signature, err := service.signedPurchase()
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "fixture_signing_failed"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{
		"responseCode": "0", "purchaseTokenData": payload, "dataSignature": signature,
	})
}

// subscription reports that the lifetime fixture has no subscription record.
func (service *fixture) subscription(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusNotFound, map[string]string{"error": "not_found"})
}

// webhook authenticates one outbound IAPStack signature and records the bounded attempt.
func (service *fixture) webhook(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumFixtureBody+1))
	if err != nil || int64(len(body)) > maximumFixtureBody || !json.Valid(body) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	timestamp, timestampErr := strconv.ParseInt(request.Header.Get("IAPStack-Timestamp"), 10, 64)
	signature := strings.TrimPrefix(request.Header.Get("IAPStack-Signature"), "v1=")
	expected := webhookSignature(service.webhookSecret, timestamp, body)
	valid := timestampErr == nil && request.Header.Get("IAPStack-Signature") == "v1="+signature &&
		hmac.Equal([]byte(signature), []byte(expected))
	service.mu.Lock()
	service.deliveries = append(service.deliveries, delivery{
		EventID: request.Header.Get("IAPStack-Event-ID"), Timestamp: timestamp,
		SignatureValid: valid, Body: append(json.RawMessage(nil), body...),
	})
	service.mu.Unlock()
	if !valid {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "invalid_signature"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "accepted"})
}

// signedEvidence creates a device-style envelope for the current purchase state.
func (service *fixture) signedEvidence() (evidence, error) {
	payload, signature, err := service.signedPurchase()
	if err != nil {
		return evidence{}, err
	}
	return evidence{PurchaseData: payload, Signature: signature, ProductKind: "non_consumable"}, nil
}

// signedPurchase serializes and signs the current authoritative purchase snapshot.
func (service *fixture) signedPurchase() (string, string, error) {
	service.mu.RLock()
	state := service.state
	service.mu.RUnlock()
	purchaseState := 0
	if state == stateRefunded {
		purchaseState = 2
	}
	payload, err := json.Marshal(purchaseData{
		ApplicationID: providerApplicationID, ProductID: providerProductID, OrderID: orderID,
		PurchaseToken: purchaseToken, PurchaseState: purchaseState, PurchaseTime: service.purchasedAt.UnixMilli(),
		DeveloperPayload: externalCustomerID, Quantity: 1,
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal fixture purchase: %w", err)
	}
	digest := sha256.Sum256(payload)
	signature, err := rsa.SignPKCS1v15(rand.Reader, service.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", "", fmt.Errorf("sign fixture purchase: %w", err)
	}
	return string(payload), base64.StdEncoding.EncodeToString(signature), nil
}

// readJSON decodes one bounded JSON body for a fixture endpoint.
func readJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumFixtureBody+1))
	if err != nil || int64(len(body)) > maximumFixtureBody || json.Unmarshal(body, destination) != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return false
	}
	return true
}

// webhookSignature computes the production-compatible HMAC over timestamp dot exact body.
func webhookSignature(secret []byte, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = io.WriteString(mac, strconv.FormatInt(timestamp, 10))
	_, _ = io.WriteString(mac, ".")
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// writeJSON writes one fixture JSON response.
func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
