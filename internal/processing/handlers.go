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
	"log/slog"
	"strings"
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
	// purchaseEvidencePurpose authenticates stored client purchase evidence.
	purchaseEvidencePurpose = "purchase_evidence"
	// purchaseSubmissionEvidenceKind matches the client evidence kind stored by verification.
	purchaseSubmissionEvidenceKind = "purchase_submission"
	// reconciliationInterval schedules daily authoritative lifecycle refreshes.
	reconciliationInterval = 24 * time.Hour
	// googlePlayAccountBindingKind names the Google Play customer binding that carries the external customer id.
	googlePlayAccountBindingKind = "obfuscated_external_account_id"
)

// linkedPurchaseReader reads the replacement link of a purchase that no customer has verified yet.
type linkedPurchaseReader interface {
	// LinkedPurchase queries the provider without a customer and stores nothing.
	LinkedPurchase(context.Context, verification.LinkedPurchaseCommand) (stores.LinkedPurchase, error)
}

// Service opens protected jobs, runs verification, and schedules the next lifecycle refresh.
type Service struct {
	store        persistence.OperationsStore
	protection   protection.Service
	replacements linkedPurchaseReader
	verification *verification.Service
	clock        func() time.Time
	logger       *slog.Logger
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
	return &Service{
		store: store, protection: protectionService, verification: verificationService,
		replacements: verificationService, clock: time.Now,
	}, nil
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
	subscriptionID := notification.SubscriptionID
	if notification.ProductKind == core.ProductKindSubscription && subscriptionID == "" {
		subscriptionID, err = service.storedHuaweiSubscriptionID(ctx, message, protected.Fingerprint)
		if err != nil {
			return verification.ReconciliationCommand{}, err
		}
	}
	queryReferences := []core.StoreReference{queryReference}
	if notification.ProductKind == core.ProductKindSubscription {
		if subscriptionID == "" {
			return verification.ReconciliationCommand{}, stores.NewFailure(
				core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureTemporary, 0, nil,
			)
		}
		subscriptionReference, referenceErr := core.NewStoreReference(
			core.ReferenceQuery, "subscription_id", subscriptionID,
		)
		if referenceErr != nil {
			return verification.ReconciliationCommand{}, referenceErr
		}
		queryReferences = append(queryReferences, subscriptionReference)
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
		QueryReferences:          queryReferences,
		Signal:                   signal,
	}, nil
}

// storedHuaweiSubscriptionID reads subscriptionId from stored client evidence for one purchase token.
// Missing or unusable evidence is retryable so older inbox envelopes are not failed permanently.
func (service *Service) storedHuaweiSubscriptionID(
	ctx context.Context,
	message persistence.QueueMessage,
	fingerprint [32]byte,
) (string, error) {
	var evidence persistence.PurchaseEvidenceRecord
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var lookupErr error
		evidence, lookupErr = repository.PurchaseSubmissionByReference(ctx, persistence.ProviderReferenceLookup{
			ProjectID: message.ProjectID, ApplicationID: message.ApplicationID,
			Role: core.ReferenceQuery, Kind: "purchase_token", Fingerprint: fingerprint,
		})
		return lookupErr
	})
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) || errors.Is(err, persistence.ErrUnavailable) {
			return "", stores.NewFailure(
				core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureTemporary, 0, err,
			)
		}
		return "", err
	}
	if evidenceErr := evidence.Validate(); evidenceErr != nil ||
		evidence.Kind != purchaseSubmissionEvidenceKind ||
		evidence.ContentType != huawei.EvidenceContentType {
		return "", stores.NewFailure(
			core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureTemporary, 0, evidenceErr,
		)
	}
	request, err := protection.NewOpenRequest(protection.Scope{
		ProjectID: message.ProjectID, ApplicationID: message.ApplicationID, Purpose: purchaseEvidencePurpose,
	}, evidence.Payload)
	if err != nil {
		return "", stores.NewFailure(
			core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureTemporary, 0, err,
		)
	}
	plaintext, err := service.protection.Open(ctx, request)
	if err != nil {
		return "", stores.NewFailure(
			core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureTemporary, 0, err,
		)
	}
	defer zero(plaintext)
	subscriptionID, err := huaweiSubscriptionID(plaintext)
	if err != nil || subscriptionID == "" {
		return "", stores.NewFailure(
			core.ProviderHuaweiAppGallery, "notification_lookup", stores.FailureTemporary, 0, err,
		)
	}
	return subscriptionID, nil
}

