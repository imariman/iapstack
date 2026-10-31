// Package huawei implements Huawei AppGallery purchase verification and reconciliation.
package huawei

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// CredentialKind identifies the v1 Huawei server API credential package.
	CredentialKind stores.CredentialKind = "huawei_server_api" // #nosec G101 -- This is a public credential type discriminator, not credential material.
	// CredentialContentType identifies the Huawei credential JSON representation.
	CredentialContentType = "application/vnd.iapstack.huawei-credentials+json"
	// CredentialSchemaVersion identifies the initial server API credential shape.
	CredentialSchemaVersion = 1
	// EvidenceContentType identifies the Huawei client purchase evidence representation.
	EvidenceContentType = stores.HuaweiEvidenceContentType
	// maximumProviderResponse bounds Huawei token and purchase response bodies.
	maximumProviderResponse int64 = 2 << 20
	// defaultTokenURL is Huawei's documented OAuth token service.
	defaultTokenURL = "https://oauth-login.cloud.huawei.com/oauth2/v3/token" // #nosec G101 -- This is Huawei's public OAuth endpoint, not credential material.
	// tokenExpiryMargin refreshes a cached OAuth token shortly before Huawei's expires_in.
	tokenExpiryMargin = 60 * time.Second
)

// Adapter verifies Huawei evidence using application-scoped credentials and authoritative server APIs.
type Adapter struct {
	credentials          stores.CredentialSource
	client               *http.Client
	clock                func() time.Time
	allowPrivateNetworks bool
	tokenMu              sync.Mutex
	tokens               map[string]cachedAccessToken
}

// cachedAccessToken is one app-level OAuth token retained until shortly before expires_in.
type cachedAccessToken struct {
	token     string
	expiresAt time.Time
}

// credentialPayload is the versioned private Huawei application configuration.
type credentialPayload struct {
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
	PublicKey       string `json:"public_key"`
	TokenURL        string `json:"token_url,omitempty"`
	OrderURL        string `json:"order_url"`
	SubscriptionURL string `json:"subscription_url"`
}

// clientEvidence is the signed Huawei purchase returned by the device SDK.
type clientEvidence struct {
	PurchaseData       string           `json:"purchase_data"`
	Signature          string           `json:"signature"`
	SignatureAlgorithm string           `json:"signature_algorithm,omitempty"`
	ProductKind        core.ProductKind `json:"product_kind"`
}

// purchaseData contains the Huawei fields required for normalization and consistency checks.
type purchaseData struct {
	ApplicationID       huaweiText `json:"applicationId"`
	PackageName         string     `json:"packageName"`
	ProductID           string     `json:"productId"`
	SubscriptionID      string     `json:"subscriptionId,omitempty"`
	OrderID             string     `json:"orderId"`
	PurchaseToken       string     `json:"purchaseToken"`
	PurchaseState       int        `json:"purchaseState"`
	PurchaseTime        int64      `json:"purchaseTime"`
	ExpirationDate      int64      `json:"expirationDate"`
	GraceExpirationTime int64      `json:"graceExpirationTime"`
	CancellationTime    int64      `json:"cancellationTime"`
	CancelTime          int64      `json:"cancelTime"`
	RenewStatus         int        `json:"renewStatus"`
	RetryFlag           int        `json:"retryFlag"`
	SubIsValid          bool       `json:"subIsvalid"`
	DeveloperPayload    string     `json:"developerPayload"`
	Quantity            uint32     `json:"quantity"`
	Currency            string     `json:"currency"`
	Price               int64      `json:"price"`
}

// providerResponse accepts the documented legacy names used by Huawei order and subscription services.
type providerResponse struct {
	ResponseCode                  string `json:"responseCode"`
	ResponseMessage               string `json:"responseMessage"`
	PurchaseTokenData             string `json:"purchaseTokenData"`
	DataSignature                 string `json:"dataSignature"`
	InAppPurchaseData             string `json:"inappPurchaseData"`
	InAppPurchaseDataSignature    string `json:"inappPurchaseDataSignature"`
	SubscriptionPurchaseData      string `json:"subscriptionPurchaseData"`
	SubscriptionPurchaseSignature string `json:"subscriptionPurchaseSignature"`
	SignatureAlgorithm            string `json:"signatureAlgorithm"`
}

