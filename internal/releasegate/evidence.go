// Package releasegate validates secret-free evidence required for a stable release.
package releasegate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// CurrentSchemaVersion is the only sandbox evidence contract accepted by this release line.
	CurrentSchemaVersion = 1
	// maximumEvidenceText bounds human-authored evidence fields.
	maximumEvidenceText = 500
	// maximumRequestIDLength matches the public HTTP request identity boundary.
	maximumRequestIDLength = 128
)

// Evidence is one secret-free stable-release approval record.
type Evidence struct {
	SchemaVersion     int                `json:"schema_version"`
	Release           string             `json:"release"`
	Commit            string             `json:"commit"`
	ExecutedAt        time.Time          `json:"executed_at"`
	Operator          string             `json:"operator"`
	Automated         AutomatedEvidence  `json:"automated"`
	Sandbox           SandboxEvidence    `json:"sandbox"`
	Scenarios         []ScenarioEvidence `json:"scenarios"`
	Webhook           WebhookEvidence    `json:"webhook"`
	NoSecretsAttested bool               `json:"no_secrets_attested"`
}

// AutomatedEvidence links the manual record to successful immutable CI gates.
type AutomatedEvidence struct {
	CIRunURL            string `json:"ci_run_url"`
	GoChecksPassed      bool   `json:"go_checks_passed"`
	FlutterChecksPassed bool   `json:"flutter_checks_passed"`
	ContainerPassed     bool   `json:"container_passed"`
	ComposePassed       bool   `json:"compose_passed"`
}

// SandboxEvidence records non-secret Huawei device and application eligibility.
type SandboxEvidence struct {
	SandboxUserActive bool   `json:"sandbox_user_active"`
	SandboxAPKActive  bool   `json:"sandbox_apk_active"`
	ApplicationID     string `json:"application_id"`
	AppVersion        string `json:"app_version"`
	DeviceModel       string `json:"device_model"`
	HMSCoreVersion    string `json:"hms_core_version"`
}

// ScenarioEvidence records one manual provider lifecycle assertion without raw purchase data.
type ScenarioEvidence struct {
	Name       string    `json:"name"`
	Passed     bool      `json:"passed"`
	ObservedAt time.Time `json:"observed_at"`
	RequestIDs []string  `json:"request_ids"`
	Assertion  string    `json:"assertion"`
}

// WebhookEvidence records production-receiver authenticity and replay controls.
type WebhookEvidence struct {
	SignedDeliveryPassed  bool `json:"signed_delivery_passed"`
	TamperRejected        bool `json:"tamper_rejected"`
	StaleRejected         bool `json:"stale_rejected"`
	DuplicateDeduplicated bool `json:"duplicate_deduplicated"`
}

// scenarioRequirement defines the minimum correlation evidence for one release scenario.
type scenarioRequirement struct {
	minimumRequestIDs int
}

var (
	// stableVersionPattern accepts stable semantic release tags and rejects candidate suffixes.
	stableVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	// requiredScenarios is the closed Huawei v0.1 manual lifecycle gate.
	requiredScenarios = map[string]scenarioRequirement{
		"lifetime_purchase":                  {minimumRequestIDs: 1},
		"lifetime_duplicate_restore":         {minimumRequestIDs: 2},
		"subscription_initial":               {minimumRequestIDs: 1},
		"subscription_renewal":               {minimumRequestIDs: 1},
		"subscription_cancellation":          {minimumRequestIDs: 1},
		"subscription_expiration":            {minimumRequestIDs: 1},
		"subscription_grace":                 {minimumRequestIDs: 1},
		"subscription_refund":                {minimumRequestIDs: 1},
		"subscription_revocation":            {minimumRequestIDs: 1},
		"invalid_signature":                  {minimumRequestIDs: 1},
		"wrong_application_binding":          {minimumRequestIDs: 1},
		"wrong_product_binding":              {minimumRequestIDs: 1},
		"wrong_customer_binding":             {minimumRequestIDs: 1},
		"duplicate_notification":             {minimumRequestIDs: 2},
		"missed_notification_reconciliation": {minimumRequestIDs: 1},
	}
)