// huaweiSubscriptionID returns subscriptionId from a stored Huawei purchase evidence envelope.
func huaweiSubscriptionID(evidence []byte) (string, error) {
	var envelope struct {
		PurchaseData string `json:"purchase_data"`
	}
	if err := json.Unmarshal(evidence, &envelope); err != nil {
		return "", err
	}
	if strings.TrimSpace(envelope.PurchaseData) == "" {
		return "", nil
	}
	var purchase struct {
		SubscriptionID string `json:"subscriptionId"`
	}
	if err := json.Unmarshal([]byte(envelope.PurchaseData), &purchase); err != nil {
		return "", err
	}
	return strings.TrimSpace(purchase.SubscriptionID), nil
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
// A subscription token that no client has verified is bound through the purchase it replaces.
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
	var externalCustomerID string
	var expectedProduct core.ProviderProductID
	purchase, err := service.googlePlayPurchase(ctx, message, notification.PurchaseToken)
	switch {
	case err == nil:
		if purchase.ProductKind != notification.ProductKind ||
			(notification.ProviderProductID != "" && notification.ProviderProductID != purchase.ProviderProductID) {
			return verification.ReconciliationCommand{}, false, stores.NewFailure(
				core.ProviderGooglePlay, "notification_lookup", stores.FailureInvalidEvidence, 0, nil,
			)
		}
		externalCustomerID, expectedProduct = purchase.Customer.ExternalID, purchase.ProviderProductID
	case errors.Is(err, persistence.ErrNotFound) && notification.Kind == googleplay.NotificationKindSubscription:
		externalCustomerID, expectedProduct, err = service.googlePlayReplacement(ctx, message, notification)
		if err != nil {
			return verification.ReconciliationCommand{}, false, err
		}
	default:
		return verification.ReconciliationCommand{}, false, googlePlayLookupFailure(err)
	}
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", notification.PurchaseToken)
	if err != nil {
		return verification.ReconciliationCommand{}, false, err
	}
	customerBinding, err := core.NewStoreReference(
		core.ReferenceCustomerBinding,
		googlePlayAccountBindingKind,
		externalCustomerID,
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
		ExternalCustomerID:       externalCustomerID,
		ExpectedProducts:         []core.ProviderProductID{expectedProduct},
		ExpectedProductKind:      notification.ProductKind,
		ExpectedCustomerBindings: []core.StoreReference{customerBinding},
		QueryReferences:          []core.StoreReference{queryReference},
		Signal:                   signal,
	}, true, nil
}

