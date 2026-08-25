// Package jobs defines private protected payloads shared by API and worker processes.
package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

// Binding contains one expected provider customer binding.
type Binding struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// VerificationPayload contains one replayable signed purchase verification request.
type VerificationPayload struct {
	ExternalCustomerID string                   `json:"external_customer_id"`
	ClaimedProducts    []core.ProviderProductID `json:"claimed_products"`
	CustomerBindings   []Binding                `json:"customer_bindings,omitempty"`
	Evidence           json.RawMessage          `json:"evidence"`
}

// ReconciliationPayload binds a verification request to one unique scheduled generation.
type ReconciliationPayload struct {
	Verification VerificationPayload `json:"verification"`
	ScheduledFor time.Time           `json:"scheduled_for"`
}

// VerificationInputs builds provider-routed evidence and authoritative customer bindings for replay.
func (payload VerificationPayload) VerificationInputs(
	provider core.Provider,
) (stores.Evidence, []core.StoreReference, error) {
	contentType, err := stores.EvidenceContentType(provider)
	if err != nil {
		return stores.Evidence{}, nil, err
	}
	evidence, err := stores.NewEvidence(contentType, payload.Evidence)
	if err != nil {
		return stores.Evidence{}, nil, err
	}
	bindingKind := ""
	switch provider {
	case core.ProviderAppleAppStore:
		bindingKind = "app_account_token"
	case core.ProviderHuaweiAppGallery:
		bindingKind = "developer_payload"
	default:
		return stores.Evidence{}, nil, fmt.Errorf("unsupported purchase provider %q", provider)
	}
	authoritativeBinding, err := core.NewStoreReference(
		core.ReferenceCustomerBinding,
		bindingKind,
		payload.ExternalCustomerID,
	)
	if err != nil {
		return stores.Evidence{}, nil, err
	}
	bindings := []core.StoreReference{authoritativeBinding}
	for _, binding := range payload.CustomerBindings {
		if provider == core.ProviderAppleAppStore && binding.Kind != bindingKind {
			return stores.Evidence{}, nil, fmt.Errorf("unsupported Apple customer binding %q", binding.Kind)
		}
		if binding.Kind == bindingKind {
			if binding.Value != payload.ExternalCustomerID {
				return stores.Evidence{}, nil, errors.New("provider customer binding mismatch")
			}
			continue
		}
		reference, err := core.NewStoreReference(core.ReferenceCustomerBinding, binding.Kind, binding.Value)
		if err != nil {
			return stores.Evidence{}, nil, err
		}
		bindings = append(bindings, reference)
	}
	return evidence, bindings, nil
}
