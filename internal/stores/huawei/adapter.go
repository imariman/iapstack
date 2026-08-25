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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// CredentialKind identifies the v1 Huawei server API credential package.
	CredentialKind stores.CredentialKind = "huawei_server_api"
	// CredentialContentType identifies the Huawei credential JSON representation.
	CredentialContentType = "application/vnd.iapstack.huawei-credentials+json"
	// CredentialSchemaVersion identifies the initial server API credential shape.
	CredentialSchemaVersion = 1
	// EvidenceContentType identifies the Huawei client purchase evidence representation.
	EvidenceContentType = "application/vnd.iapstack.huawei-purchase+json"
	// maximumProviderResponse bounds Huawei token and purchase response bodies.
	maximumProviderResponse int64 = 2 << 20
	// defaultTokenURL is Huawei's documented OAuth token service.
	defaultTokenURL = "https://oauth-login.cloud.huawei.com/oauth2/v3/token"
)

// Adapter verifies Huawei evidence using application-scoped credentials and authoritative server APIs.
type Adapter struct {
	credentials stores.CredentialSource
	client      *http.Client
	clock       func() time.Time
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
	PurchaseData string           `json:"purchase_data"`
	Signature    string           `json:"signature"`
	ProductKind  core.ProductKind `json:"product_kind"`
}

// NotificationEnvelope is the signed, customer-bound notification contract accepted by IAPStack.
type NotificationEnvelope struct {
	ExternalCustomerID string                   `json:"external_customer_id"`
	ClaimedProducts    []core.ProviderProductID `json:"claimed_products"`
	Purchase           json.RawMessage          `json:"purchase"`
}

// purchaseData contains the Huawei fields required for normalization and consistency checks.
type purchaseData struct {
	ApplicationID       string `json:"applicationId"`
	PackageName         string `json:"packageName"`
	ProductID           string `json:"productId"`
	OrderID             string `json:"orderId"`
	PurchaseToken       string `json:"purchaseToken"`
	PurchaseState       int    `json:"purchaseState"`
	PurchaseTime        int64  `json:"purchaseTime"`
	ExpirationDate      int64  `json:"expirationDate"`
	GraceExpirationTime int64  `json:"graceExpirationTime"`
	RenewStatus         int    `json:"renewStatus"`
	RetryFlag           int    `json:"retryFlag"`
	SubIsValid          bool   `json:"subIsvalid"`
	DeveloperPayload    string `json:"developerPayload"`
	Quantity            uint32 `json:"quantity"`
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
}

// tokenResponse contains the OAuth bearer fields needed by provider calls.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// signedData pairs authoritative Huawei JSON with its detached signature.
type signedData struct {
	data      string
	signature string
}