// googlePlayReplacement binds a subscription token that no client has verified to the purchase it replaces.
// Google issues a new purchase token for an upgrade, a downgrade, a deferred replacement, or a resubscribe before
// expiry, and names the old token in linkedPurchaseToken. The provider is queried before any customer is known.
// The old token must already be stored, and the new purchase's obfuscated account id must name the old token's
// customer or the same canonical customer. The caller then reconciles the new token for that account, which
// stores the purchase and acknowledges it after the commit, as Google requires within three days.
// It returns the external customer id and the product that grants access now.
func (service *Service) googlePlayReplacement(
	ctx context.Context,
	message persistence.QueueMessage,
	notification googleplay.NotificationEnvelope,
) (string, core.ProviderProductID, error) {
	if service.replacements == nil {
		return "", "", errors.New("google play linked purchase reader is not configured")
	}
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", notification.PurchaseToken)
	if err != nil {
		return "", "", err
	}
	linked, err := service.replacements.LinkedPurchase(ctx, verification.LinkedPurchaseCommand{
		ProjectID: message.ProjectID, ApplicationID: message.ApplicationID,
		ProductKind: core.ProductKindSubscription, QueryReferences: []core.StoreReference{queryReference},
	})
	var failure *stores.Failure
	if errors.As(err, &failure) && failure.Kind == stores.FailureNotFound {
		// The signed RTDN announced this token, so a missing purchase is retried like an unknown token.
		return "", "", stores.NewFailure(core.ProviderGooglePlay, "notification_lookup", stores.FailureTemporary, 0, err)
	}
	if err != nil {
		return "", "", err
	}
	linkedToken := core.FirstReferenceValue(linked.LinkedReferences, "purchase_token")
	if linkedToken == "" {
		// A purchase that replaces nothing is bound by its client verification, which may still race the RTDN.
		return "", "", googlePlayLookupFailure(persistence.ErrNotFound)
	}
	replaced, err := service.googlePlayPurchase(ctx, message, linkedToken)
	if err != nil {
		return "", "", googlePlayLookupFailure(err)
	}
	if replaced.ProductKind != core.ProductKindSubscription {
		return "", "", stores.NewFailure(
			core.ProviderGooglePlay, "notification_lookup", stores.FailureInvalidEvidence, 0, nil,
		)
	}
	accountID := core.FirstReferenceValue(linked.CustomerBindings, googlePlayAccountBindingKind)
	if accountID == "" {
		return "", "", stores.NewFailure(
			core.ProviderGooglePlay, "notification_lookup", stores.FailureInvalidEvidence, 0, nil,
		)
	}
	if accountID != replaced.Customer.ExternalID {
		same, err := service.sameCanonicalCustomer(ctx, message.ProjectID, accountID, replaced.Customer.ExternalID)
		if err != nil {
			return "", "", err
		}
		if !same {
			return "", "", stores.NewFailure(
				core.ProviderGooglePlay, "notification_lookup", stores.FailureInvalidEvidence, 0, nil,
			)
		}
	}
	return accountID, linked.ProductID, nil
}

// googlePlayPurchase fingerprints one purchase token in its query scope and loads the purchase stored for it.
func (service *Service) googlePlayPurchase(
	ctx context.Context,
	message persistence.QueueMessage,
	purchaseToken string,
) (persistence.PurchaseReferenceContext, error) {
	token := []byte(purchaseToken)
	defer zero(token)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: message.ProjectID, ApplicationID: message.ApplicationID,
		Purpose: providerReferencePurpose + ":" + string(core.ReferenceQuery) + ":purchase_token",
	}, token)
	if err != nil {
		return persistence.PurchaseReferenceContext{}, fmt.Errorf("protect Google Play notification lookup: %w", err)
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
	return purchase, err
}

// sameCanonicalCustomer reports whether two external customer ids resolve to one canonical project customer.
// An unknown id is not the same customer. A storage outage stays retryable.
func (service *Service) sameCanonicalCustomer(
	ctx context.Context,
	projectID core.ProjectID,
	left string,
	right string,
) (bool, error) {
	same := false
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		leftCustomer, err := repository.CustomerByExternalID(ctx, projectID, left)
		if err != nil {
			return err
		}
		rightCustomer, err := repository.CustomerByExternalID(ctx, projectID, right)
		if err != nil {
			return err
		}
		same = leftCustomer.ID == rightCustomer.ID
		return nil
	})
	if errors.Is(err, persistence.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, googlePlayLookupFailure(err)
	}
	return same, nil
}

// googlePlayLookupFailure keeps unknown purchases and storage outages retryable and returns other errors as they are.
func googlePlayLookupFailure(err error) error {
	if errors.Is(err, persistence.ErrNotFound) || errors.Is(err, persistence.ErrUnavailable) {
		return stores.NewFailure(core.ProviderGooglePlay, "notification_lookup", stores.FailureTemporary, 0, err)
	}
	return err
}

