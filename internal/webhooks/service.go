// Package webhooks protects application signing secrets and delivers deterministic signed outbox events.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// signingSecretPurpose binds webhook secrets to their application scope.
	signingSecretPurpose = "webhook_signing_secret"
	// minimumSigningSecretBytes prevents trivially brute-forced HMAC keys.
	minimumSigningSecretBytes = 32
	// maximumResponseBytes bounds discarded application response data.
	maximumResponseBytes int64 = 64 << 10
	// signatureVersion identifies the authenticated webhook signing contract.
	signatureVersion = "v2"
	// signatureMACSeparator unambiguously delimits versioned MAC fields.
	signatureMACSeparator = "\n"
)

// Service configures and delivers application webhooks.
type Service struct {
	store                persistence.OperationsStore
	protection           protection.Service
	client               *http.Client
	clock                func() time.Time
	allowPrivateNetworks bool
}

// DeliveryError classifies one safe webhook delivery failure.
type DeliveryError struct {
	Code     string
	CanRetry bool
}

// NewService assembles protected webhook configuration and bounded delivery dependencies.
func NewService(
	store persistence.OperationsStore,
	protectionService protection.Service,
	timeout time.Duration,
	allowPrivateNetworks bool,
) (*Service, error) {
	if store == nil || protectionService == nil {
		return nil, errors.New("webhook store and protection service are required")
	}
	if timeout <= 0 {
		return nil, errors.New("webhook timeout must be positive")
	}
	return &Service{
		store:                store,
		protection:           protectionService,
		client:               newWebhookClient(timeout, allowPrivateNetworks),
		clock:                time.Now,
		allowPrivateNetworks: allowPrivateNetworks,
	}, nil
}

// Configure protects and creates or rotates one application webhook signing secret.
func (service *Service) Configure(
	ctx context.Context,
	application core.Application,
	url string,
	secret []byte,
	expectedRevision int64,
) (persistence.WebhookEndpointRecord, error) {
	if err := application.Validate(); err != nil {
		return persistence.WebhookEndpointRecord{}, validation.Wrap(err)
	}
	if expectedRevision < 0 {
		return persistence.WebhookEndpointRecord{}, validation.Wrap(
			errors.New("webhook expected revision must not be negative"),
		)
	}
	if err := validateWebhookDestination(url, service.allowPrivateNetworks); err != nil {
		return persistence.WebhookEndpointRecord{}, validation.Wrap(err)
	}
	if len(secret) < minimumSigningSecretBytes {
		return persistence.WebhookEndpointRecord{}, validation.Wrap(
			errors.New("webhook signing secret must contain at least 32 bytes"),
		)
	}
	protected, err := protection.Protect(ctx, service.protection, webhookScope(application), secret)
	if err != nil {
		return persistence.WebhookEndpointRecord{}, fmt.Errorf("protect webhook secret: %w", err)
	}
	write := persistence.WebhookEndpointWrite{
		ProjectID:        application.ProjectID,
		ApplicationID:    application.ID,
		URL:              url,
		Secret:           protected,
		ExpectedRevision: expectedRevision,
	}
	var record persistence.WebhookEndpointRecord
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		authoritative, loadErr := repository.Application(ctx, application.ProjectID, application.ID)
		if loadErr != nil {
			return loadErr
		}
		if authoritative != application {
			return persistence.ErrConflict
		}
		record, loadErr = repository.PutWebhookEndpoint(ctx, write)
		return loadErr
	})
	if err != nil {
		return persistence.WebhookEndpointRecord{}, fmt.Errorf("configure webhook: %w", err)
	}
	return record, nil
}

