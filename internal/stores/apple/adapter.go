// Package apple implements Apple App Store purchase verification and reconciliation.
package apple

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// CredentialKind identifies the v1 App Store Server API credential package.
	CredentialKind stores.CredentialKind = "apple_app_store_server_api" // #nosec G101 -- This is a public credential type discriminator, not credential material.
	// CredentialContentType identifies the Apple credential JSON representation.
	CredentialContentType = "application/vnd.iapstack.apple-credentials+json"
	// CredentialSchemaVersion identifies the initial App Store Server API credential shape.
	CredentialSchemaVersion = 1
	// EvidenceContentType identifies the Apple signed transaction evidence representation.
	EvidenceContentType = stores.AppleEvidenceContentType
	// productionBaseURL is Apple's current production App Store Server API domain.
	productionBaseURL = "https://api.storekit.apple.com"
	// sandboxBaseURL is Apple's current sandbox App Store Server API domain.
	sandboxBaseURL = "https://api.storekit-sandbox.apple.com"
	// maximumProviderResponse bounds App Store Server API response bodies.
	maximumProviderResponse int64 = 2 << 20
	// tokenLifetime keeps authorization tokens short-lived and below Apple's one-hour maximum.
	tokenLifetime = 5 * time.Minute
	// appleAudience is the required App Store Server API JWT audience.
	appleAudience = "appstoreconnect-v1"
	// appleSubscriptionType is Apple's auto-renewable subscription product discriminator.
	appleSubscriptionType = "Auto-Renewable Subscription"
	// appleNonConsumableType is Apple's non-consumable product discriminator.
	appleNonConsumableType = "Non-Consumable"
	// applePurchasedOwnership is Apple's directly purchased ownership discriminator.
	applePurchasedOwnership = "PURCHASED"
	// appleFamilySharedOwnership is Apple's family sharing ownership discriminator.
	appleFamilySharedOwnership = "FAMILY_SHARED"
)

// Adapter verifies Apple evidence with application-scoped credentials and authoritative server APIs.
type Adapter struct {
	credentials   stores.CredentialSource
	client        *http.Client
	clock         func() time.Time
	productionURL string
	sandboxURL    string
}

// credentialPayload is the versioned private App Store Server API configuration.
type credentialPayload struct {
	IssuerID         string   `json:"issuer_id"`
	KeyID            string   `json:"key_id"`
	BundleID         string   `json:"bundle_id"`
	AppAppleID       uint64   `json:"app_apple_id,omitempty"`
	PrivateKey       string   `json:"private_key"`
	RootCertificates []string `json:"root_certificates"`
}

// configuration contains parsed credential material scoped to one application.
type configuration struct {
	issuerID   string
	keyID      string
	bundleID   string
	appAppleID uint64
	privateKey *ecdsa.PrivateKey
	roots      trustedRoots
}

// clientEvidence contains the StoreKit signed transaction submitted by an application.
type clientEvidence struct {
	SignedTransaction string           `json:"signed_transaction"`
	ProductKind       core.ProductKind `json:"product_kind"`
}

// transactionInfoResponse contains Apple's authoritative signed transaction response.
type transactionInfoResponse struct {
	SignedTransactionInfo string `json:"signedTransactionInfo"`
}

// transactionPayload contains the Apple fields required for scope checks and normalization.
type transactionPayload struct {
	OriginalTransactionID string `json:"originalTransactionId"`
	TransactionID         string `json:"transactionId"`
	WebOrderLineItemID    string `json:"webOrderLineItemId"`
	BundleID              string `json:"bundleId"`
	ProductID             string `json:"productId"`
	PurchaseDate          int64  `json:"purchaseDate"`
	ExpiresDate           int64  `json:"expiresDate"`
	Quantity              uint32 `json:"quantity"`
	Type                  string `json:"type"`
	AppAccountToken       string `json:"appAccountToken"`
	OwnershipType         string `json:"inAppOwnershipType"`
	SignedDate            int64  `json:"signedDate"`
	RevocationReason      *int   `json:"revocationReason"`
	RevocationDate        int64  `json:"revocationDate"`
	IsUpgraded            bool   `json:"isUpgraded"`
	Environment           string `json:"environment"`
}

