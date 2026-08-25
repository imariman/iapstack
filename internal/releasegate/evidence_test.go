package releasegate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestEvidenceValidateAcceptsCompleteSecretFreeGate verifies the stable happy path.
func TestEvidenceValidateAcceptsCompleteSecretFreeGate(t *testing.T) {
	evidence := validEvidence()
	if err := evidence.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// TestEvidenceValidateRejectsIncompleteProviderGate verifies manual checks cannot be omitted or duplicated.
func TestEvidenceValidateRejectsIncompleteProviderGate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Evidence)
		want   string
	}{
		{
			name: "inactive APK",
			mutate: func(evidence *Evidence) {
				evidence.Sandbox.SandboxAPKActive = false
			},
			want: "sandbox user and APK",
		},
		{
			name: "missing scenario",
			mutate: func(evidence *Evidence) {
				evidence.Scenarios = evidence.Scenarios[:len(evidence.Scenarios)-1]
			},
			want: "exactly 15",
		},
		{
			name: "failed scenario",
			mutate: func(evidence *Evidence) {
				evidence.Scenarios[0].Passed = false
			},
			want: "is not marked passed",
		},
		{
			name: "placeholder operator",
			mutate: func(evidence *Evidence) {
				evidence.Operator = "REPLACE_WITH_OPERATOR"
			},
			want: "placeholder must be replaced",
		},
		{
			name: "scenario after execution",
			mutate: func(evidence *Evidence) {
				evidence.Scenarios[0].ObservedAt = evidence.ExecutedAt.Add(time.Second)
			},
			want: "after evidence execution",
		},
		{
			name: "duplicate correlation",
			mutate: func(evidence *Evidence) {
				for index := range evidence.Scenarios {
					if evidence.Scenarios[index].Name == "duplicate_notification" {
						evidence.Scenarios[index].RequestIDs = []string{"request-1", "request-1"}
					}
				}
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

// TestDecodeRejectsUnknownEvidenceFields verifies the release record remains a closed secret-free contract.
func TestDecodeRejectsUnknownEvidenceFields(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"schema_version":1,"unknown":"value"}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
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
	if evidence.Release != "v0.1.0" {
		t.Fatalf("Decode() release = %q, want v0.1.0", evidence.Release)
	}
}

// validEvidence constructs one complete deterministic stable-release record.
func validEvidence() Evidence {
	observedAt := time.Date(2026, time.August, 25, 20, 0, 0, 0, time.UTC)
	scenarios := make([]ScenarioEvidence, 0, len(requiredScenarios))
	for name, requirement := range requiredScenarios {
		requestIDs := make([]string, requirement.minimumRequestIDs)
		for index := range requestIDs {
			requestIDs[index] = strings.ReplaceAll(name, "_", "-") + "-" + string(rune('1'+index))
		}
		scenarios = append(scenarios, ScenarioEvidence{
			Name: name, Passed: true, ObservedAt: observedAt,
			RequestIDs: requestIDs, Assertion: "Expected projection and idempotency invariants observed.",
		})
	}
	return Evidence{
		SchemaVersion: CurrentSchemaVersion,
		Release:       "v0.1.0",
		Commit:        strings.Repeat("a", 40),
		ExecutedAt:    observedAt,
		Operator:      "release-operator",
		Automated: AutomatedEvidence{
			CIRunURL:       "https://github.com/imariman/iapstack/actions/runs/1",
			GoChecksPassed: true, FlutterChecksPassed: true,
			ContainerPassed: true, ComposePassed: true,
		},
		Sandbox: SandboxEvidence{
			SandboxUserActive: true, SandboxAPKActive: true,
			ApplicationID: "provider-application", AppVersion: "1.0.0+1",
			DeviceModel: "Huawei test device", HMSCoreVersion: "6.14.0",
		},
		Scenarios: scenarios,
		Webhook: WebhookEvidence{
			SignedDeliveryPassed: true, TamperRejected: true,
			StaleRejected: true, DuplicateDeduplicated: true,
		},
		NoSecretsAttested: true,
	}
}
