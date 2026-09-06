// Package processing implements durable provider notification and reconciliation handlers.
package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/apple"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/stores/huawei"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// inboxProtectionPurpose authenticates protected notification payloads opened by workers.
	inboxProtectionPurpose = "inbox_notification"
	// reconciliationProtectionPurpose authenticates protected periodic verification payloads.
	reconciliationProtectionPurpose = "reconciliation_request"
	// providerReferencePurpose scopes deterministic lookup fingerprints exactly like verification persistence.
	providerReferencePurpose = "provider_reference"
	// reconciliationInterval schedules daily authoritative lifecycle refreshes.
	reconciliationInterval = 24 * time.Hour
)

// Service opens protected jobs, runs verification, and schedules the next lifecycle refresh.
type Service struct {
	store        persistence.OperationsStore
	protection   protection.Service
	verification *verification.Service
	clock        func() time.Time
}

// New validates and constructs durable provider processing handlers.
func New(
	store persistence.OperationsStore,
	protectionService protection.Service,
	verificationService *verification.Service,
) (*Service, error) {
	if store == nil || protectionService == nil || verificationService == nil {
		return nil, errors.New("processing dependencies are required")
	}
	return &Service{store: store, protection: protectionService, verification: verificationService, clock: time.Now}, nil
}

// HandleInbox opens one validated provider notification and runs authoritative verification when required.
func (service *Service) HandleInbox(ctx context.Context, message persistence.QueueMessage) error {
	if message.Queue != persistence.QueueInbox {
		return errors.New("unsupported inbox message")
	}
	payload, err := service.open(ctx, message, inboxProtectionPurpose)
	if err != nil {
		return err
	}
	defer zero(payload)
	switch message.Provider {
	case core.ProviderHuaweiAppGallery:
		return service.handleHuaweiInbox(ctx, message, payload)
	case core.ProviderAppleAppStore:
		return service.handleAppleInbox(ctx, message, payload)
	case core.ProviderGooglePlay:
		return service.handleGooglePlayInbox(ctx, message, payload)
	default:
		return errors.New("unsupported inbox provider")
	}
}

// handleAppleInbox re-queries authoritative App Store state from one verified V2 signal.
func (service *Service) handleAppleInbox(
	ctx context.Context,
	message persistence.QueueMessage,
	payload []byte,
) error {
	var notification apple.NotificationEnvelope
	if err := decodeStrict(payload, &notification); err != nil {
		return fmt.Errorf("decode App Store notification: %w", err)
	}
	evidence, process, err := notification.VerificationEvidence()
	if err != nil || !process {
		return err
	}
	verificationPayload := jobs.VerificationPayload{
		ExternalCustomerID: notification.ExternalCustomerID,
		ClaimedProducts:    []core.ProviderProductID{notification.ProviderProductID},
		Evidence:           evidence,
	}
	result, err := service.verify(ctx, message.ProjectID, message.ApplicationID, verificationPayload)
	if err != nil {
		return err
	}
	if !requiresReconciliation(verificationPayload.Evidence) {
		return nil
	}
	return service.schedule(ctx, message.ProjectID, message.ApplicationID, result.Customer.ID, verificationPayload)
}

// handleHuaweiInbox resolves one native Huawei V2 token and reconciles authoritative state.
func (service *Service) handleHuaweiInbox(
	ctx context.Context,
	message persistence.QueueMessage,
	payload []byte,
) error {
	var notification huawei.NotificationEnvelope
	if err := decodeStrict(payload, &notification); err != nil {
		return fmt.Errorf("decode Huawei notification: %w", err)
	}
	command, err := service.huaweiReconciliationCommand(ctx, message, notification, payload)
	if err != nil {
		return err
	}
	_, err = service.verification.Reconcile(ctx, command)
	return err
}

