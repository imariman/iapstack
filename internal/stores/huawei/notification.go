package huawei

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// NotificationContentType identifies normalized Huawei IAP V2 callbacks in the protected inbox.
	NotificationContentType = "application/vnd.iapstack.huawei-notification-v2+json"
	// notificationVersion is Huawei's supported direct callback contract version.
	notificationVersion = "v2"
	// orderEventType identifies one-time purchase lifecycle callbacks.
	orderEventType = "ORDER"
	// subscriptionEventType identifies subscription lifecycle callbacks.
	subscriptionEventType = "SUBSCRIPTION"
)

// NotificationEnvelope is the validated token-only signal stored in the protected inbox.
type NotificationEnvelope struct {
	Version           string                     `json:"version"`
	EventType         string                     `json:"event_type"`
	NotifyTime        time.Time                  `json:"notify_time"`
	ApplicationID     core.ProviderApplicationID `json:"application_id"`
	NotificationType  int                        `json:"notification_type"`
	PurchaseToken     string                     `json:"purchase_token"`
	ProviderProductID core.ProviderProductID     `json:"provider_product_id"`
	ProductKind       core.ProductKind           `json:"product_kind"`
}

// notificationRequest mirrors Huawei's native IAP V2 callback wrapper.
type notificationRequest struct {
	Version                  string                    `json:"version"`
	EventType                string                    `json:"eventType"`
	NotifyTime               int64                     `json:"notifyTime"`
	ApplicationID            string                    `json:"applicationId"`
	OrderNotification        *orderNotification        `json:"orderNotification"`
	SubscriptionNotification *subscriptionNotification `json:"subNotification"`
}

// orderNotification is Huawei's V2 one-time-order lifecycle signal.
type orderNotification struct {
	Version          string `json:"version"`
	NotificationType int    `json:"notificationType"`
	PurchaseToken    string `json:"purchaseToken"`
	ProductID        string `json:"productId"`
}

// subscriptionNotification wraps a signed Huawei subscription status update.
type subscriptionNotification struct {
	Version                  string `json:"version"`
	StatusUpdateNotification string `json:"statusUpdateNotification"`
	NotificationSignature    string `json:"notificationSignature"`
	SignatureAlgorithm       string `json:"signatureAlgorithm"`
}

// subscriptionStatusUpdate contains the signed lookup identity needed for reconciliation.
type subscriptionStatusUpdate struct {
	NotificationType int    `json:"notificationType"`
	SubscriptionID   string `json:"subscriptionId"`
	PurchaseToken    string `json:"purchaseToken"`
	ProductID        string `json:"productId"`
}

// ValidateNotification validates a native Huawei IAP V2 callback before durable acknowledgement.
func (adapter *Adapter) ValidateNotification(
	ctx context.Context,
	application core.Application,
	payload []byte,
) (NotificationEnvelope, error) {
	if err := application.Validate(); err != nil || application.Store.Provider != core.ProviderHuaweiAppGallery {
		return NotificationEnvelope{}, invalid("notification", errors.New("invalid Huawei application scope"))
	}
	var request notificationRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	if request.Version != notificationVersion || request.NotifyTime <= 0 ||
		request.ApplicationID != string(application.Store.ID) {
		return NotificationEnvelope{}, invalid("notification", errors.New("Huawei notification scope or version mismatch"))
	}
	if (request.OrderNotification == nil) == (request.SubscriptionNotification == nil) {
		return NotificationEnvelope{}, invalid("notification", errors.New("Huawei notification must contain exactly one event body"))
	}
	envelope := NotificationEnvelope{
		Version:       notificationVersion,
		EventType:     request.EventType,
		NotifyTime:    time.UnixMilli(request.NotifyTime).UTC(),
		ApplicationID: application.Store.ID,
	}
	switch request.EventType {
	case orderEventType:
		order := request.OrderNotification
		if order == nil || request.SubscriptionNotification != nil || order.Version != notificationVersion ||
			(order.NotificationType != 1 && order.NotificationType != 2) ||
			strings.TrimSpace(order.PurchaseToken) == "" || strings.TrimSpace(order.ProductID) == "" {
			return NotificationEnvelope{}, invalid("notification", errors.New("invalid Huawei order notification"))
		}
		envelope.NotificationType = order.NotificationType
		envelope.PurchaseToken = order.PurchaseToken
		envelope.ProviderProductID = core.ProviderProductID(order.ProductID)
		envelope.ProductKind = core.ProductKindNonConsumable
	case subscriptionEventType:
		subscription := request.SubscriptionNotification
		if subscription == nil || request.OrderNotification != nil || subscription.Version != notificationVersion ||
			strings.TrimSpace(subscription.StatusUpdateNotification) == "" ||
			strings.TrimSpace(subscription.NotificationSignature) == "" ||
			strings.TrimSpace(subscription.SignatureAlgorithm) == "" {
			return NotificationEnvelope{}, invalid("notification", errors.New("invalid Huawei subscription notification"))
		}
		_, publicKey, err := adapter.configuration(ctx, application)
		if err != nil {
			return NotificationEnvelope{}, err
		}
		if err := verifyNotificationSignature(
			publicKey,
			subscription.StatusUpdateNotification,
			subscription.NotificationSignature,
			subscription.SignatureAlgorithm,
		); err != nil {
			return NotificationEnvelope{}, invalid("notification", err)
		}
		var update subscriptionStatusUpdate
		if err := json.Unmarshal([]byte(subscription.StatusUpdateNotification), &update); err != nil {
			return NotificationEnvelope{}, invalid("notification", err)
		}
		if update.NotificationType <= 0 || strings.TrimSpace(update.SubscriptionID) == "" ||
			strings.TrimSpace(update.PurchaseToken) == "" || strings.TrimSpace(update.ProductID) == "" {
			return NotificationEnvelope{}, invalid("notification", errors.New("invalid Huawei subscription status update"))
		}
		envelope.NotificationType = update.NotificationType
		envelope.PurchaseToken = update.PurchaseToken
		envelope.ProviderProductID = core.ProviderProductID(update.ProductID)
		envelope.ProductKind = core.ProductKindSubscription
	default:
		return NotificationEnvelope{}, invalid("notification", errors.New("unsupported Huawei notification event type"))
	}
	if err := errors.Join(envelope.ProviderProductID.Validate(), envelope.ProductKind.Validate()); err != nil {
		return NotificationEnvelope{}, invalid("notification", err)
	}
	return envelope, nil
}

// verifyNotificationSignature supports Huawei's two documented SHA-256/RSA modes.
func verifyNotificationSignature(publicKey *rsa.PublicKey, data, encodedSignature, algorithm string) error {
	signature, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil {
		return errors.New("Huawei notification signature is not valid base64")
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
		return errors.New("unsupported Huawei notification signature algorithm")
	}
	if err != nil {
		return errors.New("Huawei notification signature verification failed")
	}
	return nil
}
