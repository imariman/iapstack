package jobs

import (
	"encoding/json"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

// TestVerificationInputsRoutesProviderContracts verifies evidence type and customer binding policy per store.
func TestVerificationInputsRoutesProviderContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		provider    core.Provider
		bindingKind string
		contentType string
	}{
		{name: "Apple", provider: core.ProviderAppleAppStore, bindingKind: "app_account_token", contentType: stores.AppleEvidenceContentType},
		{name: "Huawei", provider: core.ProviderHuaweiAppGallery, bindingKind: "developer_payload", contentType: stores.HuaweiEvidenceContentType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := VerificationPayload{
				ExternalCustomerID: "4f753b4a-32c1-4fe4-a8e6-06efb0b35db7",
				Evidence:           json.RawMessage(`{"product_kind":"non_consumable"}`),
			}
			evidence, bindings, err := payload.VerificationInputs(test.provider)
			if err != nil {
				t.Fatalf("VerificationInputs() error = %v", err)
			}
			if evidence.ContentType != test.contentType {
				t.Fatalf("VerificationInputs() content type = %q, want %q", evidence.ContentType, test.contentType)
			}
			if len(bindings) != 1 || bindings[0].Kind != test.bindingKind || bindings[0].Value() != payload.ExternalCustomerID {
				t.Fatalf("VerificationInputs() bindings = %#v", bindings)
			}
		})
	}
}

// TestVerificationInputsRejectsAppleBindingMismatch verifies callers cannot rebind an Apple transaction to another customer.
func TestVerificationInputsRejectsAppleBindingMismatch(t *testing.T) {
	t.Parallel()

	payload := VerificationPayload{
		ExternalCustomerID: "4f753b4a-32c1-4fe4-a8e6-06efb0b35db7",
		CustomerBindings: []Binding{{
			Kind: "app_account_token", Value: "e2282771-732e-4c60-a39b-e5280657714c",
		}},
		Evidence: json.RawMessage(`{"product_kind":"non_consumable"}`),
	}
	if _, _, err := payload.VerificationInputs(core.ProviderAppleAppStore); err == nil {
		t.Fatal("VerificationInputs() error = nil, want binding mismatch")
	}
}
