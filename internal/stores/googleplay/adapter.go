// Package googleplay implements Google Play purchase verification and reconciliation.
package googleplay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// CredentialKind identifies the v1 Google Play Android Publisher credential package.
	CredentialKind stores.CredentialKind = "google_play_android_publisher"
	// CredentialContentType identifies the Google Play credential JSON representation.
	CredentialContentType = "application/vnd.iapstack.google-play-credentials+json"
	// CredentialSchemaVersion identifies the initial Android Publisher credential shape.
	CredentialSchemaVersion = 1
	// EvidenceContentType identifies the Google Play purchase-token evidence representation.
	EvidenceContentType = stores.GooglePlayEvidenceContentType
	// defaultTokenURL is Google's documented OAuth 2.0 service-account token endpoint.
	defaultTokenURL = "https://oauth2.googleapis.com/token" // #nosec G101 -- This is Google's public OAuth endpoint, not credential material.
	// defaultPublisherURL is Google's documented Android Publisher API root.
	defaultPublisherURL = "https://androidpublisher.googleapis.com"
	// androidPublisherScope authorizes Google Play Developer API purchase reads.
	androidPublisherScope = "https://www.googleapis.com/auth/androidpublisher"
	// serviceAccountGrantType identifies the OAuth 2.0 JWT bearer assertion exchange.
	serviceAccountGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	// assertionLifetime keeps service-account assertions below Google's one-hour maximum.
	assertionLifetime = 5 * time.Minute
	// maximumProviderResponse bounds OAuth and Android Publisher response bodies.
	maximumProviderResponse int64 = 2 << 20
	// acknowledgeActionKind identifies a Google Play purchase acknowledgement after durable entitlement persistence.
	acknowledgeActionKind = "acknowledge_purchase"
	// acknowledgementStatePending identifies a purchase that Google still expects the backend to acknowledge.
	acknowledgementStatePending = "ACKNOWLEDGEMENT_STATE_PENDING"
	// acknowledgementStateAcknowledged identifies a purchase already completed with Google Play.
	acknowledgementStateAcknowledged = "ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED"

	// productStatePurchased identifies a completed Google Play one-time purchase.
	productStatePurchased = "PURCHASED"
	// productStateCancelled identifies a canceled Google Play one-time purchase.
	productStateCancelled = "CANCELLED"
	// productStatePending identifies a Google Play one-time purchase awaiting payment.
	productStatePending = "PENDING"

	// subscriptionStatePending identifies a subscription awaiting initial payment.
	subscriptionStatePending = "SUBSCRIPTION_STATE_PENDING"
	// subscriptionStateActive identifies an active Google Play subscription.
	subscriptionStateActive = "SUBSCRIPTION_STATE_ACTIVE"
	// subscriptionStatePaused identifies a paused Google Play subscription.
	subscriptionStatePaused = "SUBSCRIPTION_STATE_PAUSED"
	// subscriptionStateGrace identifies a subscription in billing grace period.
	subscriptionStateGrace = "SUBSCRIPTION_STATE_IN_GRACE_PERIOD"
	// subscriptionStateOnHold identifies a subscription suspended for billing recovery.
	subscriptionStateOnHold = "SUBSCRIPTION_STATE_ON_HOLD"
	// subscriptionStateCanceled identifies a canceled subscription that has not expired.
	subscriptionStateCanceled = "SUBSCRIPTION_STATE_CANCELED"
	// subscriptionStateExpired identifies an expired Google Play subscription.
	subscriptionStateExpired = "SUBSCRIPTION_STATE_EXPIRED"
	// subscriptionStatePendingCanceled identifies a canceled pending subscription purchase.
	subscriptionStatePendingCanceled = "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED"
)

// Adapter verifies Google Play purchase tokens using application-scoped service-account credentials.
type Adapter struct {
	credentials        stores.CredentialSource
	client             *http.Client
	notificationTokens notificationTokenValidator
	clock              func() time.Time
	tokenURL           string
	publisherURL       string
}

// credentialPayload is the versioned private Google Play service-account configuration.
type credentialPayload struct {
	ClientEmail  string             `json:"client_email"`
	PrivateKeyID string             `json:"private_key_id"`
	PrivateKey   string             `json:"private_key"`
	RTDN         *rtdnConfiguration `json:"rtdn,omitempty"`
}

// configuration contains parsed service-account signing material.
type configuration struct {
	clientEmail  string
	privateKeyID string
	privateKey   *rsa.PrivateKey
	rtdn         *rtdnConfiguration
}

