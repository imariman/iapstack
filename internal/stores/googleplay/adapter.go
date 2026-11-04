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

// subscriptionLineItem contains one Google Play subscription product, its plan, and any replacement details.
type subscriptionLineItem struct {
	ProductID               string            `json:"productId"`
	ExpiryTime              time.Time         `json:"expiryTime"`
	LatestSuccessfulOrderID string            `json:"latestSuccessfulOrderId"`
	AutoRenewingPlan        *autoRenewingPlan `json:"autoRenewingPlan"`
	PrepaidPlan             *struct{}         `json:"prepaidPlan"`
	DeferredItemReplacement *itemProduct      `json:"deferredItemReplacement"`
	ItemReplacement         *itemProduct      `json:"itemReplacement"`
}

// itemProduct names the product in a deferred item replacement or in the item an item replaced.
type itemProduct struct {
	ProductID string `json:"productId"`
}

// subscriptionItems is the line-item selection for one subscription purchase.
// current grants access now. pending is the item a deferred replacement switches to once it takes
// effect. replaced is the item a completed deferred replacement ended.
type subscriptionItems struct {
	current  subscriptionLineItem
	pending  *subscriptionLineItem
	replaced *subscriptionLineItem
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
// products lists every product the purchase names, including replaced and pending replacement items.
// itemReplaced is the expired snapshot of the product a one-item purchase names in itemReplacement. Its
// period is unknown, so it is recorded only when the request names that product for this purchase token.
type queryResult struct {
	artifact          stores.Evidence
	observations      []core.PurchaseObservation
	postCommitActions []stores.PostCommitAction
	products          []core.ProviderProductID
	itemReplaced      *core.PurchaseObservation
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
	verificationResult := result.verificationResult(adapter.clock().UTC(), request.ClaimedProducts)
	validationRequest := request
	validationRequest.ClaimedProducts = verificationResult.ProductsForClaim(request.ClaimedProducts)
	if err := verificationResult.ValidateForVerification(validationRequest); err != nil {
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
	purchaseToken := queryPurchaseToken(request.QueryReferences)
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
	verificationResult := result.verificationResult(adapter.clock().UTC(), request.ExpectedProducts)
	validationRequest := request
	validationRequest.ExpectedProducts = verificationResult.ProductsForClaim(request.ExpectedProducts)
	if err := verificationResult.ValidateForReconciliation(validationRequest); err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	return verificationResult, nil
}

// LinkedPurchase reads one subscription purchase token that no customer has verified yet.
// It runs the same subscriptionsv2 query and normalization as Verify and Reconcile, so a purchase they would
// reject is rejected here too. It returns the product of the item that grants access now, the query reference
// of linkedPurchaseToken when the purchase replaces another one, and the obfuscated account binding.
// Google documents linkedPurchaseToken as the way to find the account of an upgrade, downgrade, or resubscribe.
func (adapter *Adapter) LinkedPurchase(
	ctx context.Context,
	request stores.LinkedPurchaseRequest,
) (stores.LinkedPurchase, error) {
	if err := request.Validate(); err != nil {
		return stores.LinkedPurchase{}, invalid("linked_purchase", err)
	}
	if request.Application.Store.Provider != adapter.Provider() || request.ProductKind != core.ProductKindSubscription {
		return stores.LinkedPurchase{}, invalid("linked_purchase", errors.New("google play linked purchases are subscriptions"))
	}
	purchaseToken := queryPurchaseToken(request.QueryReferences)
	if purchaseToken == "" {
		return stores.LinkedPurchase{}, invalid("linked_purchase", errors.New("google play linked purchase requires a purchase token"))
	}
	configuration, err := adapter.configuration(ctx, request.Application)
	if err != nil {
		return stores.LinkedPurchase{}, err
	}
	result, err := adapter.query(ctx, request.Application, configuration, purchaseToken, core.ProductKindSubscription)
	if err != nil {
		return stores.LinkedPurchase{}, err
	}
	current := result.observations[0]
	linked := stores.LinkedPurchase{
		ProductID:        current.ProductID,
		CustomerBindings: current.ReferencesFor(core.ReferenceCustomerBinding),
	}
	for _, reference := range current.ReferencesFor(core.ReferenceLinkedLineage) {
		if reference.Kind != "purchase_token" {
			continue
		}
		query, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", reference.Value())
		if err != nil {
			return stores.LinkedPurchase{}, invalid("linked_purchase", err)
		}
		linked.LinkedReferences = append(linked.LinkedReferences, query)
	}
	if err := linked.Validate(); err != nil {
		return stores.LinkedPurchase{}, invalid("linked_purchase", err)
	}
	return linked, nil
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
		itemReplaced, err := itemReplacementObservation(application, purchaseToken, purchase, observations[0])
		if err != nil {
			return queryResult{}, err
		}
		return queryResult{
			artifact: artifact, observations: observations, postCommitActions: actions,
			products: subscriptionProducts(purchase), itemReplaced: itemReplaced,
		}, nil
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
	now := adapter.clock().UTC()
	items, err := selectSubscriptionItems(purchase.LineItems, now)
	if err != nil {
		return nil, invalid("normalize", err)
	}
	if err := validateEnvironment(application.Store.Environment, purchase.TestPurchase != nil); err != nil {
		return nil, invalid("normalize", err)
	}
	lineItem := items.current
	accountID := purchase.ExternalAccountIdentifiers.ObfuscatedExternalAccountID
	if lineItem.ProductID == "" || accountID == "" {
		return nil, invalid("normalize", errors.New("google play subscription identity or customer binding is missing"))
	}
	state, access, reason := normalizeSubscriptionState(purchase.SubscriptionState)
	renewal, err := subscriptionRenewal(lineItem)
	if err != nil {
		return nil, invalid("normalize", err)
	}
	if items.pending != nil {
		renewal, err = replacementRenewal(*items.pending)
		if err != nil {
			return nil, invalid("normalize", err)
		}
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
		OccurredAt: purchase.StartTime, ObservedAt: now,
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
	observations := []core.PurchaseObservation{observation}
	if items.replaced != nil {
		replaced, err := replacedItemObservation(application, purchaseToken, purchase, *items.replaced, observation)
		if err != nil {
			return nil, err
		}
		observations = append(observations, replaced)
	}
	return observations, nil
}

// replacedItemObservation records the item a completed deferred replacement ended as expired.
// Google keeps that item on the new purchase token with its past expiry. Its own expired snapshot
// means an earlier allowed snapshot of the old product on this token cannot keep granting the
// entitlement after the replacement took effect. The snapshot is observed one microsecond before
// the current item because both rows share one transaction timestamp, and a lookup by purchase
// token must resolve to the current product. Its identity and references use only the ended item
// and the purchase fields that never change, so renewals of the current item reuse one snapshot.
func replacedItemObservation(
	application core.Application,
	purchaseToken string,
	purchase subscriptionPurchase,
	item subscriptionLineItem,
	current core.PurchaseObservation,
) (core.PurchaseObservation, error) {
	renewal, err := subscriptionRenewal(item)
	if err != nil {
		return core.PurchaseObservation{}, invalid("normalize", err)
	}
	accountID := purchase.ExternalAccountIdentifiers.ObfuscatedExternalAccountID
	references, err := googleReferences(purchaseToken, item.LatestSuccessfulOrderID, purchase.LinkedPurchaseToken, accountID)
	if err != nil {
		return core.PurchaseObservation{}, invalid("normalize", err)
	}
	state, access, reason := normalizeSubscriptionState(subscriptionStateExpired)
	identity := replacedItemObservationIdentity(application.ID, purchaseToken, purchase, item, state, access, reason, renewal)
	period := core.EffectivePeriod{StartsAt: purchase.StartTime}
	if !purchase.StartTime.IsZero() && item.ExpiryTime.After(purchase.StartTime) {
		period.EndsAt = timePointer(item.ExpiryTime)
	}
	observation := core.PurchaseObservation{
		ID:            core.ObservationID("gpl-" + hex.EncodeToString(identity[:16])),
		ApplicationID: application.ID, Store: application.Store,
		ProductID: core.ProviderProductID(item.ProductID), ProductKind: core.ProductKindSubscription,
		State: state, ProviderState: subscriptionStateExpired,
		Access: access, AccessReason: reason, Ownership: core.OwnershipPurchased, Quantity: 1,
		OccurredAt: purchase.StartTime, ObservedAt: current.ObservedAt.Add(-time.Microsecond),
		EffectivePeriod: period, Renewal: renewal, References: references,
	}
	if err := observation.Validate(); err != nil {
		return core.PurchaseObservation{}, invalid("normalize", err)
	}
	return observation, nil
}

// itemReplacementObservation records the product a one-item purchase names in itemReplacement as expired.
// Google documents two items on a token after a deferred change, but a one-item response that names the
// replaced product is accepted for a token stored with that product. Without an expired snapshot, the
// stored allowed snapshot of the replaced product would keep its entitlement source after its end. Google
// reports no period for that product here, so the snapshot has no end, and it is added to a result only
// when the request names the replaced product.
func itemReplacementObservation(
	application core.Application,
	purchaseToken string,
	purchase subscriptionPurchase,
	current core.PurchaseObservation,
) (*core.PurchaseObservation, error) {
	if len(purchase.LineItems) != 1 || purchase.LineItems[0].ItemReplacement == nil {
		return nil, nil
	}
	productID := purchase.LineItems[0].ItemReplacement.ProductID
	if productID == "" || productID == purchase.LineItems[0].ProductID {
		return nil, nil
	}
	observation, err := replacedItemObservation(
		application, purchaseToken, purchase, subscriptionLineItem{ProductID: productID}, current,
	)
	if err != nil {
		return nil, err
	}
	return &observation, nil
}

// selectSubscriptionItems selects the line item that grants access now and any deferred replacement item.
// ReplacementMode.DEFERRED issues a purchase with two items. Until the change takes effect, the existing item
// keeps the current period and its deferredItemReplacement names the new item, which has no expiry yet. After
// the change, the replaced item keeps a past expiry without auto-renewal and the new item carries the current
// period. Any other purchase with more than one item, such as a subscription with add-ons, is not supported.
// The replaced item must already have expired at now, and an itemReplacement on the new item must name it.
func selectSubscriptionItems(items []subscriptionLineItem, now time.Time) (subscriptionItems, error) {
	if len(items) == 1 {
		return subscriptionItems{current: items[0]}, nil
	}
	if len(items) != 2 {
		return subscriptionItems{}, errors.New("google play subscription requires one line item or one deferred replacement")
	}
	for index := range items {
		current, next := items[index], items[1-index]
		if current.DeferredItemReplacement == nil {
			continue
		}
		if next.ProductID == "" || next.ProductID == current.ProductID ||
			current.DeferredItemReplacement.ProductID != next.ProductID || next.DeferredItemReplacement != nil ||
			current.ExpiryTime.IsZero() || !next.ExpiryTime.IsZero() {
			return subscriptionItems{}, errors.New("google play deferred replacement is inconsistent")
		}
		return subscriptionItems{current: current, pending: &next}, nil
	}
	replaced, current := items[0], items[1]
	if replaced.ExpiryTime.After(current.ExpiryTime) {
		replaced, current = current, replaced
	}
	if replaced.ProductID == "" || replaced.ProductID == current.ProductID || replaced.ExpiryTime.IsZero() ||
		!replaced.ExpiryTime.Before(current.ExpiryTime) || replaced.ExpiryTime.After(now) ||
		(replaced.AutoRenewingPlan != nil && replaced.AutoRenewingPlan.AutoRenewEnabled) ||
		(current.ItemReplacement != nil && current.ItemReplacement.ProductID != replaced.ProductID) {
		return subscriptionItems{}, errors.New("google play subscription has more than one current line item")
	}
	return subscriptionItems{current: current, replaced: &replaced}, nil
}

// replacementRenewal reports how a subscription continues once its pending deferred replacement takes effect.
// Google turns auto-renewal off on the replaced item, so the new item's plan decides whether the subscription
// renews, and its product is the next product. A new item without plan details leaves the renewal unknown.
func replacementRenewal(pending subscriptionLineItem) (core.Renewal, error) {
	renewal, err := subscriptionRenewal(pending)
	if err != nil {
		return core.Renewal{}, err
	}
	if renewal.Mode == core.RenewalAuto && renewal.Status == core.RenewalEnabled {
		renewal.NextProductID = core.ProviderProductID(pending.ProductID)
	}
	return renewal, nil
}

// subscriptionProducts returns each product one purchase names: its items, a pending replacement, and a replaced item.
func subscriptionProducts(purchase subscriptionPurchase) []core.ProviderProductID {
	products := make([]core.ProviderProductID, 0, 2*len(purchase.LineItems))
	add := func(productID string) {
		if productID == "" {
			return
		}
		for _, existing := range products {
			if string(existing) == productID {
				return
			}
		}
		products = append(products, core.ProviderProductID(productID))
	}
	for _, item := range purchase.LineItems {
		add(item.ProductID)
		if item.DeferredItemReplacement != nil {
			add(item.DeferredItemReplacement.ProductID)
		}
		if item.ItemReplacement != nil {
			add(item.ItemReplacement.ProductID)
		}
	}
	return products
}

// verificationResult builds the provider result for one request.
// A purchase that names more than one product reports them all, so a claim on any of them is checked against
// the observed products. The itemReplacement snapshot is added only when the request names that product.
func (result queryResult) verificationResult(
	verifiedAt time.Time,
	requested []core.ProviderProductID,
) stores.VerificationResult {
	observations := result.observations
	if result.itemReplaced != nil && len(requested) == 1 && requested[0] == result.itemReplaced.ProductID {
		observations = append(append([]core.PurchaseObservation(nil), observations...), *result.itemReplaced)
	}
	verificationResult := stores.VerificationResult{
		VerifiedAt:        verifiedAt,
		Artifacts:         []stores.VerifiedArtifact{{Kind: "google_play_server_response", Evidence: result.artifact}},
		Observations:      observations,
		PostCommitActions: result.postCommitActions,
	}
	if len(result.products) > 1 {
		verificationResult.NamedProducts = append([]core.ProviderProductID(nil), result.products...)
	}
	return verificationResult
}

// queryPurchaseToken returns the first purchase token among query references.
func queryPurchaseToken(references []core.StoreReference) string {
	for _, reference := range references {
		if reference.Kind == "purchase_token" {
			return reference.Value()
		}
	}
	return ""
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
		purchase.PurchaseStateContext.PurchaseState,
		lineItem.ProductOfferDetails.PurchaseOptionID, lineItem.ProductOfferDetails.ConsumptionState,
		strconv.FormatUint(uint64(quantity), 10), strconv.FormatUint(uint64(lineItem.ProductOfferDetails.RefundableQuantity), 10),
		string(state), string(access), string(reason),
	}, "\x00")))
}

