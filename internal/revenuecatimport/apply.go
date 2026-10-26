package revenuecatimport

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// importAliasActorKeyID is the durable actor recorded on aliases created by this command.
	importAliasActorKeyID = "revenuecat-import"
)

// Transactor runs one PostgreSQL transaction. *postgres.Store implements it.
type Transactor interface {
	// Transact executes one callback atomically.
	Transact(context.Context, persistence.TransactionFunc) error
}

// importRepository is the persistence surface the importer uses inside one transaction.
type importRepository interface {
	persistence.Transaction
	// PutCustomer creates one external customer or returns the stored row.
	PutCustomer(context.Context, core.Customer) (core.Customer, error)
	// EnsureImportBatch stores one export hash or rejects a different hash for the same batch.
	EnsureImportBatch(context.Context, persistence.ImportBatch) error
	// UpsertImportGrant inserts or updates one historical grant.
	UpsertImportGrant(context.Context, persistence.ImportGrant) (persistence.ImportGrantResult, error)
	// LoadImportCatalog reads the project catalog without creating rows.
	LoadImportCatalog(context.Context, core.ProjectID) (persistence.ImportCatalog, error)
}

// LoadCatalog reads the existing project catalog through the store.
func LoadCatalog(ctx context.Context, store Transactor, projectID core.ProjectID) (Catalog, error) {
	var snapshot persistence.ImportCatalog
	err := store.Transact(ctx, func(tx persistence.Transaction) error {
		repository, ok := tx.(importRepository)
		if !ok {
			return errors.New("import catalog repository is unavailable")
		}
		var loadErr error
		snapshot, loadErr = repository.LoadImportCatalog(ctx, projectID)
		return loadErr
	})
	if err != nil {
		return Catalog{}, err
	}
	return NewCatalog(snapshot), nil
}

// Apply writes one planned export. Webhooks are enqueued only when enabled and a projection changes.
func Apply(ctx context.Context, store Transactor, input Input, webhooks bool) (Report, error) {
	if store == nil {
		return Report{}, fmt.Errorf("import store is required: %w", ErrUsage)
	}
	if err := core.ValidateOpaqueIdentifier("import batch ID", input.BatchID); err != nil {
		return Report{}, fmt.Errorf("batch id: %w", ErrUsage)
	}
	catalog, err := LoadCatalog(ctx, store, input.ProjectID)
	if err != nil {
		return Report{}, err
	}
	input.Catalog = &catalog
	input.StoreAliases = true
	result, err := Plan(input)
	if err != nil {
		return Report{}, err
	}
	sum := sha256.Sum256(input.Export)
	err = store.Transact(ctx, func(tx persistence.Transaction) error {
		repository, ok := tx.(importRepository)
		if !ok {
			return errors.New("import batch repository is unavailable")
		}
		return repository.EnsureImportBatch(ctx, persistence.ImportBatch{
			ProjectID:    input.ProjectID,
			BatchID:      input.BatchID,
			ExportSHA256: sum,
			Format:       input.Format,
		})
	})
	if errors.Is(err, persistence.ErrConflict) {
		result.Report.DryRun = false
		result.Report.Counts.CustomersCreated = 0
		result.Report.Counts.GrantsUpserted = 0
		result.Report.Failures = append(result.Report.Failures, Failure{Code: CodeBatchConflict})
		return result.Report, ErrBatchConflict
	}
	if err != nil {
		return Report{}, err
	}
	for index := range result.Subscribers {
		subscriber := result.Subscribers[index]
		if subscriber.Skipped || subscriber.Fatal {
			continue
		}
		if err := applySubscriber(ctx, store, input, subscriber, webhooks, &result.Subscribers[index]); err != nil {
			if errors.Is(err, errSubscriberConflict) {
				result.Subscribers[index].Fatal = true
				result.Subscribers[index].Grants = nil
				result.Subscribers[index].Failures = append(result.Subscribers[index].Failures, Failure{
					Line:       subscriberLine(subscriber),
					Code:       CodeConflictingSubscriber,
					ExternalID: subscriber.ExternalID,
				})
				continue
			}
			return Report{}, err
		}
	}
	return finalizeReport(result, false), nil
}