// New constructs a bounded Apple App Store server adapter.
func New(credentials stores.CredentialSource, timeout time.Duration) (*Adapter, error) {
	if credentials == nil {
		return nil, errors.New("apple credential source is required")
	}
	if timeout <= 0 {
		return nil, errors.New("apple provider timeout must be positive")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &Adapter{
		credentials: credentials,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		clock:         time.Now,
		productionURL: productionBaseURL,
		sandboxURL:    sandboxBaseURL,
	}, nil
}

// Provider identifies Apple App Store as the implemented store.
func (adapter *Adapter) Provider() core.Provider {
	return core.ProviderAppleAppStore
}

// Verify validates submitted JWS evidence, queries Apple, and normalizes authoritative state.
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
	submitted, err := verifyTransactionJWS(evidence.SignedTransaction, configuration.roots)
	if err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	if err := validateTransactionScope(request, configuration.bundleID, evidence.ProductKind, submitted); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	if evidence.ProductKind == core.ProductKindSubscription {
		transaction, renewal, status, artifact, queryErr := adapter.querySubscriptionStatus(
			ctx, request.Application, configuration, submitted.OriginalTransactionID,
		)
		if queryErr != nil {
			return stores.VerificationResult{}, queryErr
		}
		if err := validateTransactionScope(request, configuration.bundleID, evidence.ProductKind, transaction); err != nil {
			return stores.VerificationResult{}, invalid("verify", err)
		}
		if transaction.OriginalTransactionID != submitted.OriginalTransactionID ||
			transaction.AppAccountToken != submitted.AppAccountToken {
			return stores.VerificationResult{}, invalid("verify", errors.New("apple subscription lineage does not match submitted evidence"))
		}
		if err := validateRenewalScope(request.Application, transaction, renewal); err != nil {
			return stores.VerificationResult{}, invalid("verify", err)
		}
		return adapter.result(request.Application, evidence.ProductKind, transaction, artifact, &subscriptionSnapshot{
			Status: status, Renewal: renewal,
		})
	}
	authoritativeJWS, artifact, err := adapter.query(ctx, request.Application, configuration, submitted.TransactionID)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	authoritative, err := verifyTransactionJWS(authoritativeJWS, configuration.roots)
	if err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	if err := validateTransactionScope(request, configuration.bundleID, evidence.ProductKind, authoritative); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	if err := compareTransactions(submitted, authoritative); err != nil {
		return stores.VerificationResult{}, invalid("verify", err)
	}
	return adapter.result(request.Application, evidence.ProductKind, authoritative, artifact, nil)
}