// HandleReconciliation opens one scheduled request, re-queries its provider, and schedules its next generation.
// The next generation is saved before the provider call, so a failed refresh cannot end the chain.
// A refresh that finds the lineage terminally expired closes that generation instead of leaving it runnable.
// Closing it is best effort and never fails a refresh that has already committed.
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
	customerID := message.CustomerID
	if err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		customer, loadErr := repository.CustomerByExternalID(
			ctx,
			message.ProjectID,
			scheduled.Verification.ExternalCustomerID,
		)
		if loadErr != nil {
			if errors.Is(loadErr, persistence.ErrNotFound) {
				return nil
			}
			return loadErr
		}
		customerID = customer.ID
		return nil
	}); err != nil {
		return err
	}
	nextID, err := service.scheduleNext(
		ctx,
		message.ProjectID,
		message.ApplicationID,
		customerID,
		scheduled.Verification,
	)
	if err != nil {
		return err
	}
	result, err := service.verify(ctx, message.ProjectID, message.ApplicationID, scheduled.Verification)
	if err != nil || !lineageEnded(result.Observations) {
		return err
	}
	// The refresh has committed, so a retry would only repeat the provider call. If retiring fails,
	// the next generation stays runnable, refreshes once more, and retires its own successor.
	if err := service.retire(ctx, nextID); err != nil {
		slog.WarnContext(ctx, "reconciliation_retire_failed", slog.String("reconciliation_job_id", nextID))
	}
	return nil
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
	_, err := service.scheduleNext(ctx, projectID, applicationID, customerID, verificationPayload)
	return err
}

// scheduleNext creates the next unique protected reconciliation generation and returns its durable identity.
func (service *Service) scheduleNext(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	customerID core.CustomerID,
	verificationPayload jobs.VerificationPayload,
) (string, error) {
	availableAt := nextReconciliationTime(service.clock().UTC())
	payload, err := json.Marshal(jobs.ReconciliationPayload{
		Verification: verificationPayload,
		ScheduledFor: availableAt,
	})
	if err != nil {
		return "", err
	}
	defer zero(payload)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: projectID, ApplicationID: applicationID, Purpose: reconciliationProtectionPurpose,
	}, payload)
	if err != nil {
		return "", fmt.Errorf("protect reconciliation request: %w", err)
	}
	var jobID string
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var saveErr error
		jobID, saveErr = repository.SaveReconciliationJob(ctx, persistence.ReconciliationJob{
			ID: deterministicID("reconcile", protected.Fingerprint), ProjectID: projectID,
			ApplicationID: applicationID, CustomerID: customerID,
			Payload: protected, AvailableAt: availableAt,
		})
		return saveErr
	})
	return jobID, err
}

// retire closes one future reconciliation generation so the worker skips it when it becomes due.
func (service *Service) retire(ctx context.Context, jobID string) error {
	completedAt := service.clock().UTC()
	return service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.CompleteQueue(ctx, persistence.QueueCompletion{
			Queue: persistence.QueueReconciliation, ID: jobID, CompletedAt: completedAt,
		})
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

// lineageEnded reports whether one verified purchase lineage is terminally expired.
// Every line item must be expired and denied, with no renewal still enabled. An enabled renewal means the
// provider can still renew this lineage, as in Huawei billing retry after the grace period or an App Store
// subscription still reported active with a stale signed expiry. Unknown renewal state keeps the refresh too.
// Refunds and revocations keep their daily refresh because a provider can reverse them. A later
// resubscription is verified and scheduled again by the client purchase or provider notification that reports it.
func lineageEnded(observations []core.PurchaseObservation) bool {
	if len(observations) == 0 {
		return false
	}
	for _, observation := range observations {
		if observation.State != core.LifecycleExpired || observation.Access != core.AccessDenied {
			return false
		}
		if observation.Renewal.Status != core.RenewalDisabled && observation.Renewal.Status != core.RenewalNotApplicable {
			return false
		}
	}
	return true
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
