package apple

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// NotificationContentType identifies the protected normalized App Store notification representation.
	NotificationContentType = "application/vnd.iapstack.app-store-server-notification-v2+json"
	// notificationVersion is the supported App Store Server Notifications contract version.
	notificationVersion = "2.0"
	// maximumNotificationIdentifier bounds public notification identifiers stored in the inbox.
	maximumNotificationIdentifier = 4096
	// maximumNotificationClockSkew permits bounded signer and receiver clock drift.
	maximumNotificationClockSkew = 5 * time.Minute
)

// NotificationEnvelope is the validated minimal App Store notification retained in the protected inbox.
type NotificationEnvelope struct {
	NotificationUUID   string                 `json:"notification_uuid"`
	NotificationType   string                 `json:"notification_type"`
	Subtype            string                 `json:"subtype,omitempty"`
	SignedAt           time.Time              `json:"signed_at"`
	ExternalCustomerID string                 `json:"external_customer_id,omitempty"`
	ProviderProductID  core.ProviderProductID `json:"provider_product_id,omitempty"`
	ProductKind        core.ProductKind       `json:"product_kind,omitempty"`
	SignedTransaction  string                 `json:"signed_transaction,omitempty"`
}

// notificationRequest is Apple's JSON POST wrapper containing one signed V2 payload.
type notificationRequest struct {
	SignedPayload string `json:"signedPayload"`
}

// notificationPayload contains App Store Server Notifications V2 routing and data fields.
type notificationPayload struct {
	NotificationType string            `json:"notificationType"`
	Subtype          string            `json:"subtype"`
	NotificationUUID string            `json:"notificationUUID"`
	Data             *notificationData `json:"data,omitempty"`
	Version          string            `json:"version"`
	SignedDate       int64             `json:"signedDate"`
}

// notificationData binds independently signed transaction and renewal data to one application.
type notificationData struct {
	AppAppleID            uint64 `json:"appAppleId,omitempty"`
	BundleID              string `json:"bundleId"`
	Environment           string `json:"environment"`
	SignedTransactionInfo string `json:"signedTransactionInfo,omitempty"`
	SignedRenewalInfo     string `json:"signedRenewalInfo,omitempty"`
	Status                int    `json:"status,omitempty"`
}

var (
	// ErrNotificationInvalid indicates that an App Store notification was malformed, untrusted, or outside scope.
	ErrNotificationInvalid = errors.New("invalid App Store server notification")
)

// ValidateNotification authenticates and normalizes one App Store Server Notifications V2 request.
func (adapter *Adapter) ValidateNotification(
	ctx context.Context,
	application core.Application,
	payload []byte,
) (NotificationEnvelope, error) {
	if err := application.Validate(); err != nil || application.Store.Provider != core.ProviderAppleAppStore {
		return NotificationEnvelope{}, ErrNotificationInvalid
	}
	configuration, err := adapter.configuration(ctx, application)
	if err != nil {
		return NotificationEnvelope{}, err
	}
	var request notificationRequest
	if err := decodeStrict(payload, &request); err != nil || request.SignedPayload == "" {
		return NotificationEnvelope{}, ErrNotificationInvalid
	}
	var decoded notificationPayload
	if err := verifyAppleJWS(request.SignedPayload, configuration.roots, &decoded); err != nil {
		return NotificationEnvelope{}, ErrNotificationInvalid
	}
	if decoded.Version != notificationVersion || !validAppAccountToken(decoded.NotificationUUID) ||
		!validNotificationIdentifier(decoded.NotificationType) ||
		milliseconds(decoded.SignedDate).After(adapter.clock().UTC().Add(maximumNotificationClockSkew)) {
		return NotificationEnvelope{}, ErrNotificationInvalid
	}
	envelope := NotificationEnvelope{
		NotificationUUID: decoded.NotificationUUID, NotificationType: decoded.NotificationType,
		Subtype: decoded.Subtype, SignedAt: milliseconds(decoded.SignedDate),
	}
	if decoded.Data == nil {
		return NotificationEnvelope{}, ErrNotificationInvalid
	}
	if err := validateNotificationDataScope(application, configuration, *decoded.Data); err != nil {
		return NotificationEnvelope{}, ErrNotificationInvalid
	}
	if decoded.Data.SignedTransactionInfo == "" {
		if err := envelope.Validate(); err != nil {
			return NotificationEnvelope{}, ErrNotificationInvalid
		}
		return envelope, nil
	}
	transaction, err := verifyTransactionJWS(decoded.Data.SignedTransactionInfo, configuration.roots)
	if err != nil || validateNotificationTransaction(application, transaction) != nil {
		return NotificationEnvelope{}, ErrNotificationInvalid
	}
	kind, err := productKind(transaction.Type)
	if err != nil {
		if decoded.Data.SignedRenewalInfo != "" {
			if _, verifyErr := verifyRenewalJWS(decoded.Data.SignedRenewalInfo, configuration.roots); verifyErr != nil {
				return NotificationEnvelope{}, ErrNotificationInvalid
			}
		}
		return envelope, nil
	}
	if decoded.Data.SignedRenewalInfo != "" {
		renewal, verifyErr := verifyRenewalJWS(decoded.Data.SignedRenewalInfo, configuration.roots)
		if verifyErr != nil ||
			validateRenewalScope(application, transaction, renewal) != nil {
			return NotificationEnvelope{}, ErrNotificationInvalid
		}
	}
	envelope.ExternalCustomerID = transaction.AppAccountToken
	envelope.ProviderProductID = core.ProviderProductID(transaction.ProductID)
	envelope.ProductKind = kind
	envelope.SignedTransaction = decoded.Data.SignedTransactionInfo
	if err := envelope.Validate(); err != nil {
		return NotificationEnvelope{}, ErrNotificationInvalid
	}
	return envelope, nil
}

