// Package releasegate validates secret-free evidence required for a release.
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
	// CurrentSchemaVersion is the only release evidence contract accepted by this release line.
	CurrentSchemaVersion = 2
	// maximumEvidenceText bounds human-authored evidence fields.
	maximumEvidenceText = 500
	// maximumRequestIDLength matches the public HTTP request identity boundary.
	maximumRequestIDLength = 128
	// ProviderAppleAppStore identifies Apple's real-provider release gate.
	ProviderAppleAppStore Provider = "apple_app_store"
	// ProviderGooglePlay identifies Google Play's real-provider release gate.
	ProviderGooglePlay Provider = "google_play"
	// ProviderHuaweiAppGallery identifies Huawei AppGallery's real-provider release gate.
	ProviderHuaweiAppGallery Provider = "huawei_appgallery"
)

// Provider identifies one store whose real-provider gate blocks the release.
type Provider string

// Evidence is one secret-free release approval record shared by every provider.
type Evidence struct {
	SchemaVersion     int                `json:"schema_version"`
	Release           string             `json:"release"`
	Commit            string             `json:"commit"`
	Automated         AutomatedEvidence  `json:"automated"`
	Providers         []ProviderEvidence `json:"providers"`
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

// ProviderEvidence records one store's manual test execution.
type ProviderEvidence struct {
	Provider    Provider            `json:"provider"`
	ExecutedAt  time.Time           `json:"executed_at"`
	Operator    string              `json:"operator"`
	Environment EnvironmentEvidence `json:"environment"`
	Scenarios   []ScenarioEvidence  `json:"scenarios"`
}

// EnvironmentEvidence records non-secret provider test readiness and runtime identity.
type EnvironmentEvidence struct {
	TesterAccountReady         bool   `json:"tester_account_ready"`
	ProviderConfigurationReady bool   `json:"provider_configuration_ready"`
	ApplicationID              string `json:"application_id"`
	AppVersion                 string `json:"app_version"`
	DeviceModel                string `json:"device_model"`
	OSVersion                  string `json:"os_version"`
	StoreRuntimeVersion        string `json:"store_runtime_version"`
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

type scenarioRequirement struct {
	minimumRequestIDs int
}

var (
	// releaseVersionPattern accepts stable versions and numbered release candidates only.
	releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[1-9][0-9]*)?$`)
	// providerRequirements is the closed v0.1 real-provider lifecycle gate.
	providerRequirements = map[Provider]map[string]scenarioRequirement{
		ProviderHuaweiAppGallery: {
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
		},
		ProviderAppleAppStore: {
			"non_consumable_purchase":            {minimumRequestIDs: 1},
			"non_consumable_duplicate_restore":   {minimumRequestIDs: 2},
			"subscription_initial":               {minimumRequestIDs: 1},
			"subscription_renewal":               {minimumRequestIDs: 1},
			"subscription_cancellation":          {minimumRequestIDs: 1},
			"subscription_expiration":            {minimumRequestIDs: 1},
			"subscription_billing_retry":         {minimumRequestIDs: 1},
			"subscription_grace":                 {minimumRequestIDs: 1},
			"subscription_refund":                {minimumRequestIDs: 1},
			"subscription_revocation":            {minimumRequestIDs: 1},
			"invalid_signature":                  {minimumRequestIDs: 1},
			"wrong_application_binding":          {minimumRequestIDs: 1},
			"wrong_product_binding":              {minimumRequestIDs: 1},
			"wrong_customer_binding":             {minimumRequestIDs: 1},
			"duplicate_notification":             {minimumRequestIDs: 2},
			"missed_notification_reconciliation": {minimumRequestIDs: 1},
		},
		ProviderGooglePlay: {
			"non_consumable_purchase":            {minimumRequestIDs: 1},
			"non_consumable_duplicate_restore":   {minimumRequestIDs: 2},
			"subscription_initial":               {minimumRequestIDs: 1},
			"subscription_renewal":               {minimumRequestIDs: 1},
			"subscription_cancellation":          {minimumRequestIDs: 1},
			"subscription_expiration":            {minimumRequestIDs: 1},
			"subscription_grace":                 {minimumRequestIDs: 1},
			"subscription_account_hold":          {minimumRequestIDs: 1},
			"subscription_pause":                 {minimumRequestIDs: 1},
			"subscription_refund":                {minimumRequestIDs: 1},
			"subscription_revocation":            {minimumRequestIDs: 1},
			"pending_purchase":                   {minimumRequestIDs: 1},
			"post_commit_acknowledgement":        {minimumRequestIDs: 1},
			"invalid_purchase_token":             {minimumRequestIDs: 1},
			"wrong_application_binding":          {minimumRequestIDs: 1},
			"wrong_product_binding":              {minimumRequestIDs: 1},
			"wrong_customer_binding":             {minimumRequestIDs: 1},
			"duplicate_notification":             {minimumRequestIDs: 2},
			"missed_notification_reconciliation": {minimumRequestIDs: 1},
		},
	}
)

// Decode reads one strict JSON evidence document and validates every release invariant.
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

// Validate checks immutable CI linkage, all provider gates, and webhook controls.
func (evidence Evidence) Validate() error {
	if evidence.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("release evidence schema_version must be %d", CurrentSchemaVersion)
	}
	if !releaseVersionPattern.MatchString(evidence.Release) {
		return errors.New("release must be a stable semantic tag or numbered candidate such as v0.1.0 or v0.1.0-rc.1")
	}
	if err := validateCommit(evidence.Commit); err != nil {
		return err
	}
	if err := evidence.Automated.validate(); err != nil {
		return err
	}
	if err := validateProviders(evidence.Providers); err != nil {
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

// validateProviders requires every supported provider exactly once and unique request IDs globally.
func validateProviders(providers []ProviderEvidence) error {
	if len(providers) != len(providerRequirements) {
		return fmt.Errorf("release evidence must contain exactly %d provider gates", len(providerRequirements))
	}
	seenProviders := make(map[Provider]struct{}, len(providers))
	requestIDs := make(map[string]string)
	for _, provider := range providers {
		requirements, supported := providerRequirements[provider.Provider]
		if !supported {
			return fmt.Errorf("unsupported release provider %q", provider.Provider)
		}
		if _, duplicate := seenProviders[provider.Provider]; duplicate {
			return fmt.Errorf("duplicate release provider %q", provider.Provider)
		}
		seenProviders[provider.Provider] = struct{}{}
		if err := provider.validate(requirements, requestIDs); err != nil {
			return err
		}
	}
	return nil
}

// validate checks one provider's operator, environment, execution time, and lifecycle matrix.
func (evidence ProviderEvidence) validate(requirements map[string]scenarioRequirement, requestIDs map[string]string) error {
	providerName := string(evidence.Provider)
	if evidence.ExecutedAt.IsZero() {
		return fmt.Errorf("%s executed_at is required", providerName)
	}
	if !isUTC(evidence.ExecutedAt) {
		return fmt.Errorf("%s executed_at must use UTC", providerName)
	}
	if err := validateText(providerName+" operator", evidence.Operator); err != nil {
		return err
	}
	if err := evidence.Environment.validate(evidence.Provider); err != nil {
		return err
	}
	return validateScenarios(evidence.Provider, evidence.Scenarios, requirements, evidence.ExecutedAt, requestIDs)
}

// validate requires the provider-specific runbook readiness checks and non-secret runtime identity.
func (evidence EnvironmentEvidence) validate(provider Provider) error {
	providerName := string(provider)
	if !evidence.TesterAccountReady || !evidence.ProviderConfigurationReady {
		return fmt.Errorf("%s tester account and provider configuration must both be ready", providerName)
	}
	for name, value := range map[string]string{
		"application_id":        evidence.ApplicationID,
		"app_version":           evidence.AppVersion,
		"device_model":          evidence.DeviceModel,
		"os_version":            evidence.OSVersion,
		"store_runtime_version": evidence.StoreRuntimeVersion,
	} {
		if err := validateText(providerName+" "+name, value); err != nil {
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

// validateScenarios requires the provider's closed lifecycle matrix exactly once.
func validateScenarios(
	provider Provider,
	scenarios []ScenarioEvidence,
	requirements map[string]scenarioRequirement,
	executedAt time.Time,
	requestIDs map[string]string,
) error {
	providerName := string(provider)
	if len(scenarios) != len(requirements) {
		return fmt.Errorf("%s evidence must contain exactly %d scenarios", providerName, len(requirements))
	}
	seen := make(map[string]struct{}, len(scenarios))
	for _, scenario := range scenarios {
		requirement, required := requirements[scenario.Name]
		if !required {
			return fmt.Errorf("unsupported %s release scenario %q", providerName, scenario.Name)
		}
		if _, duplicate := seen[scenario.Name]; duplicate {
			return fmt.Errorf("duplicate %s release scenario %q", providerName, scenario.Name)
		}
		seen[scenario.Name] = struct{}{}
		if !scenario.Passed {
			return fmt.Errorf("%s release scenario %q is not marked passed", providerName, scenario.Name)
		}
		if scenario.ObservedAt.IsZero() {
			return fmt.Errorf("%s release scenario %q observed_at is required", providerName, scenario.Name)
		}
		if !isUTC(scenario.ObservedAt) {
			return fmt.Errorf("%s release scenario %q observed_at must use UTC", providerName, scenario.Name)
		}
		if scenario.ObservedAt.After(executedAt) {
			return fmt.Errorf("%s release scenario %q occurs after evidence execution", providerName, scenario.Name)
		}
		if len(scenario.RequestIDs) < requirement.minimumRequestIDs {
			return fmt.Errorf("%s release scenario %q requires at least %d request IDs",
				providerName, scenario.Name, requirement.minimumRequestIDs)
		}
		for _, requestID := range scenario.RequestIDs {
			if err := validateRequestID(requestID); err != nil {
				return fmt.Errorf("%s release scenario %q: %w", providerName, scenario.Name, err)
			}
			identity := providerName + "/" + scenario.Name
			if priorScenario, duplicate := requestIDs[requestID]; duplicate {
				return fmt.Errorf("%s release scenario %q reuses request ID %q from %s",
					providerName, scenario.Name, requestID, priorScenario)
			}
			requestIDs[requestID] = identity
		}
		if err := validateText("scenario assertion", scenario.Assertion); err != nil {
			return fmt.Errorf("%s release scenario %q: %w", providerName, scenario.Name, err)
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