// Reconcile queries current Apple transaction state from one stored transaction reference.
func (adapter *Adapter) Reconcile(
	ctx context.Context,
	request stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	if err := request.Validate(); err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	if len(request.ExpectedProducts) != 1 {
		return stores.VerificationResult{}, invalid("reconcile", errors.New("apple reconciliation requires one expected product"))
	}
	transactionID := ""
	for _, reference := range request.QueryReferences {
		if reference.Kind == "transaction_id" {
			transactionID = reference.Value()
			break
		}
	}
	if transactionID == "" {
		return stores.VerificationResult{}, invalid("reconcile", errors.New("apple reconciliation requires a transaction ID"))
	}
	configuration, err := adapter.configuration(ctx, request.Application)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	authoritativeJWS, artifact, err := adapter.query(ctx, request.Application, configuration, transactionID)
	if err != nil {
		return stores.VerificationResult{}, err
	}
	authoritative, err := verifyTransactionJWS(authoritativeJWS, configuration.roots)
	if err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	productKind, err := productKind(authoritative.Type)
	if err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	verificationRequest := stores.VerificationRequest{
		Application: request.Application, CustomerID: request.CustomerID,
		ClaimedProducts: request.ExpectedProducts, ExpectedCustomerBindings: request.ExpectedCustomerBindings,
		Evidence: stores.Evidence{},
	}
	if err := validateTransactionScope(verificationRequest, configuration.bundleID, productKind, authoritative); err != nil {
		return stores.VerificationResult{}, invalid("reconcile", err)
	}
	if productKind == core.ProductKindSubscription {
		transaction, renewal, status, statusArtifact, queryErr := adapter.querySubscriptionStatus(
			ctx, request.Application, configuration, authoritative.OriginalTransactionID,
		)
		if queryErr != nil {
			return stores.VerificationResult{}, queryErr
		}
		if err := validateTransactionScope(verificationRequest, configuration.bundleID, productKind, transaction); err != nil {
			return stores.VerificationResult{}, invalid("reconcile", err)
		}
		if err := validateRenewalScope(request.Application, transaction, renewal); err != nil {
			return stores.VerificationResult{}, invalid("reconcile", err)
		}
		return adapter.result(request.Application, productKind, transaction, statusArtifact, &subscriptionSnapshot{
			Status: status, Renewal: renewal,
		})
	}
	return adapter.result(request.Application, productKind, authoritative, artifact, nil)
}

// configuration opens and validates one application-scoped Apple credential payload.
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
	if payload.IssuerID == "" || payload.KeyID == "" || payload.BundleID == "" || payload.PrivateKey == "" {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	if application.Store.Provider != core.ProviderAppleAppStore {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	if string(application.Store.ID) != payload.BundleID {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	if application.Store.Environment == core.EnvironmentProduction && payload.AppAppleID == 0 {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	if application.Store.Environment != core.EnvironmentProduction && application.Store.Environment != core.EnvironmentSandbox {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, nil)
	}
	privateKey, err := parsePrivateKey(payload.PrivateKey)
	if err != nil {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, err)
	}
	roots, err := parseTrustedRoots(payload.RootCertificates)
	if err != nil {
		return configuration{}, stores.NewFailure(adapter.Provider(), "credentials", stores.FailurePermanent, 0, err)
	}
	return configuration{
		issuerID: payload.IssuerID, keyID: payload.KeyID, bundleID: payload.BundleID,
		appAppleID: payload.AppAppleID, privateKey: privateKey, roots: roots,
	}, nil
}

// query requests one authoritative signed transaction by transaction identifier.
func (adapter *Adapter) query(
	ctx context.Context,
	application core.Application,
	configuration configuration,
	transactionID string,
) (string, stores.Evidence, error) {
	if strings.TrimSpace(transactionID) == "" {
		return "", stores.Evidence{}, invalid("query", errors.New("apple transaction ID is required"))
	}
	token, err := adapter.authorizationToken(configuration)
	if err != nil {
		return "", stores.Evidence{}, stores.NewFailure(adapter.Provider(), "token", stores.FailurePermanent, 0, err)
	}
	baseURL := adapter.productionURL
	if application.Store.Environment == core.EnvironmentSandbox {
		baseURL = adapter.sandboxURL
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/inApps/v1/transactions/" + url.PathEscape(transactionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailurePermanent, 0, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	body, status, responseHeader, err := adapter.do(request)
	if err != nil {
		return "", stores.Evidence{}, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "", stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailureUnauthorized, 0, nil)
	}
	if status == http.StatusNotFound {
		return "", stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailureNotFound, 0, nil)
	}
	if status == http.StatusTooManyRequests {
		return "", stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailureRateLimited, retryAfter(responseHeader, adapter.clock()), nil)
	}
	if status >= 500 {
		return "", stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailureTemporary, 0, nil)
	}
	if status < 200 || status >= 300 {
		return "", stores.Evidence{}, stores.NewFailure(adapter.Provider(), "query", stores.FailurePermanent, 0, nil)
	}
	var response transactionInfoResponse
	if err := json.Unmarshal(body, &response); err != nil || response.SignedTransactionInfo == "" {
		return "", stores.Evidence{}, invalid("query", errors.New("apple response omitted signed transaction info"))
	}
	artifact, err := stores.NewEvidence("application/json", body)
	if err != nil {
		return "", stores.Evidence{}, invalid("query", err)
	}
	return response.SignedTransactionInfo, artifact, nil
}