// huaweiReconciliationCommand binds a V2 callback token to an existing customer and catalog product.
func (service *Service) huaweiReconciliationCommand(
	ctx context.Context,
	message persistence.QueueMessage,
	notification huawei.NotificationEnvelope,
	payload []byte,
) (verification.ReconciliationCommand, error) {
	token := []byte(notification.PurchaseToken)
	defer zero(token)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: message.ProjectID, ApplicationID: message.ApplicationID,
		Purpose: providerReferencePurpose + ":" + string(core.ReferenceQuery) + ":purchase_token",
	}, token)
	if err != nil {
		return verification.ReconciliationCommand{}, fmt.Errorf("protect Huawei notification lookup: %w", err)
	}
	defer zero(protected.Ciphertext)
	var purchase persistence.PurchaseReferenceContext
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var lookupErr error
		purchase, lookupErr = repository.PurchaseContextByReference(ctx, persistence.ProviderReferenceLookup{
			ProjectID: message.ProjectID, ApplicationID: message.ApplicationID,
			Role: core.ReferenceQuery, Kind: "purchase_token", Fingerprint: protected.Fingerprint,
		})
		return lookupErr
	})
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			return verification.ReconciliationCommand{}, stores.NewFailure(
				core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureInvalidEvidence, 0, err,
			)
		}
		if errors.Is(err, persistence.ErrUnavailable) {
			return verification.ReconciliationCommand{}, stores.NewFailure(
				core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureTemporary, 0, err,
			)
		}
		return verification.ReconciliationCommand{}, err
	}
	if purchase.ProductKind != notification.ProductKind ||
		purchase.ProviderProductID != notification.ProviderProductID {
		return verification.ReconciliationCommand{}, stores.NewFailure(
			core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureInvalidEvidence, 0, nil,
		)
	}
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", notification.PurchaseToken)
	if err != nil {
		return verification.ReconciliationCommand{}, err
	}
	customerBinding, err := core.NewStoreReference(
		core.ReferenceCustomerBinding,
		"developer_payload",
		purchase.Customer.ExternalID,
	)
	if err != nil {
		return verification.ReconciliationCommand{}, err
	}
	signal, err := stores.NewEvidence(huawei.NotificationContentType, payload)
	if err != nil {
		return verification.ReconciliationCommand{}, err
	}
	return verification.ReconciliationCommand{
		ProjectID:                message.ProjectID,
		ApplicationID:            message.ApplicationID,
		ExternalCustomerID:       purchase.Customer.ExternalID,
		ExpectedProducts:         []core.ProviderProductID{purchase.ProviderProductID},
		ExpectedProductKind:      purchase.ProductKind,
		ExpectedCustomerBindings: []core.StoreReference{customerBinding},
		QueryReferences:          []core.StoreReference{queryReference},
		Signal:                   signal,
	}, nil
}

// handleGooglePlayInbox resolves one RTDN token to its verified customer and refreshes Android Publisher state.
func (service *Service) handleGooglePlayInbox(
	ctx context.Context,
	message persistence.QueueMessage,
	payload []byte,
) error {
	var notification googleplay.NotificationEnvelope
	if err := decodeStrict(payload, &notification); err != nil {
		return fmt.Errorf("decode Google Play notification: %w", err)
	}
	command, process, err := service.googlePlayReconciliationCommand(ctx, message, notification, payload)
	if err != nil || !process {
		return err
	}
	result, err := service.verification.Reconcile(ctx, command)
	if err != nil {
		return err
	}
	if notification.ProductKind != core.ProductKindSubscription {
		return nil
	}
	queryEvidence, _, err := notification.VerificationEvidence()
	if err != nil {
		return err
	}
	return service.schedule(ctx, message.ProjectID, message.ApplicationID, result.Customer.ID, jobs.VerificationPayload{
		ExternalCustomerID: command.ExternalCustomerID,
		ClaimedProducts:    append([]core.ProviderProductID(nil), command.ExpectedProducts...),
		Evidence:           queryEvidence,
	})
}

// googlePlayReconciliationCommand binds an RTDN token to a known purchase and preserves the native signal.
func (service *Service) googlePlayReconciliationCommand(
	ctx context.Context,
	message persistence.QueueMessage,
	notification googleplay.NotificationEnvelope,
	payload []byte,
) (verification.ReconciliationCommand, bool, error) {
	if err := notification.Validate(); err != nil {
		return verification.ReconciliationCommand{}, false, err
	}
	if notification.Kind == googleplay.NotificationKindTest ||
		notification.Kind == googleplay.NotificationKindPendingRefundReview {
		return verification.ReconciliationCommand{}, false, nil
	}
	token := []byte(notification.PurchaseToken)
	defer zero(token)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: message.ProjectID, ApplicationID: message.ApplicationID,
		Purpose: providerReferencePurpose + ":" + string(core.ReferenceQuery) + ":purchase_token",
	}, token)
	if err != nil {
		return verification.ReconciliationCommand{}, false, fmt.Errorf(
			"protect Google Play notification lookup: %w",
			err,
		)
	}
	defer zero(protected.Ciphertext)
	var purchase persistence.PurchaseReferenceContext
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var lookupErr error
		purchase, lookupErr = repository.PurchaseContextByReference(ctx, persistence.ProviderReferenceLookup{
			ProjectID: message.ProjectID, ApplicationID: message.ApplicationID,
			Role: core.ReferenceQuery, Kind: "purchase_token", Fingerprint: protected.Fingerprint,
		})
		return lookupErr
	})
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) || errors.Is(err, persistence.ErrUnavailable) {
			return verification.ReconciliationCommand{}, false, stores.NewFailure(
				core.ProviderGooglePlay, "notification_lookup", stores.FailureTemporary, 0, err,
			)
		}
		return verification.ReconciliationCommand{}, false, err
	}
	if purchase.ProductKind != notification.ProductKind ||
		(notification.ProviderProductID != "" && notification.ProviderProductID != purchase.ProviderProductID) {
		return verification.ReconciliationCommand{}, false, stores.NewFailure(
			core.ProviderGooglePlay, "notification_lookup", stores.FailureInvalidEvidence, 0, nil,
		)
	}
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", notification.PurchaseToken)
	if err != nil {
		return verification.ReconciliationCommand{}, false, err
	}
	customerBinding, err := core.NewStoreReference(
		core.ReferenceCustomerBinding,
		"obfuscated_external_account_id",
		purchase.Customer.ExternalID,
	)
	if err != nil {
		return verification.ReconciliationCommand{}, false, err
	}
	signal, err := stores.NewEvidence(googleplay.NotificationContentType, payload)
	if err != nil {
		return verification.ReconciliationCommand{}, false, err
	}
	return verification.ReconciliationCommand{
		ProjectID:                message.ProjectID,
		ApplicationID:            message.ApplicationID,
		ExternalCustomerID:       purchase.Customer.ExternalID,
		ExpectedProducts:         []core.ProviderProductID{purchase.ProviderProductID},
		ExpectedProductKind:      purchase.ProductKind,
		ExpectedCustomerBindings: []core.StoreReference{customerBinding},
		QueryReferences:          []core.StoreReference{queryReference},
		Signal:                   signal,
	}, true, nil
}