// clientEvidence contains the purchase token returned by Google Play Billing.
type clientEvidence struct {
	PurchaseToken string           `json:"purchase_token"`
	ProductKind   core.ProductKind `json:"product_kind"`
}

// tokenResponse contains the OAuth bearer fields required by Android Publisher calls.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// productPurchase contains the Google ProductPurchaseV2 fields required for normalization.
type productPurchase struct {
	Kind                        string               `json:"kind"`
	ProductLineItems            []productLineItem    `json:"productLineItem"`
	PurchaseStateContext        purchaseStateContext `json:"purchaseStateContext"`
	TestPurchaseContext         *struct{}            `json:"testPurchaseContext"`
	OrderID                     string               `json:"orderId"`
	ObfuscatedExternalAccountID string               `json:"obfuscatedExternalAccountId"`
	PurchaseCompletionTime      time.Time            `json:"purchaseCompletionTime"`
	AcknowledgementState        string               `json:"acknowledgementState"`
}

// productLineItem contains one Google Play one-time product and its quantity details.
type productLineItem struct {
	ProductID           string              `json:"productId"`
	ProductOfferDetails productOfferDetails `json:"productOfferDetails"`
}

// productOfferDetails contains quantity and consumption fields for one product line item.
type productOfferDetails struct {
	Quantity           uint32 `json:"quantity"`
	RefundableQuantity uint32 `json:"refundableQuantity"`
	ConsumptionState   string `json:"consumptionState"`
	PurchaseOptionID   string `json:"purchaseOptionId"`
}

// purchaseStateContext contains the current ProductPurchaseV2 purchase state.
type purchaseStateContext struct {
	PurchaseState string `json:"purchaseState"`
}

// subscriptionPurchase contains the Google SubscriptionPurchaseV2 fields required for normalization.
type subscriptionPurchase struct {
	Kind                       string                     `json:"kind"`
	LineItems                  []subscriptionLineItem     `json:"lineItems"`
	StartTime                  time.Time                  `json:"startTime"`
	SubscriptionState          string                     `json:"subscriptionState"`
	LatestOrderID              string                     `json:"latestOrderId"`
	LinkedPurchaseToken        string                     `json:"linkedPurchaseToken"`
	TestPurchase               *struct{}                  `json:"testPurchase"`
	AcknowledgementState       string                     `json:"acknowledgementState"`
	ExternalAccountIdentifiers externalAccountIdentifiers `json:"externalAccountIdentifiers"`
}

// subscriptionLineItem contains one current Google Play subscription product and plan.
type subscriptionLineItem struct {
	ProductID               string            `json:"productId"`
	ExpiryTime              time.Time         `json:"expiryTime"`
	LatestSuccessfulOrderID string            `json:"latestSuccessfulOrderId"`
	AutoRenewingPlan        *autoRenewingPlan `json:"autoRenewingPlan"`
	PrepaidPlan             *struct{}         `json:"prepaidPlan"`
}

// autoRenewingPlan contains the current renewal switch for one subscription item.
type autoRenewingPlan struct {
	AutoRenewEnabled bool `json:"autoRenewEnabled"`
}

// externalAccountIdentifiers contains Google Play's application customer bindings.
type externalAccountIdentifiers struct {
	ObfuscatedExternalAccountID string `json:"obfuscatedExternalAccountId"`
	ObfuscatedExternalProfileID string `json:"obfuscatedExternalProfileId"`
}

// queryResult contains one authoritative provider artifact and its normalized observations.
type queryResult struct {
	artifact          stores.Evidence
	observations      []core.PurchaseObservation
	postCommitActions []stores.PostCommitAction
}