// VerificationEvidence converts one lifecycle notification into evidence without trusting its state claim.
func (notification NotificationEnvelope) VerificationEvidence() (json.RawMessage, bool, error) {
	if err := notification.Validate(); err != nil {
		return nil, false, err
	}
	if notification.SignedTransaction == "" {
		return nil, false, nil
	}
	payload, err := json.Marshal(clientEvidence{
		SignedTransaction: notification.SignedTransaction,
		ProductKind:       notification.ProductKind,
	})
	if err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

// Validate checks the normalized notification identity and verification inputs.
func (notification NotificationEnvelope) Validate() error {
	if !validAppAccountToken(notification.NotificationUUID) ||
		!validNotificationIdentifier(notification.NotificationType) || notification.SignedAt.IsZero() {
		return errors.New("app store notification identity is invalid")
	}
	if notification.Subtype != "" && !validNotificationIdentifier(notification.Subtype) {
		return errors.New("app store notification subtype is invalid")
	}
	if notification.SignedTransaction == "" {
		if notification.ExternalCustomerID != "" || notification.ProviderProductID != "" || notification.ProductKind != "" {
			return errors.New("audit-only App Store notification contains purchase data")
		}
		return nil
	}
	if !validAppAccountToken(notification.ExternalCustomerID) || notification.ProviderProductID == "" ||
		(notification.ProductKind != core.ProductKindSubscription && notification.ProductKind != core.ProductKindNonConsumable) {
		return errors.New("app store notification verification data is invalid")
	}
	return notification.ProviderProductID.Validate()
}

// validateNotificationDataScope checks decoded App Store application and environment claims.
func validateNotificationDataScope(
	application core.Application,
	configuration configuration,
	data notificationData,
) error {
	environment, err := appleEnvironment(application.Store.Environment)
	if err != nil || data.BundleID != configuration.bundleID || data.Environment != environment {
		return ErrNotificationInvalid
	}
	if application.Store.Environment == core.EnvironmentProduction && data.AppAppleID != configuration.appAppleID {
		return ErrNotificationInvalid
	}
	if data.Status != 0 && (data.Status < subscriptionStatusActive || data.Status > subscriptionStatusRevoked) {
		return ErrNotificationInvalid
	}
	return nil
}

// validateNotificationTransaction checks signed transaction scope before creating worker inputs.
func validateNotificationTransaction(application core.Application, transaction transactionPayload) error {
	environment, err := appleEnvironment(application.Store.Environment)
	if err != nil || transaction.BundleID != string(application.Store.ID) ||
		transaction.Environment != environment || transaction.TransactionID == "" ||
		transaction.OriginalTransactionID == "" || transaction.ProductID == "" ||
		!validAppAccountToken(transaction.AppAccountToken) {
		return ErrNotificationInvalid
	}
	return nil
}

// validNotificationIdentifier rejects empty, oversized, whitespace-padded, or control-bearing text.
func validNotificationIdentifier(value string) bool {
	if value == "" || len(value) > maximumNotificationIdentifier || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
