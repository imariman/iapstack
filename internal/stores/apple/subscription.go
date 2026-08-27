package apple

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// subscriptionStatusActive identifies a currently active auto-renewable subscription.
	subscriptionStatusActive = 1
	// subscriptionStatusExpired identifies a subscription outside its service period.
	subscriptionStatusExpired = 2
	// subscriptionStatusBillingRetry identifies a subscription in billing retry without access.
	subscriptionStatusBillingRetry = 3
	// subscriptionStatusGracePeriod identifies temporary access during billing recovery.
	subscriptionStatusGracePeriod = 4
	// subscriptionStatusRevoked identifies service removed by the App Store.
	subscriptionStatusRevoked = 5
	// autoRenewDisabled identifies an App Store subscription that will not renew.
	autoRenewDisabled = 0
	// autoRenewEnabled identifies an App Store subscription scheduled to renew.
	autoRenewEnabled = 1
)

// statusResponse is Apple's Get All Subscription Statuses response contract.
type statusResponse struct {
	Environment string                            `json:"environment"`
	BundleID    string                            `json:"bundleId"`
	AppAppleID  uint64                            `json:"appAppleId,omitempty"`
	Data        []subscriptionGroupIdentifierItem `json:"data"`
}

// subscriptionGroupIdentifierItem groups the latest transaction per original purchase lineage.
type subscriptionGroupIdentifierItem struct {
	SubscriptionGroupIdentifier string                 `json:"subscriptionGroupIdentifier"`
	LastTransactions            []lastTransactionsItem `json:"lastTransactions"`
}

// lastTransactionsItem carries independently signed transaction and renewal snapshots.
type lastTransactionsItem struct {
	OriginalTransactionID string `json:"originalTransactionId"`
	Status                int    `json:"status"`
	SignedRenewalInfo     string `json:"signedRenewalInfo"`
	SignedTransactionInfo string `json:"signedTransactionInfo"`
}

// renewalPayload contains fields that affect renewal state and scope binding.
type renewalPayload struct {
	AppAccountToken        string `json:"appAccountToken"`
	AutoRenewProductID     string `json:"autoRenewProductId"`
	AutoRenewStatus        int    `json:"autoRenewStatus"`
	Environment            string `json:"environment"`
	ExpirationIntent       int    `json:"expirationIntent"`
	GracePeriodExpiresDate int64  `json:"gracePeriodExpiresDate"`
	IsInBillingRetryPeriod bool   `json:"isInBillingRetryPeriod"`
	OriginalTransactionID  string `json:"originalTransactionId"`
	ProductID              string `json:"productId"`
	RenewalDate            int64  `json:"renewalDate"`
	SignedDate             int64  `json:"signedDate"`
}

// subscriptionSnapshot combines one authoritative status with its verified renewal claims.
type subscriptionSnapshot struct {
	Status  int
	Renewal renewalPayload
}

// querySubscriptionStatus fetches and verifies the latest transaction and renewal for one lineage.
func (adapter *Adapter) querySubscriptionStatus(
	ctx context.Context,
	application core.Application,
	configuration configuration,
	originalTransactionID string,
) (transactionPayload, renewalPayload, int, stores.Evidence, error) {
	if strings.TrimSpace(originalTransactionID) == "" {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{},
			invalid("subscription_status", errors.New("Apple original transaction ID is required"))
	}
	token, err := adapter.authorizationToken(configuration)
	if err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{},
			stores.NewFailure(adapter.Provider(), "token", stores.FailurePermanent, 0, err)
	}
	baseURL := adapter.productionURL
	if application.Store.Environment == core.EnvironmentSandbox {
		baseURL = adapter.sandboxURL
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/inApps/v1/subscriptions/" + url.PathEscape(originalTransactionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{},
			stores.NewFailure(adapter.Provider(), "subscription_status", stores.FailurePermanent, 0, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	body, status, responseHeader, err := adapter.do(request)
	if err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{}, err
	}
	if err := adapter.classifyProviderStatus("subscription_status", status, responseHeader); err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{}, err
	}
	var response statusResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{},
			invalid("subscription_status", errors.New("Apple subscription status response is invalid"))
	}
	if err := validateStatusResponseScope(application, configuration, response); err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{}, invalid("subscription_status", err)
	}
	item, err := selectSubscriptionStatus(response, originalTransactionID)
	if err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{}, invalid("subscription_status", err)
	}
	transaction, err := verifyTransactionJWS(item.SignedTransactionInfo, configuration.roots)
	if err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{}, invalid("subscription_status", err)
	}
	renewal, err := verifyRenewalJWS(item.SignedRenewalInfo, configuration.roots)
	if err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{}, invalid("subscription_status", err)
	}
	artifact, err := stores.NewEvidence("application/json", body)
	if err != nil {
		return transactionPayload{}, renewalPayload{}, 0, stores.Evidence{}, invalid("subscription_status", err)
	}
	return transaction, renewal, item.Status, artifact, nil
}

// classifyProviderStatus maps bounded App Store Server API responses into retry-safe failures.
func (adapter *Adapter) classifyProviderStatus(operation string, status int, header http.Header) error {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailureUnauthorized, 0, nil)
	case status == http.StatusNotFound:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailureNotFound, 0, nil)
	case status == http.StatusTooManyRequests:
		return stores.NewFailure(
			adapter.Provider(), operation, stores.FailureRateLimited,
			retryAfter(header, adapter.clock()), nil,
		)
	case status >= http.StatusInternalServerError:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailureTemporary, 0, nil)
	default:
		return stores.NewFailure(adapter.Provider(), operation, stores.FailurePermanent, 0, nil)
	}
}