// authorizationToken signs one short-lived ES256 App Store Server API bearer token.
func (adapter *Adapter) authorizationToken(configuration configuration) (string, error) {
	now := adapter.clock().UTC()
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": configuration.keyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"iss": configuration.issuerID,
		"iat": now.Unix(),
		"exp": now.Add(tokenLifetime).Unix(),
		"aud": appleAudience,
		"bid": configuration.bundleID,
	})
	if err != nil {
		return "", err
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, configuration.privateKey, digest[:])
	if err != nil {
		return "", err
	}
	signature := append(paddedInteger(r, 32), paddedInteger(s, 32)...)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
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

// result normalizes one authoritative Apple transaction into shared artifacts and observations.
func (adapter *Adapter) result(
	application core.Application,
	kind core.ProductKind,
	transaction transactionPayload,
	artifact stores.Evidence,
	subscription *subscriptionSnapshot,
) (stores.VerificationResult, error) {
	observedAt := adapter.clock().UTC()
	state, access, reason := normalizeState(kind, transaction, observedAt)
	startsAt := milliseconds(transaction.PurchaseDate)
	var endsAt *time.Time
	if transaction.ExpiresDate > 0 {
		value := milliseconds(transaction.ExpiresDate)
		endsAt = &value
	}
	renewal := core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable}
	if kind == core.ProductKindSubscription {
		if subscription == nil {
			return stores.VerificationResult{}, invalid("normalize", errors.New("apple subscription status is required"))
		}
		state, access, reason, endsAt, renewal = normalizeSubscriptionStatus(
			transaction, *subscription, observedAt,
		)
	}
	quantity := transaction.Quantity
	if quantity == 0 {
		quantity = 1
	}
	ownership := core.OwnershipUnknown
	switch transaction.OwnershipType {
	case applePurchasedOwnership, "":
		ownership = core.OwnershipPurchased
	case appleFamilySharedOwnership:
		ownership = core.OwnershipFamilyShared
	}
	references := make([]core.StoreReference, 0, 4)
	for _, candidate := range []struct {
		role  core.ReferenceRole
		kind  string
		value string
	}{
		{role: core.ReferenceTransaction, kind: "transaction_id", value: transaction.TransactionID},
		{role: core.ReferenceLineage, kind: "original_transaction_id", value: transaction.OriginalTransactionID},
		{role: core.ReferenceQuery, kind: "transaction_id", value: transaction.TransactionID},
		{role: core.ReferenceCustomerBinding, kind: "app_account_token", value: transaction.AppAccountToken},
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
	identity := observationSnapshotIdentity(application.ID, kind, transaction, state, access, reason, renewal, ownership, quantity)
	observation := core.PurchaseObservation{
		ID:              core.ObservationID("app-" + hex.EncodeToString(identity[:16])),
		ApplicationID:   application.ID,
		Store:           application.Store,
		ProductID:       core.ProviderProductID(transaction.ProductID),
		ProductKind:     kind,
		State:           state,
		ProviderState:   providerState(transaction, state),
		Access:          access,
		AccessReason:    reason,
		Ownership:       ownership,
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
		Artifacts:    []stores.VerifiedArtifact{{Kind: appleArtifactKind(kind), Evidence: artifact}},
		Observations: []core.PurchaseObservation{observation},
	}, nil
}