// HandleReconciliation opens one scheduled request, re-queries its provider, and schedules its next generation.
func (service *Service) HandleReconciliation(ctx context.Context, message persistence.QueueMessage) error {
	if message.Queue != persistence.QueueReconciliation {
		return errors.New("unsupported reconciliation message")
	}
	payload, err := service.open(ctx, message, reconciliationProtectionPurpose)
	if err != nil {
		return err
	}
	defer zero(payload)
	var scheduled jobs.ReconciliationPayload
	if err := decodeStrict(payload, &scheduled); err != nil {
		return fmt.Errorf("decode reconciliation request: %w", err)
	}
	if err := service.schedule(
		ctx,
		message.ProjectID,
		message.ApplicationID,
		message.CustomerID,
		scheduled.Verification,
	); err != nil {
		return err
	}
	_, err = service.verify(ctx, message.ProjectID, message.ApplicationID, scheduled.Verification)
	return err
}

// verify converts one protected job into the provider-neutral verification command.
func (service *Service) verify(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	payload jobs.VerificationPayload,
) (verification.Result, error) {
	var application core.Application
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		application, loadErr = repository.Application(ctx, projectID, applicationID)
		return loadErr
	})
	if err != nil {
		return verification.Result{}, err
	}
	evidence, bindings, err := payload.VerificationInputs(application.Store.Provider)
	if err != nil {
		return verification.Result{}, err
	}
	return service.verification.Verify(ctx, verification.Command{
		ProjectID: projectID, ApplicationID: applicationID,
		ExternalCustomerID: payload.ExternalCustomerID, ClaimedProducts: payload.ClaimedProducts,
		ExpectedCustomerBindings: bindings, Evidence: evidence,
	})
}

// schedule creates the next unique protected reconciliation generation.
func (service *Service) schedule(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	customerID core.CustomerID,
	verificationPayload jobs.VerificationPayload,
) error {
	availableAt := nextReconciliationTime(service.clock().UTC())
	payload, err := json.Marshal(jobs.ReconciliationPayload{
		Verification: verificationPayload,
		ScheduledFor: availableAt,
	})
	if err != nil {
		return err
	}
	defer zero(payload)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: projectID, ApplicationID: applicationID, Purpose: reconciliationProtectionPurpose,
	}, payload)
	if err != nil {
		return fmt.Errorf("protect reconciliation request: %w", err)
	}
	return service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		_, saveErr := repository.SaveReconciliationJob(ctx, persistence.ReconciliationJob{
			ID: deterministicID("reconcile", protected.Fingerprint), ProjectID: projectID,
			ApplicationID: applicationID, CustomerID: customerID,
			Payload: protected, AvailableAt: availableAt,
		})
		return saveErr
	})
}

// open authenticates and opens one worker payload in its exact durable scope.
func (service *Service) open(
	ctx context.Context,
	message persistence.QueueMessage,
	purpose string,
) ([]byte, error) {
	request, err := protection.NewOpenRequest(protection.Scope{
		ProjectID: message.ProjectID, ApplicationID: message.ApplicationID, Purpose: purpose,
	}, message.ProtectedPayload)
	if err != nil {
		return nil, err
	}
	payload, err := service.protection.Open(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("open protected worker payload: %w", err)
	}
	return payload, nil
}

// decodeStrict accepts exactly one private JSON value and rejects unknown fields.
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

// requiresReconciliation reports whether one protected purchase is a time-sensitive subscription.
func requiresReconciliation(evidence json.RawMessage) bool {
	var metadata struct {
		ProductKind core.ProductKind `json:"product_kind"`
	}
	return json.Unmarshal(evidence, &metadata) == nil && metadata.ProductKind == core.ProductKindSubscription
}

// nextReconciliationTime returns one deterministic UTC generation boundary for idempotent scheduling.
func nextReconciliationTime(now time.Time) time.Time {
	return now.UTC().Truncate(reconciliationInterval).Add(reconciliationInterval)
}

// deterministicID derives one non-sensitive queue identity from a protected fingerprint.
func deterministicID(prefix string, fingerprint [sha256.Size]byte) string {
	digest := sha256.Sum256(append([]byte(prefix+"\x00"), fingerprint[:]...))
	return prefix + "-" + hex.EncodeToString(digest[:16])
}

// zero overwrites temporary opened queue plaintext after processing.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