// New constructs a bounded Huawei AppGallery server adapter.
func New(credentials stores.CredentialSource, timeout time.Duration) (*Adapter, error) {
	if credentials == nil {
		return nil, errors.New("Huawei credential source is required")
	}
	if timeout <= 0 {
		return nil, errors.New("Huawei provider timeout must be positive")
	}
	return &Adapter{
		credentials: credentials,
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		clock: time.Now,
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
	if err := verifySignature(publicKey, evidence.PurchaseData, evidence.Signature); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	authoritative, rawArtifact, err := adapter.query(ctx, configuration, submitted, evidence.ProductKind)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	if err := verifySignature(publicKey, authoritative.data, authoritative.signature); err != nil {
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
	for _, reference := range request.QueryReferences {
		if reference.Kind == "purchase_token" {
			token = reference.Value()
			break
		}
	}
	if token == "" || len(request.ExpectedProducts) != 1 {
		return stores.VerificationResult{}, invalid("reconcile", errors.New("Huawei reconciliation requires one product and purchase token"))
	}
	configuration, publicKey, err := adapter.configuration(ctx, request.Application)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	query := purchaseData{ApplicationID: string(request.Application.Store.ID), ProductID: string(request.ExpectedProducts[0]), PurchaseToken: token}
	for _, productKind := range []core.ProductKind{core.ProductKindSubscription, core.ProductKindNonConsumable} {
		authoritative, rawArtifact, queryErr := adapter.query(ctx, configuration, query, productKind)
		if queryErr != nil {
			var failure *stores.Failure
			if productKind == core.ProductKindSubscription && errors.As(queryErr, &failure) && failure.Kind == stores.FailureNotFound {
				continue
			}
			return stores.VerificationResult{}, queryErr
		}
		if err := verifySignature(publicKey, authoritative.data, authoritative.signature); err != nil {
			return stores.VerificationResult{}, invalid("reconcile", err)
		}
		var current purchaseData
		if err := json.Unmarshal([]byte(authoritative.data), &current); err != nil {
			return stores.VerificationResult{}, invalid("reconcile", err)
		}
		if current.PurchaseToken != token || current.ProductID != query.ProductID || current.ApplicationID != query.ApplicationID {
			return stores.VerificationResult{}, invalid("reconcile", errors.New("authoritative Huawei scope mismatch"))
		}
		return adapter.result(request.Application, productKind, current, rawArtifact)
	}
	return stores.VerificationResult{}, stores.NewFailure(core.ProviderHuaweiAppGallery, "reconcile", stores.FailureNotFound, 0, nil)
}

// ValidateNotification verifies a signed notification envelope before durable acknowledgement.
func (adapter *Adapter) ValidateNotification(
	ctx context.Context,
	application core.Application,
	payload []byte,
) (NotificationEnvelope, error) {
	var envelope NotificationEnvelope
	if err := decodeStrict(payload, &envelope); err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	if strings.TrimSpace(envelope.ExternalCustomerID) == "" || len(envelope.ClaimedProducts) == 0 {
		return NotificationEnvelope{}, invalid("notification", errors.New("notification customer and products are required"))
	}
	evidence, err := stores.NewEvidence(EvidenceContentType, envelope.Purchase)
	if err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	request := stores.VerificationRequest{Application: application, CustomerID: core.CustomerID("notification-check"),
		ClaimedProducts: envelope.ClaimedProducts, Evidence: evidence}
	if err := request.Validate(); err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	purchaseEnvelope, purchase, err := parseEvidence(evidence)
	if err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	if err := validateSubmittedScope(request, purchase); err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	if purchase.DeveloperPayload == "" || purchase.DeveloperPayload != envelope.ExternalCustomerID {
		return NotificationEnvelope{}, invalid("notification", errors.New("notification customer binding mismatch"))
	}
	_, publicKey, err := adapter.configuration(ctx, application)
	if err != nil {
		return NotificationEnvelope{}, err
	}
	if err := verifySignature(publicKey, purchaseEnvelope.PurchaseData, purchaseEnvelope.Signature); err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	canonicalPurchase, err := json.Marshal(purchaseEnvelope)
	if err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	envelope.Purchase = canonicalPurchase
	return envelope, nil
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
		if err := validateHTTPS(endpoint); err != nil {
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
	accessToken, err := adapter.token(ctx, configuration)
	if err != nil {
		return signedData{}, stores.Evidence{}, err
	}
	endpoint := configuration.OrderURL
	if productKind == core.ProductKindSubscription {
		endpoint = configuration.SubscriptionURL
	}
	body, err := json.Marshal(map[string]string{
		"purchaseToken": purchase.PurchaseToken,
		"productId":     purchase.ProductID,
	})
	if err != nil {
		return signedData{}, stores.Evidence{}, invalid("query", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return signedData{}, stores.Evidence{}, invalid("query", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.Header.Set("Accept", "application/json")
	responseBody, status, err := adapter.do(request)
	if err != nil {
		return signedData{}, stores.Evidence{}, err
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
	data, signature := result.PurchaseTokenData, result.DataSignature
	if data == "" {
		data, signature = result.InAppPurchaseData, result.InAppPurchaseDataSignature
	}
	if data == "" {
		data, signature = result.SubscriptionPurchaseData, result.SubscriptionPurchaseSignature
	}
	if data == "" || signature == "" {
		return signedData{}, stores.Evidence{}, invalid("query", errors.New("Huawei response omitted signed purchase data"))
	}
	artifact, err := stores.NewEvidence("application/json", responseBody)
	if err != nil {
		return signedData{}, stores.Evidence{}, invalid("query", err)
	}
	return signedData{data: data, signature: signature}, artifact, nil
}

// token requests a server-to-server OAuth access token using application credentials.
func (adapter *Adapter) token(ctx context.Context, configuration credentialPayload) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", configuration.ClientID)
	form.Set("client_secret", configuration.ClientSecret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, configuration.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", stores.NewFailure(adapter.Provider(), "token", stores.FailurePermanent, 0, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, status, err := adapter.do(request)
	if err != nil {
		return "", err
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return "", stores.NewFailure(adapter.Provider(), "token", stores.FailureTemporary, 0, nil)
	}
	if status < 200 || status >= 300 {
		return "", stores.NewFailure(adapter.Provider(), "token", stores.FailureUnauthorized, 0, nil)
	}
	var result tokenResponse
	if err := decodeStrict(body, &result); err != nil || result.AccessToken == "" {
		return "", stores.NewFailure(adapter.Provider(), "token", stores.FailureUnauthorized, 0, err)
	}
	return result.AccessToken, nil
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
	references := make([]core.StoreReference, 0, 3)
	for _, candidate := range []struct {
		role  core.ReferenceRole
		kind  string
		value string
	}{
		{role: core.ReferenceTransaction, kind: "order_id", value: purchase.OrderID},
		{role: core.ReferenceQuery, kind: "purchase_token", value: purchase.PurchaseToken},
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
		purchase.ApplicationID,
		purchase.ProductID,
		purchase.OrderID,
		purchase.PurchaseToken,
		strconv.Itoa(purchase.PurchaseState),
		strconv.FormatInt(purchase.PurchaseTime, 10),
		strconv.FormatInt(purchase.ExpirationDate, 10),
		strconv.FormatInt(purchase.GraceExpirationTime, 10),
		strconv.Itoa(purchase.RenewStatus),
		strconv.Itoa(purchase.RetryFlag),
		strconv.FormatBool(purchase.SubIsValid),
		purchase.DeveloperPayload,
		strconv.FormatUint(uint64(quantity), 10),
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
	if purchase.ApplicationID != string(request.Application.Store.ID) || purchase.ProductID == "" || purchase.PurchaseToken == "" {
		return errors.New("submitted Huawei application or purchase identity mismatch")
	}
	if len(request.ClaimedProducts) > 0 {
		if len(request.ClaimedProducts) != 1 || string(request.ClaimedProducts[0]) != purchase.ProductID {
			return errors.New("submitted Huawei product does not match claim")
		}
	}
	return nil
}

// comparePurchases enforces stable purchase identity across client and authoritative data.
func comparePurchases(submitted, authoritative purchaseData) error {
	if submitted.ApplicationID != authoritative.ApplicationID || submitted.ProductID != authoritative.ProductID ||
		submitted.PurchaseToken != authoritative.PurchaseToken {
		return errors.New("authoritative Huawei purchase does not match submitted evidence")
	}
	return nil
}

// normalizeState maps authoritative Huawei purchase and subscription fields to shared access state.
func normalizeState(
	productKind core.ProductKind,
	purchase purchaseData,
	now time.Time,
) (core.LifecycleState, core.AccessStatus, core.AccessReason) {
	if purchase.PurchaseState == 2 {
		return core.LifecycleRefunded, core.AccessDenied, core.AccessReasonRefunded
	}
	if purchase.PurchaseState == 1 {
		return core.LifecycleRevoked, core.AccessDenied, core.AccessReasonRevoked
	}
	if purchase.PurchaseState != 0 {
		return core.LifecyclePending, core.AccessUnresolved, core.AccessReasonPendingPayment
	}
	if productKind == core.ProductKindSubscription {
		expiresAt := milliseconds(purchase.ExpirationDate)
		if purchase.RetryFlag == 1 && purchase.GraceExpirationTime > now.UnixMilli() {
			return core.LifecycleGracePeriod, core.AccessAllowed, core.AccessReasonGracePeriod
		}
		if !purchase.SubIsValid || purchase.ExpirationDate <= 0 || !expiresAt.After(now) {
			return core.LifecycleExpired, core.AccessDenied, core.AccessReasonExpired
		}
		if purchase.RenewStatus == 0 {
			return core.LifecycleCanceled, core.AccessAllowed, core.AccessReasonCanceledAtPeriodEnd
		}
	}
	return core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid
}

// verifySignature validates Huawei's detached SHA-256 with RSA signature over exact JSON bytes.
func verifySignature(publicKey *rsa.PublicKey, data, encodedSignature string) error {
	signature, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil {
		return errors.New("Huawei signature is not valid base64")
	}
	digest := sha256.Sum256([]byte(data))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return errors.New("Huawei signature verification failed")
	}
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
			return nil, errors.New("Huawei public key is neither PEM nor base64 DER")
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
	return nil, errors.New("Huawei public key is not RSA")
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

// validateHTTPS requires absolute HTTPS provider endpoints without embedded credentials.
func validateHTTPS(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("Huawei endpoint must be an absolute HTTPS URL")
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