// New constructs a bounded Google Play Android Publisher adapter.
func New(credentials stores.CredentialSource, timeout time.Duration) (*Adapter, error) {
	if credentials == nil {
		return nil, errors.New("google play credential source is required")
	}
	if timeout <= 0 {
		return nil, errors.New("google play provider timeout must be positive")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	notificationTokens, err := newGoogleNotificationTokenValidator(client)
	if err != nil {
		return nil, fmt.Errorf("construct Google notification token validator: %w", err)
	}
	return &Adapter{
		credentials: credentials, client: client, notificationTokens: notificationTokens,
		clock: time.Now, tokenURL: defaultTokenURL, publisherURL: defaultPublisherURL,
	}, nil
}

// Provider identifies Google Play as the implemented store.
func (adapter *Adapter) Provider() core.Provider {
	return core.ProviderGooglePlay
}

// Verify queries Google Play with submitted purchase-token evidence and normalizes authoritative state.
func (adapter *Adapter) Verify(
	ctx context.Context,
	request stores.VerificationRequest,
) (stores.VerificationResult, error) {
	if err := request.Validate(); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	evidence, err := parseEvidence(request.Evidence)
	if err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	configuration, err := adapter.configuration(ctx, request.Application)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	result, err := adapter.query(ctx, request.Application, configuration, evidence.PurchaseToken, evidence.ProductKind)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	verificationResult := stores.VerificationResult{
		VerifiedAt:        adapter.clock().UTC(),
		Artifacts:         []stores.VerifiedArtifact{{Kind: "google_play_server_response", Evidence: result.artifact}},
		Observations:      result.observations,
		PostCommitActions: result.postCommitActions,
	}
	if err := verificationResult.ValidateForVerification(request); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	return verificationResult, nil
}

// Reconcile queries Google Play using one protected purchase-token reference.
func (adapter *Adapter) Reconcile(
	ctx context.Context,
	request stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	if err := request.Validate(); err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	if len(request.ExpectedProducts) != 1 {
		return stores.VerificationResult{}, invalid("reconcile", errors.New("google play reconciliation requires one expected product"))
	}
	purchaseToken := ""
	for _, reference := range request.QueryReferences {
		if reference.Kind == "purchase_token" {
			purchaseToken = reference.Value()
			break
		}
	}
	if purchaseToken == "" {
		return stores.VerificationResult{}, invalid("reconcile", errors.New("google play reconciliation requires a purchase token"))
	}
	configuration, err := adapter.configuration(ctx, request.Application)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	result, err := adapter.query(ctx, request.Application, configuration, purchaseToken, request.ExpectedProductKind)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	verificationResult := stores.VerificationResult{
		VerifiedAt:        adapter.clock().UTC(),
		Artifacts:         []stores.VerifiedArtifact{{Kind: "google_play_server_response", Evidence: result.artifact}},
		Observations:      result.observations,
		PostCommitActions: result.postCommitActions,
	}
	if err := verificationResult.ValidateForReconciliation(request); err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	return verificationResult, nil
}

// PostCommit acknowledges verified Google Play purchases only after their durable state has committed.
func (adapter *Adapter) PostCommit(ctx context.Context, request stores.PostCommitRequest) error {
	if err := request.Validate(); err != nil {
		return invalid("acknowledge", err)
	}
	if request.Application.Store.Provider != adapter.Provider() {
		return invalid("acknowledge", errors.New("google play post-commit application scope is invalid"))
	}
	configuration, err := adapter.configuration(ctx, request.Application)
	if err != nil {
		return err
	}
	accessToken, err := adapter.accessToken(ctx, configuration)
	if err != nil {
		return err
	}
	for index, action := range request.Actions {
		if err := adapter.acknowledge(ctx, request.Application, accessToken, action); err != nil {
			return fmt.Errorf("acknowledge Google Play purchase action %d: %w", index, err)
		}
	}
	return nil
}

// configuration opens and validates one application-scoped Google Play credential payload.
func (adapter *Adapter) configuration(ctx context.Context, application core.Application) (configuration, error) {
	credential, err := adapter.credentials.Credential(ctx, application, CredentialKind)
	if err != nil {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailureUnauthorized, 0, err)
	}
	if credential.ContentType != CredentialContentType || credential.SchemaVersion != CredentialSchemaVersion {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	payloadBytes := credential.Bytes()
	defer zero(payloadBytes)
	var payload credentialPayload
	if err := decodeStrict(payloadBytes, &payload); err != nil {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, err)
	}
	if application.Store.Provider != core.ProviderGooglePlay || payload.ClientEmail == "" ||
		payload.PrivateKeyID == "" || payload.PrivateKey == "" || !strings.Contains(payload.ClientEmail, "@") {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	privateKey, err := parsePrivateKey(payload.PrivateKey)
	if err != nil {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, err)
	}
	if payload.RTDN != nil {
		copy := *payload.RTDN
		payload.RTDN = &copy
	}
	return configuration{
		clientEmail: payload.ClientEmail, privateKeyID: payload.PrivateKeyID,
		privateKey: privateKey, rtdn: payload.RTDN,
	}, nil
}