// subscriptionObservationIdentity hashes every authoritative subscription field that changes a logical snapshot.
// lineItem is the current item. A pending replacement product is encoded only when present, so one-item
// purchases keep the identity they had before deferred replacements were supported.
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
	fields := []string{
		string(applicationID), purchaseToken, purchase.LinkedPurchaseToken, lineItem.ProductID,
		purchase.LatestOrderID, lineItem.LatestSuccessfulOrderID,
		purchase.ExternalAccountIdentifiers.ObfuscatedExternalAccountID,
		purchase.StartTime.UTC().Format(time.RFC3339Nano), lineItem.ExpiryTime.UTC().Format(time.RFC3339Nano),
		purchase.SubscriptionState,
		string(state), string(access), string(reason), string(renewal.Mode), string(renewal.Status),
	}
	if renewal.NextProductID != "" {
		fields = append(fields, "next_product", string(renewal.NextProductID))
	}
	return sha256.Sum256([]byte(strings.Join(fields, "\x00")))
}

// replacedItemObservationIdentity hashes the fields of one ended item of a replacement purchase.
// It leaves out the purchase-level order and state, which follow the current item, so the ended item keeps
// one snapshot while the current item renews or changes state.
func replacedItemObservationIdentity(
	applicationID core.ApplicationID,
	purchaseToken string,
	purchase subscriptionPurchase,
	item subscriptionLineItem,
	state core.LifecycleState,
	access core.AccessStatus,
	reason core.AccessReason,
	renewal core.Renewal,
) [sha256.Size]byte {
	expiry := ""
	if !item.ExpiryTime.IsZero() {
		expiry = item.ExpiryTime.UTC().Format(time.RFC3339Nano)
	}
	return sha256.Sum256([]byte(strings.Join([]string{
		"replaced_item", string(applicationID), purchaseToken, purchase.LinkedPurchaseToken, item.ProductID,
		item.LatestSuccessfulOrderID, purchase.ExternalAccountIdentifiers.ObfuscatedExternalAccountID,
		purchase.StartTime.UTC().Format(time.RFC3339Nano), expiry,
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
