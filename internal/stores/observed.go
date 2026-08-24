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