// query exchanges a service-account assertion and reads one authoritative purchase resource.
func (adapter *Adapter) query(
	ctx context.Context,
	application core.Application,
	configuration configuration,
	purchaseToken string,
	kind core.ProductKind,
) (queryResult, error) {
	accessToken, err := adapter.accessToken(ctx, configuration)
	if err != nil {
		return queryResult{}, err
	}
	packageName := url.PathEscape(string(application.Store.ID))
	token := url.PathEscape(purchaseToken)
	path := "/androidpublisher/v3/applications/" + packageName + "/purchases/productsv2/tokens/" + token
	if kind == core.ProductKindSubscription {
		path = "/androidpublisher/v3/applications/" + packageName + "/purchases/subscriptionsv2/tokens/" + token
	} else if kind != core.ProductKindNonConsumable {
		return queryResult{}, invalid("query", fmt.Errorf("unsupported Google Play product kind %q", kind))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(adapter.publisherURL, "/")+path, nil)
	if err != nil {
		return queryResult{}, stores.NewFailure(adapter.Provider(), "query", stores.FailurePermanent, 0, err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	body, status, responseHeader, err := adapter.do(request)
	if err != nil {
		return queryResult{}, err
	}
	if failure := adapter.statusFailure("query", status, responseHeader); failure != nil {
		return queryResult{}, failure
	}
	artifact, err := stores.NewEvidence("application/json", body)
	if err != nil {
		return queryResult{}, invalid("query", err)
	}
	if kind == core.ProductKindSubscription {
		var purchase subscriptionPurchase
		if err := json.Unmarshal(body, &purchase); err != nil {
			return queryResult{}, invalid("query", errors.New("google play subscription response is invalid"))
		}
		observations, err := adapter.subscriptionObservations(application, purchaseToken, purchase)
		if err != nil {
			return queryResult{}, err
		}
		actions, err := acknowledgementActions(observations[0], purchase.SubscriptionState, purchase.AcknowledgementState)
		if err != nil {
			return queryResult{}, invalid("normalize", err)
		}
		return queryResult{artifact: artifact, observations: observations, postCommitActions: actions}, nil
	}
	var purchase productPurchase
	if err := json.Unmarshal(body, &purchase); err != nil {
		return queryResult{}, invalid("query", errors.New("google play product response is invalid"))
	}
	observations, err := adapter.productObservations(application, purchaseToken, purchase)
	if err != nil {
		return queryResult{}, err
	}
	actions, err := acknowledgementActions(
		observations[0],
		purchase.PurchaseStateContext.PurchaseState,
		purchase.AcknowledgementState,
	)
	if err != nil {
		return queryResult{}, invalid("normalize", err)
	}
	return queryResult{artifact: artifact, observations: observations, postCommitActions: actions}, nil
}

// acknowledge sends one bounded Android Publisher acknowledgement for a verified purchase token.
func (adapter *Adapter) acknowledge(
	ctx context.Context,
	application core.Application,
	accessToken string,
	action stores.PostCommitAction,
) error {
	if action.Kind != acknowledgeActionKind {
		return invalid("acknowledge", fmt.Errorf("unsupported Google Play post-commit action %q", action.Kind))
	}
	purchaseToken := ""
	for _, reference := range action.QueryReferences {
		if reference.Kind != "purchase_token" {
			continue
		}
		if purchaseToken != "" {
			return invalid("acknowledge", errors.New("google play acknowledgement has multiple purchase tokens"))
		}
		purchaseToken = reference.Value()
	}
	if purchaseToken == "" {
		return invalid("acknowledge", errors.New("google play acknowledgement requires a purchase token"))
	}
	packageName := url.PathEscape(string(application.Store.ID))
	productID := url.PathEscape(string(action.ProductID))
	token := url.PathEscape(purchaseToken)
	path := "/androidpublisher/v3/applications/" + packageName + "/purchases/products/" + productID + "/tokens/" + token + ":acknowledge"
	if action.ProductKind == core.ProductKindSubscription {
		path = "/androidpublisher/v3/applications/" + packageName + "/purchases/subscriptions/" + productID + "/tokens/" + token + ":acknowledge"
	} else if action.ProductKind != core.ProductKindNonConsumable {
		return invalid("acknowledge", fmt.Errorf("unsupported Google Play acknowledgement product kind %q", action.ProductKind))
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(adapter.publisherURL, "/")+path,
		bytes.NewBufferString("{}"),
	)
	if err != nil {
		return stores.NewFailure(adapter.Provider(), "acknowledge", stores.FailurePermanent, 0, err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	_, status, responseHeader, err := adapter.do(request)
	if err != nil {
		return err
	}
	failure := adapter.statusFailure("acknowledge", status, responseHeader)
	if failure != nil && failure.Kind == stores.FailureConflict {
		failure.Kind = stores.FailureTemporary
	}
	if failure != nil {
		return failure
	}
	return nil
}

// accessToken exchanges one signed service-account assertion for an OAuth bearer.
func (adapter *Adapter) accessToken(ctx context.Context, configuration configuration) (string, error) {
	assertion, err := adapter.assertion(configuration)
	if err != nil {
		return "", stores.NewFailure(adapter.Provider(), "token", stores.FailurePermanent, 0, err)
	}
	form := url.Values{}
	form.Set("grant_type", serviceAccountGrantType)
	form.Set("assertion", assertion)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, adapter.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", stores.NewFailure(adapter.Provider(), "token", stores.FailurePermanent, 0, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	body, status, responseHeader, err := adapter.do(request)
	if err != nil {
		return "", err
	}
	if failure := adapter.statusFailure("token", status, responseHeader); failure != nil {
		if failure.Kind == stores.FailureNotFound || failure.Kind == stores.FailureConflict || failure.Kind == stores.FailurePermanent {
			failure.Kind = stores.FailureUnauthorized
		}
		return "", failure
	}
	var response tokenResponse
	if err := decodeStrict(body, &response); err != nil || response.AccessToken == "" ||
		(response.TokenType != "Bearer" && response.TokenType != "bearer") {
		return "", stores.NewFailure(adapter.Provider(), "token", stores.FailureUnauthorized, 0, err)
	}
	return response.AccessToken, nil
}

// assertion signs one short-lived RS256 OAuth service-account JWT.
func (adapter *Adapter) assertion(configuration configuration) (string, error) {
	now := adapter.clock().UTC()
	header, err := json.Marshal(map[string]string{
		"alg": "RS256", "typ": "JWT", "kid": configuration.privateKeyID,
	})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"iss": configuration.clientEmail, "scope": androidPublisherScope, "aud": adapter.tokenURL,
		"iat": now.Unix(), "exp": now.Add(assertionLifetime).Unix(),
	})
	if err != nil {
		return "", err
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, configuration.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// productObservations normalizes one ProductPurchaseV2 response.
func (adapter *Adapter) productObservations(
	application core.Application,
	purchaseToken string,
	purchase productPurchase,
) ([]core.PurchaseObservation, error) {
	if len(purchase.ProductLineItems) != 1 {
		return nil, invalid("normalize", errors.New("google play non-consumable purchase requires one line item"))
	}
	if err := validateEnvironment(application.Store.Environment, purchase.TestPurchaseContext != nil); err != nil {
		return nil, invalid("normalize", err)
	}
	lineItem := purchase.ProductLineItems[0]
	if lineItem.ProductID == "" || purchase.ObfuscatedExternalAccountID == "" {
		return nil, invalid("normalize", errors.New("google play product identity or customer binding is missing"))
	}
	state, access, reason := normalizeProductState(purchase.PurchaseStateContext.PurchaseState)
	quantity := lineItem.ProductOfferDetails.Quantity
	if quantity == 0 {
		quantity = 1
	}
	references, err := googleReferences(
		purchaseToken,
		purchase.OrderID,
		"",
		purchase.ObfuscatedExternalAccountID,
	)
	if err != nil {
		return nil, invalid("normalize", err)
	}
	identity := productObservationIdentity(application.ID, purchaseToken, purchase, lineItem, state, access, reason, quantity)
	observation := core.PurchaseObservation{
		ID:            core.ObservationID("gpl-" + hex.EncodeToString(identity[:16])),
		ApplicationID: application.ID, Store: application.Store,
		ProductID: core.ProviderProductID(lineItem.ProductID), ProductKind: core.ProductKindNonConsumable,
		State: state, ProviderState: purchase.PurchaseStateContext.PurchaseState,
		Access: access, AccessReason: reason, Ownership: core.OwnershipPurchased, Quantity: quantity,
		OccurredAt: purchase.PurchaseCompletionTime, ObservedAt: adapter.clock().UTC(),
		EffectivePeriod: core.EffectivePeriod{StartsAt: purchase.PurchaseCompletionTime},
		Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
		References:      references,
	}
	if state == core.LifecyclePending {
		observation.EffectivePeriod = core.EffectivePeriod{}
	}
	if err := observation.Validate(); err != nil {
		return nil, invalid("normalize", err)
	}
	return []core.PurchaseObservation{observation}, nil
}

// subscriptionObservations normalizes one SubscriptionPurchaseV2 response.
func (adapter *Adapter) subscriptionObservations(
	application core.Application,
	purchaseToken string,
	purchase subscriptionPurchase,
) ([]core.PurchaseObservation, error) {
	if len(purchase.LineItems) != 1 {
		return nil, invalid("normalize", errors.New("google play subscription requires one line item"))
	}
	if err := validateEnvironment(application.Store.Environment, purchase.TestPurchase != nil); err != nil {
		return nil, invalid("normalize", err)
	}
	lineItem := purchase.LineItems[0]
	accountID := purchase.ExternalAccountIdentifiers.ObfuscatedExternalAccountID
	if lineItem.ProductID == "" || accountID == "" {
		return nil, invalid("normalize", errors.New("google play subscription identity or customer binding is missing"))
	}
	state, access, reason := normalizeSubscriptionState(purchase.SubscriptionState)
	renewal, err := subscriptionRenewal(lineItem)
	if err != nil {
		return nil, invalid("normalize", err)
	}
	orderID := lineItem.LatestSuccessfulOrderID
	if orderID == "" {
		orderID = purchase.LatestOrderID
	}
	references, err := googleReferences(purchaseToken, orderID, purchase.LinkedPurchaseToken, accountID)
	if err != nil {
		return nil, invalid("normalize", err)
	}
	identity := subscriptionObservationIdentity(application.ID, purchaseToken, purchase, lineItem, state, access, reason, renewal)
	observation := core.PurchaseObservation{
		ID:            core.ObservationID("gpl-" + hex.EncodeToString(identity[:16])),
		ApplicationID: application.ID, Store: application.Store,
		ProductID: core.ProviderProductID(lineItem.ProductID), ProductKind: core.ProductKindSubscription,
		State: state, ProviderState: purchase.SubscriptionState,
		Access: access, AccessReason: reason, Ownership: core.OwnershipPurchased, Quantity: 1,
		OccurredAt: purchase.StartTime, ObservedAt: adapter.clock().UTC(),
		EffectivePeriod: core.EffectivePeriod{StartsAt: purchase.StartTime, EndsAt: timePointer(lineItem.ExpiryTime)},
		Renewal:         renewal, References: references,
	}
	if state == core.LifecyclePending || state == core.LifecycleRevoked {
		observation.OccurredAt = time.Time{}
		observation.EffectivePeriod = core.EffectivePeriod{}
	}
	if err := observation.Validate(); err != nil {
		return nil, invalid("normalize", err)
	}
	return []core.PurchaseObservation{observation}, nil
}

// googleReferences creates protected transaction, lineage, query, and customer references.
func googleReferences(purchaseToken, orderID, linkedToken, accountID string) ([]core.StoreReference, error) {
	references := make([]core.StoreReference, 0, 5)
	for _, candidate := range []struct {
		role  core.ReferenceRole
		kind  string
		value string
	}{
		{role: core.ReferenceTransaction, kind: "order_id", value: orderID},
		{role: core.ReferenceLineage, kind: "purchase_token", value: purchaseToken},
		{role: core.ReferenceLinkedLineage, kind: "purchase_token", value: linkedToken},
		{role: core.ReferenceQuery, kind: "purchase_token", value: purchaseToken},
		{role: core.ReferenceCustomerBinding, kind: "obfuscated_external_account_id", value: accountID},
	} {
		if candidate.value == "" {
			continue
		}
		reference, err := core.NewStoreReference(candidate.role, candidate.kind, candidate.value)
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	return references, nil
}

// acknowledgementActions creates one safe post-commit action for a completed unacknowledged purchase.
func acknowledgementActions(
	observation core.PurchaseObservation,
	providerState string,
	acknowledgementState string,
) ([]stores.PostCommitAction, error) {
	eligible := false
	switch observation.ProductKind {
	case core.ProductKindNonConsumable:
		eligible = providerState == productStatePurchased
	case core.ProductKindSubscription:
		switch providerState {
		case subscriptionStateActive,
			subscriptionStatePaused,
			subscriptionStateGrace,
			subscriptionStateOnHold,
			subscriptionStateCanceled:
			eligible = true
		}
	default:
		return nil, fmt.Errorf("unsupported Google Play acknowledgement product kind %q", observation.ProductKind)
	}
	if !eligible || acknowledgementState == acknowledgementStateAcknowledged {
		return nil, nil
	}
	if acknowledgementState != acknowledgementStatePending {
		return nil, fmt.Errorf("completed Google Play purchase has acknowledgement state %q", acknowledgementState)
	}
	queryReferences := observation.ReferencesFor(core.ReferenceQuery)
	if len(queryReferences) == 0 {
		return nil, errors.New("google play acknowledgement requires a query reference")
	}
	return []stores.PostCommitAction{{
		Kind:            acknowledgeActionKind,
		ProductID:       observation.ProductID,
		ProductKind:     observation.ProductKind,
		QueryReferences: append([]core.StoreReference(nil), queryReferences...),
	}}, nil
}

// normalizeProductState maps ProductPurchaseV2 states into shared access semantics.
func normalizeProductState(value string) (core.LifecycleState, core.AccessStatus, core.AccessReason) {
	switch value {
	case productStatePurchased:
		return core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid
	case productStateCancelled:
		return core.LifecycleRefunded, core.AccessDenied, core.AccessReasonRefunded
	case productStatePending:
		return core.LifecyclePending, core.AccessUnresolved, core.AccessReasonPendingPayment
	default:
		return core.LifecycleUnresolved, core.AccessUnresolved, core.AccessReasonUnresolved
	}
}

// normalizeSubscriptionState maps SubscriptionPurchaseV2 states into shared access semantics.
func normalizeSubscriptionState(value string) (core.LifecycleState, core.AccessStatus, core.AccessReason) {
	switch value {
	case subscriptionStatePending:
		return core.LifecyclePending, core.AccessUnresolved, core.AccessReasonPendingPayment
	case subscriptionStateActive:
		return core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid
	case subscriptionStatePaused:
		return core.LifecyclePaused, core.AccessDenied, core.AccessReasonPaused
	case subscriptionStateGrace:
		return core.LifecycleGracePeriod, core.AccessAllowed, core.AccessReasonGracePeriod
	case subscriptionStateOnHold:
		return core.LifecycleOnHold, core.AccessDenied, core.AccessReasonBillingIssue
	case subscriptionStateCanceled:
		return core.LifecycleCanceled, core.AccessAllowed, core.AccessReasonCanceledAtPeriodEnd
	case subscriptionStateExpired:
		return core.LifecycleExpired, core.AccessDenied, core.AccessReasonExpired
	case subscriptionStatePendingCanceled:
		return core.LifecycleRevoked, core.AccessDenied, core.AccessReasonRevoked
	default:
		return core.LifecycleUnresolved, core.AccessUnresolved, core.AccessReasonUnresolved
	}
}

// subscriptionRenewal derives shared renewal semantics from the current Google plan type.
func subscriptionRenewal(lineItem subscriptionLineItem) (core.Renewal, error) {
	if lineItem.AutoRenewingPlan != nil && lineItem.PrepaidPlan != nil {
		return core.Renewal{}, errors.New("google play subscription has multiple plan types")
	}
	if lineItem.AutoRenewingPlan != nil {
		status := core.RenewalDisabled
		if lineItem.AutoRenewingPlan.AutoRenewEnabled {
			status = core.RenewalEnabled
		}
		return core.Renewal{Mode: core.RenewalAuto, Status: status}, nil
	}
	if lineItem.PrepaidPlan != nil {
		return core.Renewal{Mode: core.RenewalPrepaid, Status: core.RenewalNotApplicable}, nil
	}
	return core.Renewal{Mode: core.RenewalUnknown, Status: core.RenewalStatusUnknown}, nil
}

// validateEnvironment prevents test purchases from crossing configured application environments.
func validateEnvironment(environment core.Environment, testPurchase bool) error {
	switch environment {
	case core.EnvironmentProduction:
		if testPurchase {
			return errors.New("google play test purchase cannot use a production application")
		}
	case core.EnvironmentSandbox, core.EnvironmentTest:
		if !testPurchase {
			return errors.New("google play live purchase cannot use a test application")
		}
	default:
		return fmt.Errorf("unsupported Google Play environment %q", environment)
	}
	return nil
}

// productObservationIdentity hashes every authoritative product field that changes a logical snapshot.
func productObservationIdentity(
	applicationID core.ApplicationID,
	purchaseToken string,
	purchase productPurchase,
	lineItem productLineItem,
	state core.LifecycleState,
	access core.AccessStatus,
	reason core.AccessReason,
	quantity uint32,
) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join([]string{
		string(applicationID), purchaseToken, lineItem.ProductID, purchase.OrderID,
		purchase.ObfuscatedExternalAccountID, purchase.PurchaseCompletionTime.UTC().Format(time.RFC3339Nano),
		purchase.PurchaseStateContext.PurchaseState, purchase.AcknowledgementState,
		lineItem.ProductOfferDetails.PurchaseOptionID, lineItem.ProductOfferDetails.ConsumptionState,
		strconv.FormatUint(uint64(quantity), 10), strconv.FormatUint(uint64(lineItem.ProductOfferDetails.RefundableQuantity), 10),
		string(state), string(access), string(reason),
	}, "\x00")))
}

// subscriptionObservationIdentity hashes every authoritative subscription field that changes a logical snapshot.
func subscriptionObservationIdentity(
	applicationID core.ApplicationID,
	purchaseToken string,
	purchase subscriptionPurchase,
	lineItem subscriptionLineItem,
	state core.LifecycleState,
	access core.AccessStatus,
	reason core.AccessReason,
	renewal core.Renewal,
) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join([]string{
		string(applicationID), purchaseToken, purchase.LinkedPurchaseToken, lineItem.ProductID,
		purchase.LatestOrderID, lineItem.LatestSuccessfulOrderID,
		purchase.ExternalAccountIdentifiers.ObfuscatedExternalAccountID,
		purchase.StartTime.UTC().Format(time.RFC3339Nano), lineItem.ExpiryTime.UTC().Format(time.RFC3339Nano),
		purchase.SubscriptionState, purchase.AcknowledgementState,
		string(state), string(access), string(reason), string(renewal.Mode), string(renewal.Status),
	}, "\x00")))
}

