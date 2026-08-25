package googleplay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cloud.google.com/go/auth/credentials/idtoken"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// NotificationContentType identifies the protected normalized Google Play RTDN representation.
	NotificationContentType = "application/vnd.iapstack.google-play-rtdn+json"

	// NotificationKindSubscription identifies a Google Play subscription lifecycle signal.
	NotificationKindSubscription NotificationKind = "subscription"
	// NotificationKindOneTimeProduct identifies a Google Play one-time product lifecycle signal.
	NotificationKindOneTimeProduct NotificationKind = "one_time_product"
	// NotificationKindVoidedPurchase identifies a Google Play voided purchase signal.
	NotificationKindVoidedPurchase NotificationKind = "voided_purchase"
	// NotificationKindPendingRefundReview identifies a chargeback review signal without an entitlement transition.
	NotificationKindPendingRefundReview NotificationKind = "pending_refund_review"
	// NotificationKindTest identifies a Google Play Console delivery test.
	NotificationKindTest NotificationKind = "test"

	// developerNotificationVersion is the current documented RTDN envelope version.
	developerNotificationVersion = "1.0"
	// voidedProductTypeSubscription identifies a voided Google Play subscription.
	voidedProductTypeSubscription = 1
	// voidedProductTypeOneTime identifies a voided Google Play one-time product.
	voidedProductTypeOneTime = 2
	// maximumNotificationData bounds decoded Pub/Sub message data independently of the HTTP body limit.
	maximumNotificationData = 1 << 20
	// maximumNotificationText bounds identifiers and opaque values accepted from Pub/Sub.
	maximumNotificationText = 4096
	// maximumNotificationTokenClockSkew permits bounded signer and receiver clock drift.
	maximumNotificationTokenClockSkew = 5 * time.Minute
	// googleTokenIssuer identifies Google's canonical OIDC issuer URL.
	googleTokenIssuer = "https://accounts.google.com"
	// googleTokenIssuerAlias identifies Google's documented issuer alias.
	googleTokenIssuerAlias = "accounts.google.com"
)

// NotificationKind identifies one mutually exclusive Google Play developer notification payload.
type NotificationKind string

// NotificationEnvelope is the validated minimal RTDN payload retained in the protected inbox.
type NotificationEnvelope struct {
	MessageID         string                 `json:"message_id"`
	Kind              NotificationKind       `json:"kind"`
	NotificationType  int                    `json:"notification_type,omitempty"`
	EventTime         time.Time              `json:"event_time"`
	PurchaseToken     string                 `json:"purchase_token,omitempty"`
	ProductKind       core.ProductKind       `json:"product_kind,omitempty"`
	ProviderProductID core.ProviderProductID `json:"provider_product_id,omitempty"`
}

// rtdnConfiguration binds one application to its authenticated Pub/Sub push subscription.
type rtdnConfiguration struct {
	Subscription            string `json:"subscription"`
	PushServiceAccountEmail string `json:"push_service_account_email"`
	Audience                string `json:"audience"`
}

// notificationTokenClaims contains the additional Pub/Sub identity claims checked after signature validation.
type notificationTokenClaims struct {
	Email         string
	EmailVerified bool
}

// notificationTokenValidator verifies one Google-issued OIDC token for an expected audience.
type notificationTokenValidator interface {
	// Validate verifies signature, issuer, expiry, and audience before returning additional claims.
	Validate(context.Context, string, string) (notificationTokenClaims, error)
}

// googleNotificationTokenValidator adapts Google's supported ID-token verifier.
type googleNotificationTokenValidator struct {
	validator *idtoken.Validator
}

// pubSubPushEnvelope contains the documented wrapped Pub/Sub push request.
type pubSubPushEnvelope struct {
	Message         pubSubMessage `json:"message"`
	Subscription    string        `json:"subscription"`
	DeliveryAttempt int           `json:"deliveryAttempt,omitempty"`
}

// pubSubMessage contains the encoded RTDN bytes and delivery identity.
type pubSubMessage struct {
	Attributes  map[string]string `json:"attributes,omitempty"`
	Data        string            `json:"data"`
	MessageID   string            `json:"messageId"`
	OrderingKey string            `json:"orderingKey,omitempty"`
	PublishTime time.Time         `json:"publishTime,omitempty"`
}