// providerQuery is the purchase-token body sent to Huawei's Order or Subscription service.
type providerQuery struct {
	PurchaseToken  string `json:"purchaseToken"`
	ProductID      string `json:"productId,omitempty"`
	SubscriptionID string `json:"subscriptionId,omitempty"`
}

// huaweiText stores a Huawei JSON string. InAppPurchaseData encodes applicationId as an integer.
type huaweiText string

// tokenResponse contains the OAuth bearer fields needed by provider calls.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// signedData pairs authoritative Huawei JSON with its detached signature and algorithm.
type signedData struct {
	data      string
	signature string
	algorithm string
}

// New constructs a bounded Huawei AppGallery server adapter under the configured address policy.
func New(
	credentials stores.CredentialSource,
	timeout time.Duration,
	allowPrivateNetworks bool,
) (*Adapter, error) {
	if credentials == nil {
		return nil, errors.New("huawei credential source is required")
	}
	if timeout <= 0 {
		return nil, errors.New("huawei provider timeout must be positive")
	}
	return &Adapter{
		credentials:          credentials,
		client:               newProviderClient(timeout, allowPrivateNetworks),
		clock:                time.Now,
		allowPrivateNetworks: allowPrivateNetworks,
		tokens:               make(map[string]cachedAccessToken),
	}, nil
}

// Provider identifies Huawei AppGallery as the implemented store.
func (adapter *Adapter) Provider() core.Provider {
	return core.ProviderHuaweiAppGallery
}

// Verify validates submitted evidence, queries Huawei, verifies its signature, and normalizes state.
func (adapter *Adapter) Verify(
	ctx context.Context,
	request stores.VerificationRequest,
) (stores.VerificationResult, error) {
	if err := request.Validate(); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	evidence, submitted, err := parseEvidence(request.Evidence)
	if err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	if err := validateSubmittedScope(request, submitted); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	configuration, publicKey, err := adapter.configuration(ctx, request.Application)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	if err := verifySignature(publicKey, evidence.PurchaseData, evidence.Signature, evidence.SignatureAlgorithm); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	authoritative, rawArtifact, err := adapter.query(ctx, configuration, submitted, evidence.ProductKind)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	if err := verifySignature(publicKey, authoritative.data, authoritative.signature, authoritative.algorithm); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	var current purchaseData
	if err := json.Unmarshal([]byte(authoritative.data), &current); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	if err := comparePurchases(submitted, current); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	return adapter.result(request.Application, evidence.ProductKind, current, rawArtifact)
}

// Reconcile queries current Huawei state using one opaque purchase-token reference.
func (adapter *Adapter) Reconcile(
	ctx context.Context,
	request stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	if err := request.Validate(); err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	token := ""
	subscriptionID := ""
	for _, reference := range request.QueryReferences {
		switch reference.Kind {
		case "purchase_token":
			if token == "" {
				token = reference.Value()
			}
		case "subscription_id":
			if subscriptionID == "" {
				subscriptionID = reference.Value()
			}
		}
	}
	if token == "" || len(request.ExpectedProducts) != 1 {
		return stores.VerificationResult{}, invalid("reconcile", errors.New("huawei reconciliation requires one product and purchase token"))
	}
	if request.ExpectedProductKind == core.ProductKindSubscription && subscriptionID == "" {
		return stores.VerificationResult{}, invalid("reconcile", errors.New("huawei subscription reconciliation requires subscriptionId"))
	}
	configuration, publicKey, err := adapter.configuration(ctx, request.Application)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	query := purchaseData{
		ApplicationID:  huaweiText(request.Application.Store.ID),
		ProductID:      string(request.ExpectedProducts[0]),
		SubscriptionID: subscriptionID,
		PurchaseToken:  token,
	}
	authoritative, rawArtifact, err := adapter.query(ctx, configuration, query, request.ExpectedProductKind)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	if err := verifySignature(publicKey, authoritative.data, authoritative.signature, authoritative.algorithm); err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	var current purchaseData
	if err := json.Unmarshal([]byte(authoritative.data), &current); err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	if current.PurchaseToken != token || current.ProductID != query.ProductID || current.ApplicationID != query.ApplicationID ||
		(query.SubscriptionID != "" && current.SubscriptionID != query.SubscriptionID) {
		return stores.VerificationResult{}, invalid("reconcile", errors.New("authoritative Huawei scope mismatch"))
	}
	return adapter.result(request.Application, request.ExpectedProductKind, current, rawArtifact)
}