// parseEvidence decodes the submitted versioned Apple evidence envelope.
func parseEvidence(evidence stores.Evidence) (clientEvidence, error) {
	if evidence.ContentType != EvidenceContentType {
		return clientEvidence{}, errors.New("unsupported Apple evidence content type")
	}
	var envelope clientEvidence
	if err := decodeStrict(evidence.Bytes(), &envelope); err != nil {
		return clientEvidence{}, err
	}
	if envelope.SignedTransaction == "" {
		return clientEvidence{}, errors.New("apple signed transaction is required")
	}
	if envelope.ProductKind != core.ProductKindSubscription && envelope.ProductKind != core.ProductKindNonConsumable {
		return clientEvidence{}, errors.New("unsupported Apple product kind")
	}
	return envelope, nil
}

// validateTransactionScope checks application, environment, product, type, and customer binding claims.
func validateTransactionScope(
	request stores.VerificationRequest,
	bundleID string,
	expectedKind core.ProductKind,
	transaction transactionPayload,
) error {
	if transaction.TransactionID == "" || transaction.OriginalTransactionID == "" || transaction.ProductID == "" ||
		transaction.BundleID != bundleID || transaction.BundleID != string(request.Application.Store.ID) {
		return errors.New("apple transaction application or purchase identity mismatch")
	}
	expectedEnvironment, err := appleEnvironment(request.Application.Store.Environment)
	if err != nil || transaction.Environment != expectedEnvironment {
		return errors.New("apple transaction environment mismatch")
	}
	actualKind, err := productKind(transaction.Type)
	if err != nil || actualKind != expectedKind {
		return errors.New("apple transaction product kind mismatch")
	}
	if len(request.ClaimedProducts) > 0 && (len(request.ClaimedProducts) != 1 || string(request.ClaimedProducts[0]) != transaction.ProductID) {
		return errors.New("apple transaction product does not match claim")
	}
	if transaction.AppAccountToken == "" {
		return errors.New("apple app account token is required")
	}
	if !validAppAccountToken(transaction.AppAccountToken) {
		return errors.New("apple app account token must be a UUID")
	}
	matchedBinding := false
	for _, binding := range request.ExpectedCustomerBindings {
		if binding.Kind == "app_account_token" && binding.Value() == transaction.AppAccountToken {
			matchedBinding = true
			break
		}
	}
	if !matchedBinding {
		return errors.New("apple app account token customer binding mismatch")
	}
	return nil
}

// compareTransactions enforces stable purchase identity across submitted and authoritative JWS values.
func compareTransactions(submitted, authoritative transactionPayload) error {
	if submitted.TransactionID != authoritative.TransactionID ||
		submitted.OriginalTransactionID != authoritative.OriginalTransactionID ||
		submitted.BundleID != authoritative.BundleID ||
		submitted.ProductID != authoritative.ProductID ||
		submitted.AppAccountToken != authoritative.AppAccountToken {
		return errors.New("authoritative Apple transaction does not match submitted evidence")
	}
	return nil
}

// productKind maps supported Apple product type strings into the shared domain.
func productKind(value string) (core.ProductKind, error) {
	switch value {
	case appleSubscriptionType:
		return core.ProductKindSubscription, nil
	case appleNonConsumableType:
		return core.ProductKindNonConsumable, nil
	default:
		return "", fmt.Errorf("unsupported Apple product type %q", value)
	}
}

// normalizeState maps Apple transaction dates and revocations into shared access state.
func normalizeState(
	kind core.ProductKind,
	transaction transactionPayload,
	now time.Time,
) (core.LifecycleState, core.AccessStatus, core.AccessReason) {
	if transaction.RevocationDate > 0 {
		if transaction.OwnershipType == appleFamilySharedOwnership {
			return core.LifecycleRevoked, core.AccessDenied, core.AccessReasonRevoked
		}
		return core.LifecycleRefunded, core.AccessDenied, core.AccessReasonRefunded
	}
	if kind == core.ProductKindSubscription && (transaction.ExpiresDate <= 0 || !milliseconds(transaction.ExpiresDate).After(now)) {
		return core.LifecycleExpired, core.AccessDenied, core.AccessReasonExpired
	}
	return core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid
}

