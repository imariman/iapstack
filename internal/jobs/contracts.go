// Package jobs defines private protected payloads shared by API and worker processes.
package jobs

import (
	"encoding/json"
	"time"

	"github.com/imariman/iapstack/internal/core"
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