// configuration opens and validates one application-scoped Huawei credential payload and RSA key.
func (adapter *Adapter) configuration(
	ctx context.Context,
	application core.Application,
) (credentialPayload, *rsa.PublicKey, error) {
	credential, err := adapter.credentials.Credential(ctx, application, CredentialKind)
	if err != nil {
		return credentialPayload{}, nil, stores.NewFailure(adapter.Provider(), "credentials", stores.FailureUnauthorized, 0, err)
	}
	if credential.ContentType != CredentialContentType || credential.SchemaVersion != CredentialSchemaVersion {
		return credentialPayload{}, nil, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	payload := credential.Bytes()
	defer zero(payload)
	var configuration credentialPayload
	if err := decodeStrict(payload, &configuration); err != nil {
		return credentialPayload{}, nil, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, err)
	}
	if configuration.TokenURL == "" {
		configuration.TokenURL = defaultTokenURL
	}
	if configuration.ClientID == "" || configuration.ClientSecret == "" || configuration.PublicKey == "" ||
		configuration.OrderURL == "" || configuration.SubscriptionURL == "" {
		return credentialPayload{}, nil, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	for _, endpoint := range []string{configuration.TokenURL, configuration.OrderURL, configuration.SubscriptionURL} {
		if err := validateHTTPS(endpoint, adapter.allowPrivateNetworks); err != nil {
			return credentialPayload{}, nil, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, err)
		}
	}
	publicKey, err := parsePublicKey(configuration.PublicKey)
	if err != nil {
		return credentialPayload{}, nil, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, err)
	}
	return configuration, publicKey, nil
}