// developerNotification contains every mutually exclusive RTDN payload documented for Google Play.
type developerNotification struct {
	Version                         string                           `json:"version"`
	PackageName                     string                           `json:"packageName"`
	EventTimeMillis                 unixMilliseconds                 `json:"eventTimeMillis"`
	SubscriptionNotification        *subscriptionNotification        `json:"subscriptionNotification,omitempty"`
	OneTimeProductNotification      *oneTimeProductNotification      `json:"oneTimeProductNotification,omitempty"`
	VoidedPurchaseNotification      *voidedPurchaseNotification      `json:"voidedPurchaseNotification,omitempty"`
	PendingRefundReviewNotification *pendingRefundReviewNotification `json:"pendingRefundReviewNotification,omitempty"`
	TestNotification                *testNotification                `json:"testNotification,omitempty"`
}

// subscriptionNotification identifies one subscription token whose authoritative state changed.
type subscriptionNotification struct {
	Version          string `json:"version"`
	NotificationType int    `json:"notificationType"`
	PurchaseToken    string `json:"purchaseToken"`
}

// oneTimeProductNotification identifies one one-time product token whose authoritative state changed.
type oneTimeProductNotification struct {
	Version          string `json:"version"`
	NotificationType int    `json:"notificationType"`
	PurchaseToken    string `json:"purchaseToken"`
	SKU              string `json:"sku"`
}

// voidedPurchaseNotification identifies a fully or partially voided provider transaction.
type voidedPurchaseNotification struct {
	PurchaseToken string `json:"purchaseToken"`
	OrderID       string `json:"orderId"`
	ProductType   int    `json:"productType"`
	RefundType    int    `json:"refundType"`
}

// pendingRefundReviewNotification contains a chargeback review request that does not yet change entitlement state.
type pendingRefundReviewNotification struct {
	Version             string `json:"version"`
	PendingRefundToken  string `json:"pendingRefundToken"`
	OrderID             string `json:"orderId"`
	RefundReason        int    `json:"refundReason"`
	ObfuscatedAccountID string `json:"obfuscatedAccountId,omitempty"`
	ObfuscatedProfileID string `json:"obfuscatedProfileId,omitempty"`
}

// testNotification identifies one Google Play Console delivery test.
type testNotification struct {
	Version string `json:"version"`
}

// unixMilliseconds accepts the documented JSON string or number representation of epoch milliseconds.
type unixMilliseconds struct {
	value int64
}

var (
	// ErrNotificationUnauthorized indicates that Pub/Sub push authentication did not match application configuration.
	ErrNotificationUnauthorized = errors.New("Google Play notification authentication failed")
	// ErrNotificationInvalid indicates that a Pub/Sub or RTDN envelope was malformed or outside application scope.
	ErrNotificationInvalid = errors.New("invalid Google Play notification")
)

// newGoogleNotificationTokenValidator constructs Google's verifier with the adapter's bounded HTTP client.
func newGoogleNotificationTokenValidator(client *http.Client) (notificationTokenValidator, error) {
	validator, err := idtoken.NewValidator(&idtoken.ValidatorOptions{Client: client})
	if err != nil {
		return nil, err
	}
	return googleNotificationTokenValidator{validator: validator}, nil
}

// Validate verifies one Google-issued OIDC token and extracts the Pub/Sub service-account identity.
func (validator googleNotificationTokenValidator) Validate(
	ctx context.Context,
	token string,
	audience string,
) (notificationTokenClaims, error) {
	payload, err := validator.validator.Validate(ctx, token, audience)
	if err != nil {
		return notificationTokenClaims{}, err
	}
	if (payload.Issuer != googleTokenIssuer && payload.Issuer != googleTokenIssuerAlias) ||
		payload.Subject == "" || payload.IssuedAt <= 0 ||
		payload.IssuedAt > time.Now().Add(maximumNotificationTokenClockSkew).Unix() {
		return notificationTokenClaims{}, errors.New("Google notification token claims are invalid")
	}
	email, _ := payload.Claims["email"].(string)
	emailVerified, _ := payload.Claims["email_verified"].(bool)
	return notificationTokenClaims{Email: email, EmailVerified: emailVerified}, nil
}

// ValidateNotification authenticates and normalizes one wrapped Google Play Pub/Sub push request.
func (adapter *Adapter) ValidateNotification(
	ctx context.Context,
	application core.Application,
	bearerToken string,
	payload []byte,
) (NotificationEnvelope, error) {
	if err := application.Validate(); err != nil || application.Store.Provider != core.ProviderGooglePlay {
		return NotificationEnvelope{}, notificationInvalid()
	}
	configuration, err := adapter.configuration(ctx, application)
	if err != nil {
		return NotificationEnvelope{}, err
	}
	if configuration.rtdn == nil || configuration.rtdn.Validate() != nil {
		return NotificationEnvelope{}, stores.NewFailure(adapter.Provider(), "notification_config", stores.FailurePermanent, 0, nil)
	}
	if strings.TrimSpace(bearerToken) == "" || adapter.notificationTokens == nil {
		return NotificationEnvelope{}, ErrNotificationUnauthorized
	}
	claims, err := adapter.notificationTokens.Validate(ctx, bearerToken, configuration.rtdn.Audience)
	if err != nil || !claims.EmailVerified || claims.Email != configuration.rtdn.PushServiceAccountEmail {
		return NotificationEnvelope{}, ErrNotificationUnauthorized
	}
	return decodeNotification(application, *configuration.rtdn, payload)
}