// providerState returns a stable non-sensitive Apple lifecycle label.
func providerState(transaction transactionPayload, state core.LifecycleState) string {
	if transaction.RevocationDate > 0 {
		return "revoked"
	}
	if transaction.IsUpgraded {
		return "upgraded"
	}
	return string(state)
}

// observationSnapshotIdentity hashes all authoritative fields that affect one immutable normalized snapshot.
func observationSnapshotIdentity(
	applicationID core.ApplicationID,
	kind core.ProductKind,
	transaction transactionPayload,
	state core.LifecycleState,
	access core.AccessStatus,
	reason core.AccessReason,
	renewal core.Renewal,
	ownership core.Ownership,
	quantity uint32,
) [sha256.Size]byte {
	revocationReason := ""
	if transaction.RevocationReason != nil {
		revocationReason = strconv.Itoa(*transaction.RevocationReason)
	}
	return sha256.Sum256([]byte(strings.Join([]string{
		string(applicationID), string(kind), transaction.OriginalTransactionID, transaction.TransactionID,
		transaction.WebOrderLineItemID, transaction.BundleID, transaction.ProductID,
		strconv.FormatInt(transaction.PurchaseDate, 10), strconv.FormatInt(transaction.ExpiresDate, 10),
		strconv.FormatUint(uint64(quantity), 10), transaction.Type, transaction.AppAccountToken,
		transaction.OwnershipType, revocationReason,
		strconv.FormatInt(transaction.RevocationDate, 10), strconv.FormatBool(transaction.IsUpgraded),
		transaction.Environment, string(state), string(access), string(reason), string(renewal.Mode),
		string(renewal.Status), string(renewal.NextProductID), optionalTimeIdentity(renewal.NextRenewalAt),
		string(ownership),
	}, "\x00")))
}

// optionalTimeIdentity encodes one optional UTC time without introducing wall-clock formatting differences.
func optionalTimeIdentity(value *time.Time) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(value.UTC().UnixMilli(), 10)
}

// appleEnvironment maps the normalized application environment to Apple's signed value.
func appleEnvironment(environment core.Environment) (string, error) {
	switch environment {
	case core.EnvironmentProduction:
		return "Production", nil
	case core.EnvironmentSandbox:
		return "Sandbox", nil
	default:
		return "", fmt.Errorf("unsupported Apple environment %q", environment)
	}
}

// validAppAccountToken reports whether StoreKit's customer binding is a canonical UUID value.
func validAppAccountToken(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	encoded := strings.ReplaceAll(value, "-", "")
	_, err := hex.DecodeString(encoded)
	return err == nil
}

// parsePrivateKey parses Apple's PKCS#8 P-256 private key format.
func parsePrivateKey(encoded string) (*ecdsa.PrivateKey, error) {
	block, remainder := pem.Decode([]byte(encoded))
	if block == nil || block.Type != "PRIVATE KEY" || strings.TrimSpace(string(remainder)) != "" {
		return nil, errors.New("apple private key must be one PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("apple private key is invalid PKCS#8")
	}
	privateKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || privateKey.Curve != elliptic.P256() {
		return nil, errors.New("apple private key must use P-256 ECDSA")
	}
	return privateKey, nil
}

// paddedInteger encodes one ES256 signature component at its fixed width.
func paddedInteger(value *big.Int, size int) []byte {
	result := make([]byte, size)
	value.FillBytes(result)
	return result
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

// invalid constructs a redacted non-retryable Apple evidence failure.
func invalid(operation string, cause error) error {
	return stores.NewFailure(core.ProviderAppleAppStore, operation, stores.FailureInvalidEvidence, 0, cause)
}

// zero overwrites temporary credential payload bytes after parsing.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