// parseEvidence decodes the submitted versioned Google Play purchase-token envelope.
func parseEvidence(evidence stores.Evidence) (clientEvidence, error) {
	if evidence.ContentType != EvidenceContentType {
		return clientEvidence{}, errors.New("unsupported Google Play evidence content type")
	}
	var result clientEvidence
	if err := decodeStrict(evidence.Bytes(), &result); err != nil {
		return clientEvidence{}, err
	}
	if strings.TrimSpace(result.PurchaseToken) == "" {
		return clientEvidence{}, errors.New("google play purchase token is required")
	}
	if result.ProductKind != core.ProductKindSubscription && result.ProductKind != core.ProductKindNonConsumable {
		return clientEvidence{}, errors.New("unsupported Google Play product kind")
	}
	return result, nil
}

// parsePrivateKey parses a service-account PKCS#8 RSA private key.
func parsePrivateKey(encoded string) (*rsa.PrivateKey, error) {
	block, remainder := pem.Decode([]byte(encoded))
	if block == nil || block.Type != "PRIVATE KEY" || strings.TrimSpace(string(remainder)) != "" {
		return nil, errors.New("google play private key must be one PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("google play private key is invalid PKCS#8")
	}
	privateKey, ok := parsed.(*rsa.PrivateKey)
	if !ok || privateKey.N.BitLen() < 2048 {
		return nil, errors.New("google play private key must be an RSA key of at least 2048 bits")
	}
	if err := privateKey.Validate(); err != nil {
		return nil, errors.New("google play private key is invalid")
	}
	return privateKey, nil
}

// statusFailure maps bounded HTTP status information into stable provider categories.
func (adapter *Adapter) statusFailure(operation string, status int, header http.Header) *stores.Failure {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailureUnauthorized, 0, nil)
	case status == http.StatusNotFound || status == http.StatusGone:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailureNotFound, 0, nil)
	case status == http.StatusConflict:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailureConflict, 0, nil)
	case status == http.StatusTooManyRequests:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailureRateLimited, retryAfter(header, adapter.clock()), nil)
	case status >= 500:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailureTemporary, 0, nil)
	default:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailurePermanent, 0, nil)
	}
}

