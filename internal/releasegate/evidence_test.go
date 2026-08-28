package releasegate

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestEvidenceValidateAcceptsCompleteThreeProviderGate verifies the aggregate happy path.
func TestEvidenceValidateAcceptsCompleteThreeProviderGate(t *testing.T) {
	evidence := validEvidence()
	if err := evidence.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// TestEvidenceValidateAcceptsStableAndCandidateVersions verifies the closed version grammar.
func TestEvidenceValidateAcceptsStableAndCandidateVersions(t *testing.T) {
	for _, version := range []string{"v0.1.0", "v0.1.0-rc.1", "v12.3.4-rc.27"} {
		t.Run(version, func(t *testing.T) {
			evidence := validEvidence()
			evidence.Release = version
			if err := evidence.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
	for _, version := range []string{"0.1.0", "v0.1.0-beta.1", "v0.1.0-rc.0", "v0.1.0-rc"} {
		t.Run("reject_"+version, func(t *testing.T) {
			evidence := validEvidence()
			evidence.Release = version
			if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), "stable semantic tag or numbered candidate") {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

// TestEvidenceValidateRejectsIncompleteProviderGate verifies no provider evidence can be omitted or reused.
func TestEvidenceValidateRejectsIncompleteProviderGate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Evidence)
		want   string
	}{
		{
			name: "missing provider",
			mutate: func(evidence *Evidence) {
				evidence.Providers = evidence.Providers[:2]
			},
			want: "exactly 3 provider gates",
		},
		{
			name: "duplicate provider",
			mutate: func(evidence *Evidence) {
				evidence.Providers[1] = evidence.Providers[0]
			},
			want: "duplicate release provider",
		},
		{
			name: "unready provider configuration",
			mutate: func(evidence *Evidence) {
				evidence.Providers[0].Environment.ProviderConfigurationReady = false
			},
			want: "tester account and provider configuration",
		},
		{
			name: "missing scenario",
			mutate: func(evidence *Evidence) {
				evidence.Providers[0].Scenarios = evidence.Providers[0].Scenarios[:len(evidence.Providers[0].Scenarios)-1]
			},
			want: "evidence must contain exactly",
		},
		{
			name: "failed scenario",
			mutate: func(evidence *Evidence) {
				evidence.Providers[0].Scenarios[0].Passed = false
			},
			want: "is not marked passed",
		},
		{
			name: "placeholder operator",
			mutate: func(evidence *Evidence) {
				evidence.Providers[0].Operator = "REPLACE_WITH_OPERATOR"
			},
			want: "placeholder must be replaced",
		},
		{
			name: "scenario after provider execution",
			mutate: func(evidence *Evidence) {
				evidence.Providers[0].Scenarios[0].ObservedAt = evidence.Providers[0].ExecutedAt.Add(time.Second)
			},
			want: "after evidence execution",
		},
		{
			name: "duplicate correlation across providers",
			mutate: func(evidence *Evidence) {
				evidence.Providers[1].Scenarios[0].RequestIDs[0] = evidence.Providers[0].Scenarios[0].RequestIDs[0]
			},
			want: "reuses request ID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := validEvidence()
			test.mutate(&evidence)
			if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

// TestDecodeRejectsUnknownEvidenceFields verifies the aggregate record remains a closed contract.
func TestDecodeRejectsUnknownEvidenceFields(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"schema_version":2,"unknown":"value"}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Decode() error = %v, want unknown field", err)
	}
}

// TestDecodeAcceptsOneStrictDocument verifies JSON encoding round-trips through the validator.
func TestDecodeAcceptsOneStrictDocument(t *testing.T) {
	payload, err := json.Marshal(validEvidence())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	evidence, err := Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if evidence.Release != "v0.1.0-rc.1" {
		t.Fatalf("Decode() release = %q, want v0.1.0-rc.1", evidence.Release)
	}
}

// TestExampleEvidenceMatchesClosedProviderRequirements prevents the operator template from drifting.
func TestExampleEvidenceMatchesClosedProviderRequirements(t *testing.T) {
	payload, err := os.ReadFile("../../docs/releases/v0.1.0-rc.1-release-evidence.example.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var evidence Evidence
	if err := json.Unmarshal(payload, &evidence); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if evidence.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", evidence.SchemaVersion, CurrentSchemaVersion)
	}
	if len(evidence.Providers) != len(providerRequirements) {
		t.Fatalf("provider count = %d, want %d", len(evidence.Providers), len(providerRequirements))
	}
	seenProviders := make(map[Provider]struct{}, len(evidence.Providers))
	for _, provider := range evidence.Providers {
		requirements, exists := providerRequirements[provider.Provider]
		if !exists {
			t.Fatalf("example contains unsupported provider %q", provider.Provider)
		}
		if _, duplicate := seenProviders[provider.Provider]; duplicate {
			t.Fatalf("example repeats provider %q", provider.Provider)
		}
		seenProviders[provider.Provider] = struct{}{}
		if len(provider.Scenarios) != len(requirements) {
			t.Fatalf("%s scenario count = %d, want %d", provider.Provider, len(provider.Scenarios), len(requirements))
		}
		seen := make(map[string]struct{}, len(provider.Scenarios))
		for _, scenario := range provider.Scenarios {
			if _, required := requirements[scenario.Name]; !required {
				t.Fatalf("%s example contains unsupported scenario %q", provider.Provider, scenario.Name)
			}
			if _, duplicate := seen[scenario.Name]; duplicate {
				t.Fatalf("%s example repeats scenario %q", provider.Provider, scenario.Name)
			}
			seen[scenario.Name] = struct{}{}
		}
	}
}

// validEvidence constructs one complete deterministic three-provider release record.
func validEvidence() Evidence {
	observedAt := time.Date(2026, time.August, 28, 20, 0, 0, 0, time.UTC)
	providers := []Provider{ProviderAppleAppStore, ProviderGooglePlay, ProviderHuaweiAppGallery}
	providerEvidence := make([]ProviderEvidence, 0, len(providers))
	for _, provider := range providers {
		requirements := providerRequirements[provider]
		scenarios := make([]ScenarioEvidence, 0, len(requirements))
		for name, requirement := range requirements {
			requestIDs := make([]string, requirement.minimumRequestIDs)
			for index := range requestIDs {
				requestIDs[index] = string(provider) + "-" + strings.ReplaceAll(name, "_", "-") + "-" + string(rune('1'+index))
			}
			scenarios = append(scenarios, ScenarioEvidence{
				Name: name, Passed: true, ObservedAt: observedAt,
				RequestIDs: requestIDs, Assertion: "Expected projection and idempotency invariants observed.",
			})
		}
		providerEvidence = append(providerEvidence, ProviderEvidence{
			Provider: provider, ExecutedAt: observedAt, Operator: "release-operator",
			Environment: EnvironmentEvidence{
				TesterAccountReady: true, ProviderConfigurationReady: true,
				ApplicationID: "provider-application", AppVersion: "1.0.0+1",
				DeviceModel: "test-device", OSVersion: "test-os-1",
				StoreRuntimeVersion: "test-store-runtime-1",
			},
			Scenarios: scenarios,
		})
	}
	return Evidence{
		SchemaVersion: CurrentSchemaVersion,
		Release:       "v0.1.0-rc.1",
		Commit:        strings.Repeat("a", 40),
		Automated: AutomatedEvidence{
			CIRunURL:       "https://github.com/imariman/iapstack/actions/runs/1",
			GoChecksPassed: true, FlutterChecksPassed: true,
			ContainerPassed: true, ComposePassed: true,
		},
		Providers: providerEvidence,
		Webhook: WebhookEvidence{
			SignedDeliveryPassed: true, TamperRejected: true,
			StaleRejected: true, DuplicateDeduplicated: true,
		},
		NoSecretsAttested: true,
	}
}