// applySubscriber writes one canonical customer, its aliases, and the projections the shared projector selects.
func applySubscriber(
	ctx context.Context,
	store Transactor,
	input Input,
	subscriber Subscriber,
	webhooks bool,
	target *Subscriber,
) error {
	return store.Transact(ctx, func(tx persistence.Transaction) error {
		repository, ok := tx.(importRepository)
		if !ok {
			return errors.New("import repository is unavailable")
		}
		customer, created, err := importCustomer(ctx, repository, input.ProjectID, subscriber)
		if err != nil {
			return err
		}
		if err := repository.LockCustomerClosure(ctx, input.ProjectID, customer.ID); err != nil {
			return err
		}
		for _, alias := range subscriber.Aliases {
			_, aliasErr := repository.PutCustomerAlias(ctx, persistence.CustomerAliasWrite{
				ProjectID:           input.ProjectID,
				CanonicalCustomerID: customer.ID,
				ExternalID:          alias,
				ActorRole:           persistence.APIKeyRoleAdmin,
				ActorKeyID:          importAliasActorKeyID,
			})
			if errors.Is(aliasErr, persistence.ErrConflict) {
				return errSubscriberConflict
			}
			if aliasErr != nil {
				return aliasErr
			}
		}
		upserted := 0
		unchanged := 0
		for _, grant := range subscriber.Grants {
			grant.ID = GrantID(string(input.ProjectID), string(customer.ID), string(grant.ApplicationID), string(grant.ProductID), string(grant.EntitlementID), grant.LineageFingerprint)
			result, grantErr := repository.UpsertImportGrant(ctx, importGrantRecord(input, customer.ID, grant))
			if grantErr != nil {
				return grantErr
			}
			if result.Inserted || result.Updated {
				upserted++
			} else {
				unchanged++
			}
		}
		refreshed, err := verification.RefreshCustomerProjectionWithOptions(
			ctx,
			repository,
			verification.NewDefaultProjector(),
			input.ProjectID,
			customer.ID,
			input.Now.UTC(),
			webhooks,
		)
		if err != nil {
			return err
		}
		target.CustomerID = customer.ID
		target.Created = created
		target.Reused = !created
		target.GrantsUpserted = upserted
		target.GrantsUnchanged = unchanged
		target.ProjectionsChanged = refreshed.ProjectionsChanged
		target.WebhooksEnqueued = refreshed.WebhooksEnqueued
		return nil
	})
}

// importCustomer resolves aliases and merges to the canonical customer before writing grants.
func importCustomer(
	ctx context.Context,
	repository importRepository,
	projectID core.ProjectID,
	subscriber Subscriber,
) (core.Customer, bool, error) {
	resolution, err := repository.ResolveCustomer(ctx, projectID, subscriber.ExternalID)
	if err == nil {
		return core.Customer{ID: resolution.CanonicalCustomerID, ProjectID: projectID}, false, nil
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		return core.Customer{}, false, err
	}
	customer, err := repository.PutCustomer(ctx, core.Customer{
		ID:         subscriber.CustomerID,
		ProjectID:  projectID,
		ExternalID: subscriber.ExternalID,
	})
	if errors.Is(err, persistence.ErrConflict) {
		return core.Customer{}, false, errSubscriberConflict
	}
	if err != nil {
		return core.Customer{}, false, err
	}
	return customer, true, nil
}

// importGrantRecord converts one planned grant into the durable row.
func importGrantRecord(input Input, customerID core.CustomerID, grant Grant) persistence.ImportGrant {
	return persistence.ImportGrant{
		ID:                 grant.ID,
		ProjectID:          input.ProjectID,
		CustomerID:         customerID,
		ApplicationID:      grant.ApplicationID,
		ProductID:          grant.ProductID,
		EntitlementID:      grant.EntitlementID,
		Origin:             persistence.ImportOriginRevenueCat,
		OriginStore:        grant.OriginStore,
		Environment:        grant.Environment,
		Access:             grant.Access,
		AccessReason:       grant.Reason,
		EffectivePeriod:    core.EffectivePeriod{StartsAt: grant.StartsAt, EndsAt: grant.EndsAt},
		ObservedAt:         grant.ObservedAt,
		ExportSHA256:       sha256.Sum256(input.Export),
		LineageFingerprint: grant.LineageFingerprint,
	}
}

// finalizeReport rebuilds write counts from the subscribers actually committed.
func finalizeReport(result Result, dryRun bool) Report {
	report := result.Report
	report.DryRun = dryRun
	report.Failures = []Failure{}
	report.Counts.CustomersCreated = 0
	report.Counts.CustomersReused = 0
	report.Counts.GrantsUpserted = 0
	report.Counts.GrantsUnchanged = 0
	report.Counts.ProjectionsChanged = 0
	report.Counts.WebhooksEnqueued = 0
	report.Counts.AliasRefused = 0
	report.Counts.AnonymousCustomers = 0
	report.Counts.Failed = 0
	for _, subscriber := range result.Subscribers {
		report.Failures = append(report.Failures, subscriber.Failures...)
		for _, failure := range subscriber.Failures {
			if failure.Code == CodeAliasRefused {
				report.Counts.AliasRefused++
			}
		}
		if subscriber.Skipped {
			continue
		}
		if subscriber.Fatal {
			report.Counts.Failed++
			continue
		}
		if subscriber.Anonymous {
			report.Counts.AnonymousCustomers++
		}
		if subscriber.Created {
			report.Counts.CustomersCreated++
		}
		if subscriber.Reused {
			report.Counts.CustomersReused++
		}
		report.Counts.GrantsUpserted += subscriber.GrantsUpserted
		report.Counts.GrantsUnchanged += subscriber.GrantsUnchanged
		report.Counts.ProjectionsChanged += subscriber.ProjectionsChanged
		report.Counts.WebhooksEnqueued += subscriber.WebhooksEnqueued
	}
	if report.Failures == nil {
		report.Failures = []Failure{}
	}
	return report
}

// subscriberLine returns the first line associated with a subscriber.
func subscriberLine(subscriber Subscriber) int {
	if len(subscriber.Failures) > 0 {
		return subscriber.Failures[0].Line
	}
	if len(subscriber.Grants) > 0 {
		return subscriber.Grants[0].Line
	}
	return 0
}