// VerificationEvidence converts one lifecycle RTDN into provider evidence without trusting its state claim.
func (notification NotificationEnvelope) VerificationEvidence() (json.RawMessage, bool, error) {
	if err := notification.Validate(); err != nil {
		return nil, false, err
	}
	if notification.Kind == NotificationKindTest || notification.Kind == NotificationKindPendingRefundReview {
		return nil, false, nil
	}
	payload, err := json.Marshal(clientEvidence{
		PurchaseToken: notification.PurchaseToken,
		ProductKind:   notification.ProductKind,
	})
	if err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

// Validate checks the normalized notification identity and lifecycle lookup fields.
func (notification NotificationEnvelope) Validate() error {
	if err := validateNotificationText("message ID", notification.MessageID); err != nil {
		return err
	}
	if notification.EventTime.IsZero() {
		return errors.New("notification event time is required")
	}
	switch notification.Kind {
	case NotificationKindSubscription:
		if err := validateNotificationText("purchase token", notification.PurchaseToken); err != nil {
			return err
		}
		if notification.ProductKind != core.ProductKindSubscription || notification.NotificationType <= 0 ||
			notification.ProviderProductID != "" {
			return errors.New("subscription notification lookup data is invalid")
		}
	case NotificationKindOneTimeProduct:
		if err := validateNotificationText("purchase token", notification.PurchaseToken); err != nil {
			return err
		}
		if notification.ProductKind != core.ProductKindNonConsumable || notification.NotificationType <= 0 ||
			notification.ProviderProductID == "" {
			return errors.New("one-time notification lookup data is invalid")
		}
	case NotificationKindVoidedPurchase:
		if err := validateNotificationText("purchase token", notification.PurchaseToken); err != nil {
			return err
		}
		if notification.ProductKind != core.ProductKindSubscription && notification.ProductKind != core.ProductKindNonConsumable {
			return errors.New("voided notification product kind is unsupported")
		}
	case NotificationKindPendingRefundReview, NotificationKindTest:
		if notification.NotificationType != 0 || notification.PurchaseToken != "" ||
			notification.ProductKind != "" || notification.ProviderProductID != "" {
			return errors.New("non-lifecycle notification contains purchase lookup data")
		}
	default:
		return errors.New("notification kind is unsupported")
	}
	if notification.ProviderProductID != "" {
		return notification.ProviderProductID.Validate()
	}
	return nil
}

// Validate checks the RTDN subscription, audience, and push service-account identity.
func (configuration rtdnConfiguration) Validate() error {
	if err := validateNotificationText("RTDN subscription", configuration.Subscription); err != nil {
		return err
	}
	parts := strings.Split(configuration.Subscription, "/")
	if len(parts) != 4 || parts[0] != "projects" || parts[1] == "" ||
		parts[2] != "subscriptions" || parts[3] == "" {
		return errors.New("RTDN subscription must be a Pub/Sub subscription resource name")
	}
	if err := validateNotificationText("RTDN push service account email", configuration.PushServiceAccountEmail); err != nil {
		return err
	}
	if !strings.Contains(configuration.PushServiceAccountEmail, "@") {
		return errors.New("RTDN push service account email is invalid")
	}
	return validateNotificationText("RTDN audience", configuration.Audience)
}

// UnmarshalJSON decodes an exact positive epoch-millisecond string or number.
func (value *unixMilliseconds) UnmarshalJSON(payload []byte) error {
	var encoded string
	if len(payload) > 0 && payload[0] == '"' {
		if err := json.Unmarshal(payload, &encoded); err != nil {
			return err
		}
	} else {
		encoded = string(payload)
	}
	milliseconds, err := strconv.ParseInt(encoded, 10, 64)
	if err != nil || milliseconds <= 0 {
		return errors.New("eventTimeMillis must be a positive integer")
	}
	value.value = milliseconds
	return nil
}

// decodeNotification validates the Pub/Sub wrapper and normalizes exactly one developer notification payload.
func decodeNotification(
	application core.Application,
	configuration rtdnConfiguration,
	payload []byte,
) (NotificationEnvelope, error) {
	var push pubSubPushEnvelope
	if err := decodeStrict(payload, &push); err != nil ||
		push.Subscription != configuration.Subscription ||
		validateNotificationText("Pub/Sub message ID", push.Message.MessageID) != nil ||
		strings.TrimSpace(push.Message.Data) == "" {
		return NotificationEnvelope{}, notificationInvalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(push.Message.Data)
	if err != nil || len(data) == 0 || len(data) > maximumNotificationData {
		return NotificationEnvelope{}, notificationInvalid()
	}
	defer zero(data)
	var notification developerNotification
	if err := decodeStrict(data, &notification); err != nil ||
		notification.Version != developerNotificationVersion ||
		notification.PackageName != string(application.Store.ID) ||
		notification.EventTimeMillis.value <= 0 {
		return NotificationEnvelope{}, notificationInvalid()
	}
	result := NotificationEnvelope{
		MessageID: push.Message.MessageID,
		EventTime: time.UnixMilli(notification.EventTimeMillis.value).UTC(),
	}
	if err := normalizeDeveloperNotification(notification, &result); err != nil {
		return NotificationEnvelope{}, notificationInvalid()
	}
	if err := result.Validate(); err != nil {
		return NotificationEnvelope{}, notificationInvalid()
	}
	return result, nil
}

// normalizeDeveloperNotification enforces mutual exclusivity and maps one RTDN variant into the protected envelope.
func normalizeDeveloperNotification(notification developerNotification, result *NotificationEnvelope) error {
	variantCount := 0
	if value := notification.SubscriptionNotification; value != nil {
		variantCount++
		if value.Version != developerNotificationVersion || value.NotificationType <= 0 ||
			validateNotificationText("subscription purchase token", value.PurchaseToken) != nil {
			return ErrNotificationInvalid
		}
		result.Kind = NotificationKindSubscription
		result.NotificationType = value.NotificationType
		result.PurchaseToken = value.PurchaseToken
		result.ProductKind = core.ProductKindSubscription
	}
	if value := notification.OneTimeProductNotification; value != nil {
		variantCount++
		if value.Version != developerNotificationVersion || value.NotificationType <= 0 ||
			validateNotificationText("one-time purchase token", value.PurchaseToken) != nil ||
			validateNotificationText("one-time product SKU", value.SKU) != nil {
			return ErrNotificationInvalid
		}
		result.Kind = NotificationKindOneTimeProduct
		result.NotificationType = value.NotificationType
		result.PurchaseToken = value.PurchaseToken
		result.ProductKind = core.ProductKindNonConsumable
		result.ProviderProductID = core.ProviderProductID(value.SKU)
	}
	if value := notification.VoidedPurchaseNotification; value != nil {
		variantCount++
		if validateNotificationText("voided purchase token", value.PurchaseToken) != nil ||
			validateNotificationText("voided order ID", value.OrderID) != nil || value.RefundType <= 0 {
			return ErrNotificationInvalid
		}
		result.Kind = NotificationKindVoidedPurchase
		result.PurchaseToken = value.PurchaseToken
		switch value.ProductType {
		case voidedProductTypeSubscription:
			result.ProductKind = core.ProductKindSubscription
		case voidedProductTypeOneTime:
			result.ProductKind = core.ProductKindNonConsumable
		default:
			return ErrNotificationInvalid
		}
	}
	if value := notification.PendingRefundReviewNotification; value != nil {
		variantCount++
		if value.Version != developerNotificationVersion || value.RefundReason <= 0 ||
			validateNotificationText("pending refund token", value.PendingRefundToken) != nil ||
			validateNotificationText("pending refund order ID", value.OrderID) != nil {
			return ErrNotificationInvalid
		}
		result.Kind = NotificationKindPendingRefundReview
	}
	if value := notification.TestNotification; value != nil {
		variantCount++
		if value.Version != developerNotificationVersion {
			return ErrNotificationInvalid
		}
		result.Kind = NotificationKindTest
	}
	if variantCount != 1 {
		return ErrNotificationInvalid
	}
	return nil
}

// validateNotificationText bounds provider-controlled text and rejects whitespace or control characters.
func validateNotificationText(name, value string) error {
	if value == "" {
		return errors.New(name + " is required")
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || len(value) > maximumNotificationText {
		return errors.New(name + " is invalid")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return errors.New(name + " is invalid")
		}
	}
	return nil
}

// notificationInvalid returns a stable error without retaining provider-controlled details.
func notificationInvalid() error {
	return ErrNotificationInvalid
}