// Deliver signs and sends one immutable outbox message to its scoped application endpoint.
func (service *Service) Deliver(ctx context.Context, message persistence.QueueMessage) error {
	if message.Queue != persistence.QueueOutbox || message.ID == "" || len(message.JSONPayload) == 0 {
		return &DeliveryError{Code: "invalid_outbox_message", CanRetry: false}
	}
	var endpoint persistence.WebhookEndpointRecord
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		endpoint, loadErr = repository.WebhookEndpoint(ctx, message.ProjectID, message.ApplicationID)
		return loadErr
	})
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			return &DeliveryError{Code: "webhook_not_configured", CanRetry: false}
		}
		return &DeliveryError{Code: "webhook_endpoint_unavailable", CanRetry: true}
	}
	if err := validateWebhookDestination(endpoint.URL, service.allowPrivateNetworks); err != nil {
		return &DeliveryError{Code: "webhook_destination_invalid", CanRetry: false}
	}
	openRequest, err := protection.NewOpenRequest(
		protection.Scope{
			ProjectID:     endpoint.ProjectID,
			ApplicationID: endpoint.ApplicationID,
			Purpose:       signingSecretPurpose,
		},
		endpoint.Secret,
	)
	if err != nil {
		return &DeliveryError{Code: "webhook_secret_invalid", CanRetry: false}
	}
	secret, err := service.protection.Open(ctx, openRequest)
	if err != nil {
		if errors.Is(err, protection.ErrOpenFailed) {
			return &DeliveryError{Code: "webhook_secret_invalid", CanRetry: false}
		}
		return &DeliveryError{Code: "webhook_secret_unavailable", CanRetry: true}
	}
	defer zero(secret)
	timestamp := service.clock().UTC().Truncate(time.Second)
	signature := Sign(secret, message.ID, timestamp, message.JSONPayload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, bytes.NewReader(message.JSONPayload))
	if err != nil {
		return &DeliveryError{Code: "webhook_request_invalid", CanRetry: false}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("IAPStack-Event-ID", message.ID)
	request.Header.Set("IAPStack-Timestamp", strconv.FormatInt(timestamp.Unix(), 10))
	request.Header.Set("IAPStack-Signature", signatureVersion+"="+signature)
	response, err := service.client.Do(request)
	if err != nil {
		return &DeliveryError{Code: "webhook_transport_error", CanRetry: true}
	}
	defer response.Body.Close()
	responseBytes, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes+1))
	if readErr != nil {
		return &DeliveryError{Code: "webhook_response_error", CanRetry: true}
	}
	if responseBytes > maximumResponseBytes {
		return &DeliveryError{Code: "webhook_response_too_large", CanRetry: false}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return &DeliveryError{Code: "webhook_retryable_status", CanRetry: true}
	}
	return &DeliveryError{Code: "webhook_rejected", CanRetry: false}
}

// Error returns the stable safe delivery failure code.
func (failure *DeliveryError) Error() string {
	if failure == nil || failure.Code == "" {
		return "webhook delivery failed"
	}
	return failure.Code
}

// Retryable reports whether another bounded delivery attempt is appropriate.
func (failure *DeliveryError) Retryable() bool {
	return failure != nil && failure.CanRetry
}

// CodeValue returns the stable safe delivery failure code.
func (failure *DeliveryError) CodeValue() string {
	if failure == nil || failure.Code == "" {
		return "webhook_delivery_failed"
	}
	return failure.Code
}

// Sign returns a lowercase HMAC-SHA-256 signature over the versioned webhook MAC input.
func Sign(secret []byte, eventID string, timestamp time.Time, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	writeSignatureMAC(mac, eventID, timestamp, body)
	return hex.EncodeToString(mac.Sum(nil))
}

// writeSignatureMAC writes the unambiguous v2 webhook MAC input.
func writeSignatureMAC(mac io.Writer, eventID string, timestamp time.Time, body []byte) {
	_, _ = io.WriteString(mac, signatureVersion)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, eventID)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, strconv.FormatInt(timestamp.Unix(), 10))
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = mac.Write(body)
}

// webhookScope binds one signing secret to its exact application identity.
func webhookScope(application core.Application) protection.Scope {
	return protection.Scope{
		ProjectID:     application.ProjectID,
		ApplicationID: application.ID,
		Purpose:       signingSecretPurpose,
	}
}

// zero overwrites temporary plaintext signing material after use.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