// query obtains an access token and requests authoritative purchase state.
func (adapter *Adapter) query(
	ctx context.Context,
	configuration credentialPayload,
	purchase purchaseData,
	productKind core.ProductKind,
) (signedData, stores.Evidence, error) {
	if productKind == core.ProductKindSubscription && strings.TrimSpace(purchase.SubscriptionID) == "" {
		return signedData{}, stores.Evidence{}, invalid("query", errors.New("huawei subscription verification requires subscriptionId"))
	}
	endpoint := configuration.OrderURL
	bodyQuery := providerQuery{PurchaseToken: purchase.PurchaseToken, ProductID: purchase.ProductID}
	if productKind == core.ProductKindSubscription {
		endpoint = configuration.SubscriptionURL
		bodyQuery = providerQuery{PurchaseToken: purchase.PurchaseToken, SubscriptionID: purchase.SubscriptionID}
	}
	body, err := json.Marshal(bodyQuery)
	if err != nil {
		return signedData{}, stores.Evidence{}, invalid("query", err)
	}
	var responseBody []byte
	var status int
	for attempt := 0; attempt < 2; attempt++ {
		accessToken, tokenErr := adapter.token(ctx, configuration)
		if tokenErr != nil {
			return signedData{}, stores.Evidence{}, tokenErr
		}
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if requestErr != nil {
			return signedData{}, stores.Evidence{}, invalid("query", requestErr)
		}
		request.Header.Set("Authorization", appAccessTokenAuthorization(accessToken))
		request.Header.Set("Content-Type", "application/json; charset=UTF-8")
		request.Header.Set("Accept", "application/json")
		responseBody, status, err = adapter.do(request)
		if err != nil {
			return signedData{}, stores.Evidence{}, err
		}
		if status != http.StatusUnauthorized {
			break
		}
		adapter.forgetToken(configuration.ClientID, accessToken)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return signedData{}, stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailureUnauthorized, 0, nil)
	}
	if status == http.StatusTooManyRequests {
		return signedData{}, stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailureRateLimited, 0, nil)
	}
	if status == http.StatusNotFound {
		return signedData{}, stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailureNotFound, 0, nil)
	}
	if status >= 500 {
		return signedData{}, stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailureTemporary, 0, nil)
	}
	if status < 200 || status >= 300 {
		return signedData{}, stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailurePermanent, 0, nil)
	}
	var result providerResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return signedData{}, stores.Evidence{}, invalid("query", err)
	}
	if result.ResponseCode != "0" && result.ResponseCode != "" {
		kind := stores.FailurePermanent
		if strings.HasPrefix(result.ResponseCode, "5") {
			kind = stores.FailureTemporary
		}
		return signedData{}, stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", kind, 0, nil)
	}
	signed, err := result.signedPurchase()
	if err != nil {
		return signedData{}, stores.Evidence{}, invalid("query", err)
	}
	artifact, err := stores.NewEvidence("application/json", responseBody)
	if err != nil {
		return signedData{}, stores.Evidence{}, invalid("query", err)
	}
	return signed, artifact, nil
}

// appAccessTokenAuthorization formats Huawei's app-level access token for Order and Subscription calls.
// The value is Basic followed by Base64("APPAT:" + accessToken).
func appAccessTokenAuthorization(accessToken string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte("APPAT:" + accessToken))
	return "Basic " + encoded
}

// signedPurchase selects the signed purchase JSON and the signature that covers those exact bytes.
func (result providerResponse) signedPurchase() (signedData, error) {
	switch {
	case result.PurchaseTokenData != "":
		if result.DataSignature == "" {
			return signedData{}, errors.New("huawei response omitted signed purchase data")
		}
		return signedData{
			data: result.PurchaseTokenData, signature: result.DataSignature, algorithm: result.SignatureAlgorithm,
		}, nil
	case result.InAppPurchaseData != "":
		signature := result.InAppPurchaseDataSignature
		if signature == "" {
			signature = result.DataSignature
		}
		if signature == "" {
			return signedData{}, errors.New("huawei response omitted signed purchase data")
		}
		return signedData{
			data: result.InAppPurchaseData, signature: signature, algorithm: result.SignatureAlgorithm,
		}, nil
	case result.SubscriptionPurchaseData != "":
		signature := result.SubscriptionPurchaseSignature
		if signature == "" {
			signature = result.DataSignature
		}
		if signature == "" {
			return signedData{}, errors.New("huawei response omitted signed purchase data")
		}
		return signedData{
			data: result.SubscriptionPurchaseData, signature: signature, algorithm: result.SignatureAlgorithm,
		}, nil
	default:
		return signedData{}, errors.New("huawei response omitted signed purchase data")
	}
}

// token returns a cached app-level access token, fetching one per client id when needed.
// The cache is held under tokenMu for the whole fetch so concurrent callers share one request.
func (adapter *Adapter) token(ctx context.Context, configuration credentialPayload) (string, error) {
	adapter.tokenMu.Lock()
	defer adapter.tokenMu.Unlock()
	if adapter.tokens == nil {
		adapter.tokens = make(map[string]cachedAccessToken)
	}
	now := adapter.now()
	if cached, ok := adapter.tokens[configuration.ClientID]; ok && cached.token != "" && now.Before(cached.expiresAt) {
		return cached.token, nil
	}
	accessToken, expiresIn, err := adapter.fetchToken(ctx, configuration)
	if err != nil {
		return "", err
	}
	if expiresAt, ok := tokenCacheExpiry(now, expiresIn); ok {
		adapter.tokens[configuration.ClientID] = cachedAccessToken{token: accessToken, expiresAt: expiresAt}
	} else {
		delete(adapter.tokens, configuration.ClientID)
	}
	return accessToken, nil
}

