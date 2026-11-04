package stores

import (
	"context"
	"errors"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/platform/metrics"
)

// ObservedAdapter records bounded provider call and verification outcomes around one concrete adapter.
type ObservedAdapter struct {
	adapter Adapter
	metrics *metrics.Registry
}

// ObservedAdapter forwards every optional adapter contract that the services discover by type assertion.
var (
	_ PostCommitter          = (*ObservedAdapter)(nil)
	_ LinkedPurchaseResolver = (*ObservedAdapter)(nil)
)

// NewObservedAdapter wraps one adapter with operational metrics.
func NewObservedAdapter(adapter Adapter, registry *metrics.Registry) (*ObservedAdapter, error) {
	if adapter == nil || registry == nil {
		return nil, errors.New("observed adapter and metrics registry are required")
	}
	return &ObservedAdapter{adapter: adapter, metrics: registry}, nil
}

// Provider returns the wrapped provider identity.
func (adapter *ObservedAdapter) Provider() core.Provider {
	return adapter.adapter.Provider()
}

// Verify records one authoritative purchase verification outcome.
func (adapter *ObservedAdapter) Verify(
	ctx context.Context,
	request VerificationRequest,
) (VerificationResult, error) {
	result, err := adapter.adapter.Verify(ctx, request)
	outcome := providerOutcome(err)
	adapter.metrics.ObserveProvider(string(adapter.Provider()), "verify", outcome)
	adapter.metrics.ObserveVerification(string(adapter.Provider()), outcome)
	return result, err
}

// Reconcile records one authoritative lifecycle reconciliation outcome.
func (adapter *ObservedAdapter) Reconcile(
	ctx context.Context,
	request ReconciliationRequest,
) (VerificationResult, error) {
	result, err := adapter.adapter.Reconcile(ctx, request)
	adapter.metrics.ObserveProvider(string(adapter.Provider()), "reconcile", providerOutcome(err))
	return result, err
}

// PostCommit forwards durable-result provider actions and records their bounded outcome.
func (adapter *ObservedAdapter) PostCommit(ctx context.Context, request PostCommitRequest) error {
	committer, ok := adapter.adapter.(PostCommitter)
	if !ok {
		err := errors.New("store adapter does not support post-commit actions")
		adapter.metrics.ObserveProvider(string(adapter.Provider()), "post_commit", providerOutcome(err))
		return err
	}
	err := committer.PostCommit(ctx, request)
	adapter.metrics.ObserveProvider(string(adapter.Provider()), "post_commit", providerOutcome(err))
	return err
}

// LinkedPurchase forwards a customer-free replacement lookup and records its bounded outcome.
// Production registers every adapter through this wrapper, so the lookup must be forwarded here for the
// verification service to find it.
func (adapter *ObservedAdapter) LinkedPurchase(
	ctx context.Context,
	request LinkedPurchaseRequest,
) (LinkedPurchase, error) {
	resolver, ok := adapter.adapter.(LinkedPurchaseResolver)
	if !ok {
		err := errors.New("store adapter does not report linked purchases")
		adapter.metrics.ObserveProvider(string(adapter.Provider()), "linked_purchase", providerOutcome(err))
		return LinkedPurchase{}, err
	}
	linked, err := resolver.LinkedPurchase(ctx, request)
	adapter.metrics.ObserveProvider(string(adapter.Provider()), "linked_purchase", providerOutcome(err))
	return linked, err
}

// providerOutcome maps private failures to one bounded metrics label.
func providerOutcome(err error) string {
	if err == nil {
		return "success"
	}
	var failure *Failure
	if errors.As(err, &failure) {
		return string(failure.Kind)
	}
	return "error"
}