// Decode reads one strict JSON evidence document and validates every stable-release invariant.
func Decode(reader io.Reader) (Evidence, error) {
	if reader == nil {
		return Evidence{}, errors.New("release evidence reader is required")
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if err := decoder.Decode(&evidence); err != nil {
		return Evidence{}, fmt.Errorf("decode release evidence: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Evidence{}, errors.New("release evidence must contain one JSON document")
	}
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

// Validate checks immutable CI linkage, sandbox eligibility, scenarios, and webhook controls.
func (evidence Evidence) Validate() error {
	if evidence.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("release evidence schema_version must be %d", CurrentSchemaVersion)
	}
	if !stableVersionPattern.MatchString(evidence.Release) {
		return errors.New("release must be a stable semantic tag such as v0.1.0")
	}
	if err := validateCommit(evidence.Commit); err != nil {
		return err
	}
	if evidence.ExecutedAt.IsZero() {
		return errors.New("release evidence executed_at is required")
	}
	if !isUTC(evidence.ExecutedAt) {
		return errors.New("release evidence executed_at must use UTC")
	}
	if err := validateText("operator", evidence.Operator); err != nil {
		return err
	}
	if err := evidence.Automated.validate(); err != nil {
		return err
	}
	if err := evidence.Sandbox.validate(); err != nil {
		return err
	}
	if err := validateScenarios(evidence.Scenarios, evidence.ExecutedAt); err != nil {
		return err
	}
	if err := evidence.Webhook.validate(); err != nil {
		return err
	}
	if !evidence.NoSecretsAttested {
		return errors.New("no_secrets_attested must be true")
	}
	return nil
}

// validate requires immutable links to every automated release gate.
func (evidence AutomatedEvidence) validate() error {
	parsed, err := url.ParseRequestURI(evidence.CIRunURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || strings.Contains(evidence.CIRunURL, "REPLACE_") {
		return errors.New("automated ci_run_url must be an absolute HTTPS URL")
	}
	if !evidence.GoChecksPassed || !evidence.FlutterChecksPassed ||
		!evidence.ContainerPassed || !evidence.ComposePassed {
		return errors.New("every automated release gate must be marked passed")
	}
	return nil
}

// validate requires Huawei to confirm both tester-account and APK sandbox eligibility.
func (evidence SandboxEvidence) validate() error {
	if !evidence.SandboxUserActive || !evidence.SandboxAPKActive {
		return errors.New("Huawei sandbox user and APK must both be active")
	}
	for name, value := range map[string]string{
		"sandbox application_id":   evidence.ApplicationID,
		"sandbox app_version":      evidence.AppVersion,
		"sandbox device_model":     evidence.DeviceModel,
		"sandbox hms_core_version": evidence.HMSCoreVersion,
	} {
		if err := validateText(name, value); err != nil {
			return err
		}
	}
	return nil
}

// validate requires authenticity, freshness, and deduplication at the production receiver.
func (evidence WebhookEvidence) validate() error {
	if !evidence.SignedDeliveryPassed || !evidence.TamperRejected ||
		!evidence.StaleRejected || !evidence.DuplicateDeduplicated {
		return errors.New("every production webhook assertion must be marked passed")
	}
	return nil
}

// validateScenarios requires each closed Huawei lifecycle scenario exactly once.
func validateScenarios(scenarios []ScenarioEvidence, executedAt time.Time) error {
	if len(scenarios) != len(requiredScenarios) {
		return fmt.Errorf("release evidence must contain exactly %d Huawei scenarios", len(requiredScenarios))
	}
	seen := make(map[string]struct{}, len(scenarios))
	requestIDs := make(map[string]string)
	for _, scenario := range scenarios {
		requirement, required := requiredScenarios[scenario.Name]
		if !required {
			return fmt.Errorf("unsupported Huawei release scenario %q", scenario.Name)
		}
		if _, duplicate := seen[scenario.Name]; duplicate {
			return fmt.Errorf("duplicate Huawei release scenario %q", scenario.Name)
		}
		seen[scenario.Name] = struct{}{}
		if !scenario.Passed {
			return fmt.Errorf("Huawei release scenario %q is not marked passed", scenario.Name)
		}
		if scenario.ObservedAt.IsZero() {
			return fmt.Errorf("Huawei release scenario %q observed_at is required", scenario.Name)
		}
		if !isUTC(scenario.ObservedAt) {
			return fmt.Errorf("Huawei release scenario %q observed_at must use UTC", scenario.Name)
		}
		if scenario.ObservedAt.After(executedAt) {
			return fmt.Errorf("Huawei release scenario %q occurs after evidence execution", scenario.Name)
		}
		if len(scenario.RequestIDs) < requirement.minimumRequestIDs {
			return fmt.Errorf("Huawei release scenario %q requires at least %d request IDs",
				scenario.Name, requirement.minimumRequestIDs)
		}
		for _, requestID := range scenario.RequestIDs {
			if err := validateRequestID(requestID); err != nil {
				return fmt.Errorf("Huawei release scenario %q: %w", scenario.Name, err)
			}
			if priorScenario, duplicate := requestIDs[requestID]; duplicate {
				return fmt.Errorf("Huawei release scenario %q reuses request ID %q from scenario %q",
					scenario.Name, requestID, priorScenario)
			}
			requestIDs[requestID] = scenario.Name
		}
		if err := validateText("scenario assertion", scenario.Assertion); err != nil {
			return fmt.Errorf("Huawei release scenario %q: %w", scenario.Name, err)
		}
	}
	return nil
}

// validateCommit accepts one exact lowercase Git SHA-1 identity.
func validateCommit(value string) error {
	if len(value) != 40 || strings.ToLower(value) != value || strings.Trim(value, "0") == "" {
		return errors.New("commit must be a 40-character lowercase Git SHA-1")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return errors.New("commit must be a 40-character lowercase Git SHA-1")
	}
	return nil
}

// isUTC accepts timestamp locations whose effective offset is UTC.
func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}

// validateRequestID mirrors the HTTP server's bounded correlation identity alphabet.
func validateRequestID(value string) error {
	if value == "" || len(value) > maximumRequestIDLength {
		return errors.New("request ID must contain between 1 and 128 characters")
	}
	if strings.HasPrefix(value, "REPLACE-") {
		return errors.New("request ID placeholder must be replaced")
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._:-", character)) {
			return errors.New("request ID contains unsupported characters")
		}
	}
	return nil
}

// validateText rejects absent, padded, control-bearing, and oversized evidence text.
func validateText(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is required and must not have surrounding whitespace", name)
	}
	if strings.HasPrefix(value, "REPLACE_") {
		return fmt.Errorf("%s placeholder must be replaced", name)
	}
	if len(value) > maximumEvidenceText {
		return fmt.Errorf("%s exceeds %d bytes", name, maximumEvidenceText)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}