// fetchToken requests a new OAuth access token and reports its expires_in lifetime.
func (adapter *Adapter) fetchToken(ctx context.Context, configuration credentialPayload) (string, int64, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", configuration.ClientID)
	form.Set("client_secret", configuration.ClientSecret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, configuration.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, stores.NewFailure(adapter.Provider(), "token", stores.FailurePermanent, 0, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, status, err := adapter.do(request)
	if err != nil {
		return "", 0, err
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return "", 0, stores.NewFailure(adapter.Provider(), "token", stores.FailureTemporary, 0, nil)
	}
	if status < 200 || status >= 300 {
		return "", 0, stores.NewFailure(adapter.Provider(), "token", stores.FailureUnauthorized, 0, nil)
	}
	var result tokenResponse
	if err := decodeStrict(body, &result); err != nil || result.AccessToken == "" {
		return "", 0, stores.NewFailure(adapter.Provider(), "token", stores.FailureUnauthorized, 0, err)
	}
	return result.AccessToken, result.ExpiresIn, nil
}

// forgetToken drops one cached access token after Huawei rejected that exact value.
func (adapter *Adapter) forgetToken(clientID, accessToken string) {
	adapter.tokenMu.Lock()
	defer adapter.tokenMu.Unlock()
	cached, ok := adapter.tokens[clientID]
	if ok && cached.token == accessToken {
		delete(adapter.tokens, clientID)
	}
}

// now returns the adapter clock, or the wall clock when a test left it unset.
func (adapter *Adapter) now() time.Time {
	if adapter.clock != nil {
		return adapter.clock()
	}
	return time.Now()
}

// tokenCacheExpiry returns when a cached token must be refreshed, shortly before it expires.
func tokenCacheExpiry(now time.Time, expiresIn int64) (time.Time, bool) {
	if expiresIn <= 0 {
		return time.Time{}, false
	}
	lifetime := time.Duration(expiresIn) * time.Second
	if lifetime <= tokenExpiryMargin || lifetime/time.Second != time.Duration(expiresIn) {
		return time.Time{}, false
	}
	return now.Add(lifetime - tokenExpiryMargin), true
}

// do executes one bounded provider call and reads no more than the response limit.
func (adapter *Adapter) do(request *http.Request) ([]byte, int, error) {
	response, err := adapter.client.Do(request)
	if err != nil {
		return nil, 0, stores.NewFailure(adapter.Provider(), "http", stores.FailureTemporary, 0, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumProviderResponse+1))
	if err != nil {
		return nil, response.StatusCode, stores.NewFailure(adapter.Provider(), "http", stores.FailureTemporary, 0, err)
	}
	if int64(len(body)) > maximumProviderResponse {
		return nil, response.StatusCode, stores.NewFailure(adapter.Provider(), "http", stores.FailurePermanent, 0, nil)
	}
	return body, response.StatusCode, nil
}

// result normalizes one authoritative Huawei purchase into shared artifacts and observations.
func (adapter *Adapter) result(
	application core.Application,
	productKind core.ProductKind,
	purchase purchaseData,
	artifact stores.Evidence,
) (stores.VerificationResult, error) {
	observedAt := adapter.clock().UTC()
	state, access, reason := normalizeState(productKind, purchase, observedAt)
	startsAt := milliseconds(purchase.PurchaseTime)
	var endsAt *time.Time
	if purchase.ExpirationDate > 0 {
		value := milliseconds(purchase.ExpirationDate)
		endsAt = &value
	}
	renewal := core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable}
	if productKind == core.ProductKindSubscription {
		renewal = core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalDisabled}
		if purchase.RenewStatus == 1 {
			renewal.Status = core.RenewalEnabled
		}
	}
	quantity := purchase.Quantity
	if quantity == 0 {
		quantity = 1
	}
	if err := validatePurchasePrice(purchase); err != nil {
		return stores.VerificationResult{}, invalid("normalize", err)
	}
	price := &core.PurchasePrice{
		Milliunits: purchase.Price * 10,
		Currency:   purchase.Currency,
	}
	references := make([]core.StoreReference, 0, 4)
	for _, candidate := range []struct {
		role  core.ReferenceRole
		kind  string
		value string
	}{
		{role: core.ReferenceTransaction, kind: "order_id", value: purchase.OrderID},
		{role: core.ReferenceQuery, kind: "purchase_token", value: purchase.PurchaseToken},
		{role: core.ReferenceQuery, kind: "subscription_id", value: purchase.SubscriptionID},
		{role: core.ReferenceCustomerBinding, kind: "developer_payload", value: purchase.DeveloperPayload},
	} {
		if candidate.value == "" {
			continue
		}
		reference, err := core.NewStoreReference(candidate.role, candidate.kind, candidate.value)
		if err != nil {
			return stores.VerificationResult{}, invalid("normalize", err)
		}
		references = append(references, reference)
	}
	identity := observationSnapshotIdentity(
		application.ID,
		productKind,
		purchase,
		state,
		access,
		reason,
		renewal,
		quantity,
	)
	observation := core.PurchaseObservation{
		ID:              core.ObservationID("hua-" + hex.EncodeToString(identity[:16])),
		ApplicationID:   application.ID,
		Store:           application.Store,
		ProductID:       core.ProviderProductID(purchase.ProductID),
		ProductKind:     productKind,
		State:           state,
		ProviderState:   strconv.Itoa(purchase.PurchaseState),
		Access:          access,
		AccessReason:    reason,
		Ownership:       core.OwnershipPurchased,
		Quantity:        quantity,
		Price:           price,
		OccurredAt:      startsAt,
		ObservedAt:      observedAt,
		EffectivePeriod: core.EffectivePeriod{StartsAt: startsAt, EndsAt: endsAt},
		Renewal:         renewal,
		References:      references,
	}
	if err := observation.Validate(); err != nil {
		return stores.VerificationResult{}, invalid("normalize", err)
	}
	return stores.VerificationResult{
		VerifiedAt:   observedAt,
		Artifacts:    []stores.VerifiedArtifact{{Kind: "huawei_server_response", Evidence: artifact}},
		Observations: []core.PurchaseObservation{observation},
	}, nil
}