// do executes one bounded provider call and returns a cloned response header.
func (adapter *Adapter) do(request *http.Request) ([]byte, int, http.Header, error) {
	response, err := adapter.client.Do(request)
	if err != nil {
		return nil, 0, nil, stores.NewFailure(adapter.Provider(), "http", stores.FailureTemporary, 0, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumProviderResponse+1))
	if err != nil {
		return nil, response.StatusCode, response.Header.Clone(), stores.NewFailure(adapter.Provider(), "http", stores.FailureTemporary, 0, err)
	}
	if int64(len(body)) > maximumProviderResponse {
		return nil, response.StatusCode, response.Header.Clone(), stores.NewFailure(adapter.Provider(), "http", stores.FailurePermanent, 0, nil)
	}
	return body, response.StatusCode, response.Header.Clone(), nil
}

// retryAfter parses either seconds or an HTTP date without exposing provider response data.
func retryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}

// timePointer returns nil for absent provider timestamps.
func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	result := value.UTC()
	return &result
}

// decodeStrict decodes one JSON value and rejects unknown or trailing fields.
func decodeStrict(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

// invalid constructs a redacted non-retryable Google Play evidence failure.
func invalid(operation string, cause error) error {
	return stores.NewFailure(core.ProviderGooglePlay, operation, stores.FailureInvalidEvidence, 0, cause)
}

// zero overwrites temporary credential payload bytes after parsing.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