// validateStatusResponseScope checks App Store application and environment response fields.
func validateStatusResponseScope(
	application core.Application,
	configuration configuration,
	response statusResponse,
) error {
	environment, err := appleEnvironment(application.Store.Environment)
	if err != nil || response.Environment != environment || response.BundleID != configuration.bundleID {
		return errors.New("Apple subscription status application scope mismatch")
	}
	if application.Store.Environment == core.EnvironmentProduction && response.AppAppleID != configuration.appAppleID {
		return errors.New("Apple subscription status App Apple ID mismatch")
	}
	return nil
}

// selectSubscriptionStatus finds exactly one latest row for the requested original transaction.
func selectSubscriptionStatus(response statusResponse, originalTransactionID string) (lastTransactionsItem, error) {
	var selected lastTransactionsItem
	matches := 0
	for _, group := range response.Data {
		for _, item := range group.LastTransactions {
			if item.OriginalTransactionID != originalTransactionID {
				continue
			}
			selected = item
			matches++
		}
	}
	if matches != 1 || selected.SignedTransactionInfo == "" || selected.SignedRenewalInfo == "" ||
		selected.Status < subscriptionStatusActive || selected.Status > subscriptionStatusRevoked {
		return lastTransactionsItem{}, errors.New("Apple subscription status row is missing or ambiguous")
	}
	return selected, nil
}

// validateRenewalScope binds verified renewal claims to the latest transaction and application.
func validateRenewalScope(
	application core.Application,
	transaction transactionPayload,
	renewal renewalPayload,
) error {
	environment, err := appleEnvironment(application.Store.Environment)
	if err != nil || renewal.Environment != environment ||
		renewal.OriginalTransactionID != transaction.OriginalTransactionID ||
		renewal.ProductID != transaction.ProductID ||
		renewal.AppAccountToken != transaction.AppAccountToken {
		return errors.New("Apple renewal scope mismatch")
	}
	if renewal.AutoRenewStatus != autoRenewDisabled && renewal.AutoRenewStatus != autoRenewEnabled {
		return errors.New("Apple renewal status is unsupported")
	}
	return nil
}

// normalizeSubscriptionStatus maps Apple's five subscription states into safe shared access.
func normalizeSubscriptionStatus(
	transaction transactionPayload,
	subscription subscriptionSnapshot,
	now time.Time,
) (core.LifecycleState, core.AccessStatus, core.AccessReason, *time.Time, core.Renewal) {
	endsAt := optionalMilliseconds(transaction.ExpiresDate)
	renewal := normalizeRenewal(subscription.Renewal, now)
	if transaction.RevocationDate > 0 {
		state, access, reason := normalizeState(core.ProductKindSubscription, transaction, now)
		return state, access, reason, endsAt, renewal
	}
	switch subscription.Status {
	case subscriptionStatusActive:
		if endsAt == nil || !endsAt.After(now) {
			return core.LifecycleUnresolved, core.AccessUnresolved, core.AccessReasonUnresolved, endsAt, renewal
		}
		if renewal.Status == core.RenewalDisabled {
			return core.LifecycleCanceled, core.AccessAllowed, core.AccessReasonCanceledAtPeriodEnd, endsAt, renewal
		}
		return core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid, endsAt, renewal
	case subscriptionStatusExpired:
		return core.LifecycleExpired, core.AccessDenied, core.AccessReasonExpired, endsAt, renewal
	case subscriptionStatusBillingRetry:
		return core.LifecycleOnHold, core.AccessDenied, core.AccessReasonBillingIssue, endsAt, renewal
	case subscriptionStatusGracePeriod:
		graceEndsAt := optionalMilliseconds(subscription.Renewal.GracePeriodExpiresDate)
		if graceEndsAt == nil || !graceEndsAt.After(now) {
			return core.LifecycleOnHold, core.AccessDenied, core.AccessReasonBillingIssue, graceEndsAt, renewal
		}
		return core.LifecycleGracePeriod, core.AccessAllowed, core.AccessReasonGracePeriod, graceEndsAt, renewal
	case subscriptionStatusRevoked:
		return core.LifecycleRevoked, core.AccessDenied, core.AccessReasonRevoked, endsAt, renewal
	default:
		return core.LifecycleUnresolved, core.AccessUnresolved, core.AccessReasonUnresolved, endsAt, renewal
	}
}

// normalizeRenewal maps verified App Store renewal claims into the shared renewal projection.
func normalizeRenewal(payload renewalPayload, now time.Time) core.Renewal {
	status := core.RenewalDisabled
	if payload.AutoRenewStatus == autoRenewEnabled {
		status = core.RenewalEnabled
	}
	renewal := core.Renewal{Mode: core.RenewalAuto, Status: status}
	if status == core.RenewalEnabled && payload.AutoRenewProductID != "" {
		renewal.NextProductID = core.ProviderProductID(payload.AutoRenewProductID)
	}
	if status == core.RenewalEnabled && payload.RenewalDate > 0 {
		next := milliseconds(payload.RenewalDate)
		if next.After(now) {
			renewal.NextRenewalAt = &next
		}
	}
	return renewal
}

// optionalMilliseconds returns nil for absent provider timestamps.
func optionalMilliseconds(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	converted := milliseconds(value)
	return &converted
}

// appleArtifactKind identifies the authoritative response used for one normalized product.
func appleArtifactKind(kind core.ProductKind) string {
	if kind == core.ProductKindSubscription {
		return "apple_subscription_status"
	}
	return "apple_server_transaction"
}