// observationSnapshotIdentity hashes every authoritative field that can change the immutable normalized snapshot.
func observationSnapshotIdentity(
	applicationID core.ApplicationID,
	productKind core.ProductKind,
	purchase purchaseData,
	state core.LifecycleState,
	access core.AccessStatus,
	reason core.AccessReason,
	renewal core.Renewal,
	quantity uint32,
) [32]byte {
	return sha256.Sum256([]byte(strings.Join([]string{
		string(applicationID),
		string(productKind),
		string(purchase.ApplicationID),
		purchase.ProductID,
		purchase.OrderID,
		purchase.PurchaseToken,
		strconv.Itoa(purchase.PurchaseState),
		strconv.FormatInt(purchase.PurchaseTime, 10),
		strconv.FormatInt(purchase.ExpirationDate, 10),
		strconv.FormatInt(purchase.GraceExpirationTime, 10),
		strconv.FormatInt(purchase.CancellationTime, 10),
		strconv.FormatInt(purchase.CancelTime, 10),
		strconv.Itoa(purchase.RenewStatus),
		strconv.Itoa(purchase.RetryFlag),
		strconv.FormatBool(purchase.SubIsValid),
		purchase.DeveloperPayload,
		strconv.FormatUint(uint64(quantity), 10),
		strconv.FormatInt(purchase.Price, 10),
		purchase.Currency,
		string(state),
		string(access),
		string(reason),
		string(renewal.Mode),
		string(renewal.Status),
	}, "\x00")))
}

// parseEvidence decodes the submitted envelope and its embedded purchase data.
func parseEvidence(evidence stores.Evidence) (clientEvidence, purchaseData, error) {
	if evidence.ContentType != EvidenceContentType {
		return clientEvidence{}, purchaseData{}, errors.New("unsupported Huawei evidence content type")
	}
	var envelope clientEvidence
	if err := decodeStrict(evidence.Bytes(), &envelope); err != nil {
		return clientEvidence{}, purchaseData{}, err
	}
	if envelope.PurchaseData == "" || envelope.Signature == "" ||
		(envelope.ProductKind != core.ProductKindSubscription && envelope.ProductKind != core.ProductKindNonConsumable) {
		return clientEvidence{}, purchaseData{}, errors.New("incomplete Huawei purchase evidence")
	}
	var purchase purchaseData
	if err := json.Unmarshal([]byte(envelope.PurchaseData), &purchase); err != nil {
		return clientEvidence{}, purchaseData{}, err
	}
	return envelope, purchase, nil
}

// validateSubmittedScope checks client claims before any provider network call.
func validateSubmittedScope(request stores.VerificationRequest, purchase purchaseData) error {
	if string(purchase.ApplicationID) != string(request.Application.Store.ID) || purchase.ProductID == "" || purchase.PurchaseToken == "" {
		return errors.New("submitted Huawei application or purchase identity mismatch")
	}
	if len(request.ClaimedProducts) > 0 {
		if len(request.ClaimedProducts) != 1 || string(request.ClaimedProducts[0]) != purchase.ProductID {
			return errors.New("submitted Huawei product does not match claim")
		}
	}
	return validatePurchasePrice(purchase)
}

// comparePurchases enforces stable purchase identity across client and authoritative data.
func comparePurchases(submitted, authoritative purchaseData) error {
	if submitted.ApplicationID != authoritative.ApplicationID || submitted.ProductID != authoritative.ProductID ||
		submitted.SubscriptionID != authoritative.SubscriptionID ||
		submitted.PurchaseToken != authoritative.PurchaseToken || submitted.Price != authoritative.Price ||
		submitted.Currency != authoritative.Currency {
		return errors.New("authoritative Huawei purchase does not match submitted evidence")
	}
	return validatePurchasePrice(authoritative)
}

// validatePurchasePrice validates Huawei's signed amount in hundredths of an ISO 4217 unit.
func validatePurchasePrice(purchase purchaseData) error {
	if purchase.Price < 0 || purchase.Price > math.MaxInt64/10 {
		return errors.New("Huawei purchase price is outside the supported range")
	}
	if len(purchase.Currency) != 3 {
		return errors.New("Huawei purchase currency must be a three-letter ISO 4217 code")
	}
	for _, character := range purchase.Currency {
		if character < 'A' || character > 'Z' {
			return errors.New("Huawei purchase currency must use uppercase ASCII letters")
		}
	}
	return nil
}

// normalizeState maps authoritative Huawei purchase and subscription fields to shared access state.
func normalizeState(
	productKind core.ProductKind,
	purchase purchaseData,
	now time.Time,
) (core.LifecycleState, core.AccessStatus, core.AccessReason) {
	if productKind == core.ProductKindSubscription && purchase.CancelTime > 0 {
		return core.LifecycleRevoked, core.AccessDenied, core.AccessReasonRevoked
	}
	if productKind != core.ProductKindSubscription && purchase.PurchaseState == 2 {
		return core.LifecycleRefunded, core.AccessDenied, core.AccessReasonRefunded
	}
	if purchase.PurchaseState == 1 {
		return core.LifecycleRevoked, core.AccessDenied, core.AccessReasonRevoked
	}
	if productKind == core.ProductKindSubscription {
		expiresAt := milliseconds(purchase.ExpirationDate)
		if purchase.PurchaseState == 2 {
			if (purchase.RetryFlag == 1 && purchase.GraceExpirationTime > now.UnixMilli()) ||
				(purchase.SubIsValid && purchase.ExpirationDate > 0 && expiresAt.After(now)) {
				return core.LifecycleRefunded, core.AccessAllowed, core.AccessReasonRefunded
			}
			return core.LifecycleRefunded, core.AccessDenied, core.AccessReasonRefunded
		}
		if purchase.PurchaseState != 0 {
			return core.LifecyclePending, core.AccessUnresolved, core.AccessReasonPendingPayment
		}
		if purchase.RetryFlag == 1 && purchase.GraceExpirationTime > now.UnixMilli() {
			return core.LifecycleGracePeriod, core.AccessAllowed, core.AccessReasonGracePeriod
		}
		if !purchase.SubIsValid || purchase.ExpirationDate <= 0 || !expiresAt.After(now) {
			return core.LifecycleExpired, core.AccessDenied, core.AccessReasonExpired
		}
		if purchase.RenewStatus == 0 || purchase.CancellationTime > 0 {
			return core.LifecycleCanceled, core.AccessAllowed, core.AccessReasonCanceledAtPeriodEnd
		}
	}
	if purchase.PurchaseState != 0 {
		return core.LifecyclePending, core.AccessUnresolved, core.AccessReasonPendingPayment
	}
	return core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid
}

// verifySignature validates Huawei's detached SHA-256 RSA signature over exact JSON bytes.
// An empty algorithm uses SHA256WithRSA. SHA256WithRSA and SHA256WithRSA/PSS are accepted.
func verifySignature(publicKey *rsa.PublicKey, data, encodedSignature, algorithm string) error {
	signature, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil {
		return errors.New("huawei signature is not valid base64")
	}
	if strings.TrimSpace(algorithm) == "" {
		algorithm = "SHA256WithRSA"
	}
	digest := sha256.Sum256([]byte(data))
	switch strings.ToUpper(strings.TrimSpace(algorithm)) {
	case "SHA256WITHRSA":
		err = rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature)
	case "SHA256WITHRSA/PSS":
		err = rsa.VerifyPSS(publicKey, crypto.SHA256, digest[:], signature, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthAuto,
			Hash:       crypto.SHA256,
		})
	default:
		return errors.New("unsupported huawei signature algorithm")
	}
	if err != nil {
		return errors.New("huawei signature verification failed")
	}
	return nil
}

// UnmarshalJSON accepts a JSON string or integer and stores its decimal or literal text.
func (value *huaweiText) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return errors.New("huawei text value is empty")
	}
	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*value = huaweiText(text)
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return errors.New("huawei text value must be a string or integer")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("huawei text value must be a string or integer")
	}
	if _, err := number.Int64(); err != nil || strings.HasPrefix(number.String(), "-") {
		return errors.New("huawei text value must be a string or integer")
	}
	*value = huaweiText(number.String())
	return nil
}

// parsePublicKey accepts PEM or base64 DER PKIX and PKCS#1 RSA public keys.
func parsePublicKey(encoded string) (*rsa.PublicKey, error) {
	data := []byte(encoded)
	if block, _ := pem.Decode(data); block != nil {
		data = block.Bytes
	} else {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, errors.New("huawei public key is neither PEM nor base64 DER")
		}
		data = decoded
	}
	if parsed, err := x509.ParsePKIXPublicKey(data); err == nil {
		if key, ok := parsed.(*rsa.PublicKey); ok {
			return key, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(data); err == nil {
		return key, nil
	}
	return nil, errors.New("huawei public key is not RSA")
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

// milliseconds converts a provider Unix millisecond timestamp to UTC.
func milliseconds(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

// invalid constructs a redacted non-retryable evidence failure.
func invalid(operation string, cause error) error {
	return stores.NewFailure(core.ProviderHuaweiAppGallery, operation, stores.FailureInvalidEvidence, 0, cause)
}

// zero overwrites temporary credential payload bytes after parsing.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
