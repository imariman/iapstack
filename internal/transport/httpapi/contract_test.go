package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/credentials"
	"github.com/imariman/iapstack/internal/offering"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// openAPIContractPath locates the canonical v1 document from this package test directory.
	openAPIContractPath = "../../../contracts/openapi/v1.yaml"
)

// contractOperation identifies one method and path declared by the OpenAPI document.
type contractOperation struct {
	Method      string
	Path        string
	OperationID string
}

// TestOpenAPIContractIsStructurallyValid verifies references, parameters, schemas, and operations comply with OpenAPI 3.
func TestOpenAPIContractIsStructurallyValid(t *testing.T) {
	t.Parallel()

	document := loadOpenAPIContract(t)
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if document.OpenAPI != "3.0.3" {
		t.Fatalf("OpenAPI version = %q, want 3.0.3", document.OpenAPI)
	}
	if document.Info == nil || document.Info.Version != "1.0.0" {
		t.Fatalf("contract info version = %#v, want 1.0.0", document.Info)
	}
}

// TestOpenAPIContractMatchesRegisteredRoutes prevents undocumented endpoints and stale documented operations.
func TestOpenAPIContractMatchesRegisteredRoutes(t *testing.T) {
	t.Parallel()

	documented := contractOperations(loadOpenAPIContract(t))
	registered := (&API{}).v1Routes()
	if len(documented) != len(registered) {
		t.Fatalf("documented operations = %d, registered routes = %d\ndocumented: %s\nregistered: %s",
			len(documented), len(registered), formatContractOperations(documented), formatRoutes(registered))
	}
	byRoute := make(map[string]contractOperation, len(documented))
	for _, operation := range documented {
		key := operation.Method + " " + operation.Path
		if _, exists := byRoute[key]; exists {
			t.Fatalf("duplicate documented operation %s", key)
		}
		byRoute[key] = operation
	}
	operationIDs := make(map[string]string, len(registered))
	for _, route := range registered {
		key := route.Method + " " + route.Path
		operation, exists := byRoute[key]
		if !exists {
			t.Errorf("registered route %s is missing from OpenAPI", key)
			continue
		}
		if operation.OperationID != route.OperationID {
			t.Errorf("operation ID for %s = %q, want %q", key, operation.OperationID, route.OperationID)
		}
		if previous, exists := operationIDs[route.OperationID]; exists {
			t.Errorf("registered operation ID %q is shared by %s and %s", route.OperationID, previous, key)
		}
		operationIDs[route.OperationID] = key
	}
}

// TestOpenAPIContractKeepsStableClientSchemas verifies the SDK and dashboard compatibility boundary remains present.
func TestOpenAPIContractKeepsStableClientSchemas(t *testing.T) {
	t.Parallel()

	document := loadOpenAPIContract(t)
	for name, required := range map[string][]string{
		"AppleCredentialPayload":       {"issuer_id", "key_id", "bundle_id", "private_key", "root_certificates"},
		"AppleEvidence":                {"signed_transaction", "product_kind"},
		"AppleNotificationV2":          {"signedPayload"},
		"GooglePlayCredentialPayload":  {"client_email", "private_key_id", "private_key"},
		"GooglePlayRTDNConfiguration":  {"subscription", "push_service_account_email", "audience"},
		"GooglePlayEvidence":           {"purchase_token", "product_kind"},
		"GooglePlayPubSubPushEnvelope": {"message", "subscription"},
		"GooglePlayPubSubMessage":      {"data", "messageId"},
		"PurchaseSubmission":           {"external_customer_id", "claimed_products", "evidence"},
		"CustomerSessionRequest":       {"external_customer_id"},
		"CustomerSession":              {"token", "expires_at"},
		"DashboardSession":             {"expires_at"},
		"VerificationResult":           {"verified_at", "customer_id", "entitlements"},
		"RestoreResult":                {"results"},
		"EntitlementSnapshot":          {"customer_id", "entitlements"},
		"OfferingDocument":             {"schema_version", "application_id", "revision", "offerings"},
		"Offering":                     {"id", "packages"},
		"OfferingPackage":              {"id", "store_product_id"},
		"ErrorEnvelope":                {"error"},
		"AdminProjectOverview":         {"project", "analytics", "applications", "products", "customers", "recent_transactions", "queues", "recent_webhook_events"},
		"AdminAnalytics":               {"window_days", "generated_at", "verified_count", "allowed_count", "denied_count", "unresolved_count", "reversed_count", "active_entitlement_count", "revenue", "sandbox_value", "daily_activity"},
		"AdminRevenue":                 {"status", "recognized_minor_units", "currency", "amounts", "uncovered_providers", "zero_filled_application_ids", "production_application_count", "test_application_count"},
		"AdminSandboxValue":            {"amounts", "transaction_count", "missing_price_count"},
		"AdminMoney":                   {"milliunits", "currency"},
		"AdminDailyActivity":           {"date", "verified", "allowed", "denied", "unresolved", "reversed"},
		"AdminSubscriptionMetrics": {
			"environment", "window_days", "window_start", "window_end", "measured_at", "active_at_start",
			"active_at_end", "in_trial_at_end", "active_during_window", "new_subscriptions", "renewals",
			"plan_changes", "resubscriptions", "churn",
			"trials", "breakdown", "breakdown_truncated", "daily_active",
		},
		"AdminSubscriptionChurn": {"churned", "voluntary", "billing", "reversed", "unclassified", "replaced", "paused", "awaiting_evidence", "rate"},
		"AdminSubscriptionTrials": {
			"provider", "availability", "active_during_window", "with_offer_phase", "started", "ended",
			"converted", "not_converted", "outcome_not_recorded", "conversion_rate",
		},
		"AdminSubscriptionBreakdown":   {"application_id", "provider", "product_id", "active_at_end", "new_subscriptions", "renewals", "churned"},
		"AdminSubscriptionDailyActive": {"date", "active"},
		"CredentialMetadata":           {"project_id", "application_id", "kind", "content_type", "schema_version", "revision", "created_at", "updated_at"},
		"ApiKeyCollection":             {"api_keys"},
		"ApiKey":                       {"id", "role", "created_at", "current"},
		"CustomerResolution":           {"project_id", "external_id", "canonical_customer_id", "match"},
		"CustomerMerge":                {"id", "project_id", "canonical_customer_id", "source_customer_id", "idempotency_key", "created_at"},
		"AdminCustomerSearchPage":      {"customers"},
		"AdminCustomerDetail":          {"customer", "canonical_customer_id", "entitlements", "manual_grants", "timeline", "timeline_truncated"},
		"AdminCustomerEntitlement":     {"key", "source", "source_id", "access", "reason", "version"},
		"ManualGrant":                  {"id", "customer_id", "entitlement_key", "reason", "actor_key_id", "status", "effective_ends_at"},
		"ManualGrantRequest":           {"idempotency_key", "entitlement_id", "product_id", "application_id", "reason"},
		"ManualGrantRevocationRequest": {"reason"},
		"AppleConsumptionStatus":       {"settings", "recent_requests"},
		"AppleConsumptionSettings":     {"project_id", "application_id", "enabled", "refund_preference", "sample_content_provided", "revision"},
		"AppleConsumptionConsent":      {"application_id", "customer_id", "external_customer_id", "consented", "updated_at"},
	} {
		schemaReference, exists := document.Components.Schemas[name]
		if !exists || schemaReference.Value == nil {
			t.Fatalf("schema %q is missing", name)
		}
		for _, field := range required {
			if _, exists := schemaReference.Value.Properties[field]; !exists {
				t.Errorf("schema %q is missing property %q", name, field)
			}
			if !containsString(schemaReference.Value.Required, field) {
				t.Errorf("schema %q property %q is not required", name, field)
			}
		}
	}
	entitlement := document.Components.Schemas["Entitlement"].Value
	if _, exists := entitlement.Properties["source_customer_id"]; exists {
		t.Fatal("public Entitlement schema must not expose source_customer_id")
	}
	adminCustomer := document.Components.Schemas["AdminCustomer"].Value
	for _, field := range []string{"merged_into_customer_id", "alias_count"} {
		if _, exists := adminCustomer.Properties[field]; !exists {
			t.Errorf("AdminCustomer is missing optional property %q", field)
		}
		if containsString(adminCustomer.Required, field) {
			t.Errorf("AdminCustomer property %q must stay optional", field)
		}
	}
}

// TestGoResponsesMatchOpenAPI validates representative server JSON against the canonical response schemas.
func TestGoResponsesMatchOpenAPI(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	zero := int64(0)
	dailyActivity := make([]controlplane.DailyActivity, 0, 30)
	for offset := 29; offset >= 0; offset-- {
		dailyActivity = append(dailyActivity, controlplane.DailyActivity{
			Date: now.AddDate(0, 0, -offset).Format(time.DateOnly),
		})
	}
	document := loadOpenAPIContract(t)
	tests := []struct {
		name   string
		schema string
		value  any
	}{
		{name: "error envelope", schema: "ErrorEnvelope", value: errorEnvelope{Error: apiError{
			Code: "invalid_request", Message: "request values are invalid", RequestID: "request-1",
		}}},
		{name: "verification result", schema: "VerificationResult", value: verificationResponse{
			VerifiedAt: now, CustomerID: "customer-1", Entitlements: []entitlementResponse{{
				Key: "premium", Access: core.AccessAllowed, Reason: core.AccessReasonPurchaseValid,
				Version: 1,
			}},
		}},
		{name: "customer session", schema: "CustomerSession", value: customerSessionResponse{
			Token: "iaps_opaque", ExpiresAt: now.Add(15 * time.Minute),
		}},
		{name: "dashboard session", schema: "DashboardSession", value: dashboardSessionResponse{
			ExpiresAt: now.Add(12 * time.Hour),
		}},
		{name: "account dashboard session", schema: "DashboardSession", value: dashboardSessionResponse{
			ExpiresAt: now.Add(12 * time.Hour), HasDurableAdmin: true, Username: "owner@example.com",
		}},
		{name: "administrator collection", schema: "AdministratorCollection", value: administratorCollectionResponse{
			Administrators: []administratorResponse{{
				ID: "0123456789abcdef01234567", Username: "owner", CreatedAt: now, PasswordChangedAt: now, Current: true,
			}},
		}},
		{name: "empty administrator collection", schema: "AdministratorCollection", value: administratorCollectionResponse{
			Administrators: []administratorResponse{},
		}},
		{name: "project collection", schema: "AdminProjects", value: controlplane.ProjectList{
			Projects: []controlplane.Project{{ID: "project-1", CreatedAt: now}},
		}},
		{name: "API key collection", schema: "ApiKeyCollection", value: apiKeyCollectionResponse{
			APIKeys: []apiKeyResponse{{
				ID: "0123456789abcdef01234567", Role: persistence.APIKeyRoleApplication,
				ProjectID: "project-1", ApplicationID: "application-1", CreatedAt: now, Current: false,
			}},
		}},
		{name: "API key page", schema: "ApiKeyCollection", value: apiKeyCollectionResponse{
			APIKeys: []apiKeyResponse{{
				ID: "0123456789abcdef01234567", Role: persistence.APIKeyRoleAdmin, CreatedAt: now, Current: true,
			}},
			NextPageToken: "0123456789abcdef01234567",
		}},
		{name: "agent API key", schema: "ApiKey", value: apiKeyFromSummary(persistence.APIKeySummary{
			ID: "0123456789abcdef01234568", Role: persistence.APIKeyRoleAgent, ProjectID: "project-1",
			AllowWrites: true, CreatedAt: now,
		}, "")},
		{name: "imported proceeds", schema: "AdminRevenue", value: controlplane.Revenue{
			Status: persistence.AdminRevenueReportsImported,
			Amounts: []controlplane.RecognizedAmount{{
				Provider: core.ProviderAppleAppStore, ReportKind: persistence.FinancialReportKindAppleConsolidated,
				PeriodStart: "2026-09-28", PeriodEnd: "2026-11-01", Currency: "EUR",
				AmountDecimal: "-1.25", AmountScale: 2, MinorUnits: func() *int64 { value := int64(-125); return &value }(),
				CommissionState: persistence.FinancialReportCommissionIncluded,
				TaxState:        persistence.FinancialReportTaxNotItemized,
			}},
			UncoveredProviders: []core.Provider{}, ZeroFilledApplicationIDs: []core.ApplicationID{},
			ProductionApplicationCount: 1,
		}},
		{name: "one imported app of two", schema: "AdminRevenue", value: controlplane.Revenue{
			Status: persistence.AdminRevenueStoreReportsRequired,
			Amounts: []controlplane.RecognizedAmount{{
				Provider: core.ProviderAppleAppStore, ReportKind: persistence.FinancialReportKindAppleConsolidated,
				PeriodStart: "2026-09-28", PeriodEnd: "2026-11-01", Currency: "USD",
				AmountDecimal: "2.50", AmountScale: 2, MinorUnits: func() *int64 { value := int64(250); return &value }(),
				CommissionState: persistence.FinancialReportCommissionIncluded,
				TaxState:        persistence.FinancialReportTaxNotItemized,
			}},
			UncoveredProviders: []core.Provider{}, ZeroFilledApplicationIDs: []core.ApplicationID{"app-apple-other"},
			ProductionApplicationCount: 2,
		}},
		{name: "project overview", schema: "AdminProjectOverview", value: controlplane.ProjectOverview{
			Project: controlplane.Project{ID: "project-1", CreatedAt: now},
			Analytics: controlplane.Analytics{
				WindowDays: 30, GeneratedAt: now,
				Revenue: controlplane.Revenue{
					Status: persistence.AdminRevenueSandboxOnly, RecognizedMinorUnits: &zero,
					Amounts: []controlplane.RecognizedAmount{}, UncoveredProviders: []core.Provider{},
					ZeroFilledApplicationIDs: []core.ApplicationID{},
				},
				SandboxValue:  controlplane.SandboxValue{Amounts: []controlplane.Money{}},
				DailyActivity: dailyActivity,
			},
			Applications: []controlplane.Application{}, Products: []controlplane.Product{},
			Customers: []controlplane.Customer{}, RecentTransactions: []controlplane.Transaction{},
			Queues: []controlplane.Queue{}, RecentWebhookEvents: []controlplane.WebhookEvent{},
		}},
		{name: "manual grant entitlement", schema: "EntitlementSnapshot", value: map[string]any{
			"customer_id": "customer-1",
			"entitlements": entitlementResponses([]persistence.CustomerEntitlement{
				fakeAdminCustomerEntitlement(now),
			}, now),
		}},
		{name: "customer search page", schema: "AdminCustomerSearchPage", value: controlplane.CustomerSearchPage{
			Customers: []controlplane.CustomerMatch{{
				Customer: controlplane.Customer{
					ID: "customer-1", ExternalID: "user-1", AliasCount: 1, EntitlementCount: 1,
					AllowedCount: 1, LastObservedAt: &now, CreatedAt: now,
				},
				Match: persistence.AdminCustomerMatchTransactionID,
			}},
			NextPageToken: controlplane.EncodeSearchPageToken(persistence.AdminCustomerSearchCursor{
				Rank: 4, CreatedAt: now, CustomerID: "customer-1",
			}, "2000000123456789"),
		}},
		{name: "customer listing page", schema: "AdminCustomerSearchPage", value: controlplane.CustomerSearchPage{
			Customers: []controlplane.CustomerMatch{{
				Customer: controlplane.Customer{ID: "customer-2", ExternalID: "user-2", CreatedAt: now},
			}},
		}},
		{name: "customer detail", schema: "AdminCustomerDetail", value: controlplane.CustomerDetailView(
			fakeAdminCustomerDetail("project-1"), now,
		)},
		{name: "manual grant result", schema: "ManualGrantResult", value: manualGrantResultFromRecord(
			persistence.ManualGrantResult{Grant: fakeAdminCustomerDetail("project-1").ManualGrants[0]},
			verification.ProjectionRefreshResult{ProjectionsChanged: 1, WebhooksEnqueued: 1}, now,
		)},
		{name: "subscription metrics", schema: "AdminSubscriptionMetrics", value: controlplane.SubscriptionMetricsView(
			fakeSubscriptionMetrics(persistence.SubscriptionMetricsQuery{Environment: core.EnvironmentProduction, WindowDays: 30}),
		)},
		{name: "empty subscription metrics", schema: "AdminSubscriptionMetrics", value: controlplane.SubscriptionMetricsView(
			persistence.ComputeSubscriptionMetrics(
				persistence.SubscriptionMetricsQuery{Environment: core.EnvironmentSandbox, WindowDays: 1},
				persistence.SubscriptionWindow{Start: now.Truncate(24 * time.Hour), End: now.Truncate(24 * time.Hour).Add(24 * time.Hour), MeasuredAt: now},
				[]core.Provider{core.ProviderHuaweiAppGallery}, nil,
			),
		)},
		{name: "credential metadata", schema: "CredentialMetadata", value: credentials.Metadata{
			ProjectID: "project-1", ApplicationID: "application-1", Kind: "huawei_server_api",
			ContentType: "application/vnd.iapstack.huawei-credentials+json", SchemaVersion: 1,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}},
		{name: "Apple credential metadata", schema: "CredentialMetadata", value: credentials.Metadata{
			ProjectID: "project-1", ApplicationID: "application-2", Kind: "apple_app_store_server_api",
			ContentType: "application/vnd.iapstack.apple-credentials+json", SchemaVersion: 1,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}},
		{name: "Google Play credential metadata", schema: "CredentialMetadata", value: credentials.Metadata{
			ProjectID: "project-1", ApplicationID: "application-3", Kind: "google_play_android_publisher",
			ContentType: "application/vnd.iapstack.google-play-credentials+json", SchemaVersion: 1,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}},
		{name: "default Apple consumption status", schema: "AppleConsumptionStatus", value: appleConsumptionStatusResponse{
			Settings:       consumptionSettingsResponse(persistence.DefaultAppleConsumptionSettings("project-1", "application-2")),
			RecentRequests: []appleConsumptionRequestResponse{},
		}},
		{name: "Apple consumption status", schema: "AppleConsumptionStatus", value: appleConsumptionStatusResponse{
			Settings: consumptionSettingsResponse(persistence.AppleConsumptionSettings{
				ProjectID: "project-1", ApplicationID: "application-2", Enabled: true,
				RefundPreference:      persistence.ConsumptionRefundPreferenceDecline,
				SampleContentProvided: true, Revision: 2, CreatedAt: now, UpdatedAt: now,
			}),
			RecentRequests: []appleConsumptionRequestResponse{
				consumptionRequestResponse(persistence.AppleConsumptionRequestSummary{
					ID: "consumption-1", ApplicationID: "application-2", CustomerID: "customer-1",
					ProviderProductID: "iapstack.pro", ProductType: "auto_renewable_subscription",
					CustomerReason: "unintended_purchase", Status: persistence.ConsumptionStatusSent,
					StatusReason: "accepted", AttemptCount: 1, SignedAt: now, ReceivedAt: now,
					DeadlineAt: now.Add(12 * time.Hour), CompletedAt: &now, LastHTTPStatus: http.StatusAccepted,
				}),
				consumptionRequestResponse(persistence.AppleConsumptionRequestSummary{
					ID: "consumption-2", ApplicationID: "application-2", ProviderProductID: "iapstack.coins",
					ProductType: "consumable", Status: persistence.ConsumptionStatusPending,
					StatusReason: "provider_temporary", AttemptCount: 2, SignedAt: now, ReceivedAt: now,
					DeadlineAt: now.Add(12 * time.Hour), LastHTTPStatus: http.StatusBadRequest, LastAppleErrorCode: 4000006,
				}),
			},
		}},
		{name: "Apple consumption consent", schema: "AppleConsumptionConsent", value: appleConsumptionConsentResponse{
			ApplicationID: "application-2", CustomerID: "customer-1",
			ExternalCustomerID: "4f753b4a-32c1-4fe4-a8e6-06efb0b35db7", Consented: true, UpdatedAt: now,
		}},
		{name: "webhook metadata", schema: "WebhookConfiguration", value: map[string]any{
			"url": "https://example.com/iapstack/events", "revision": int64(1),
		}},
		{name: "offering document", schema: "OfferingDocument", value: offering.Document{
			SchemaVersion: 1, ApplicationID: "application-1", Revision: "revision-1",
			Offerings: []offering.Offering{{
				ID: "default",
				Packages: []offering.Package{{
					ID: "monthly", StoreProductID: "premium_monthly", DisplayKey: "premium_monthly",
				}},
			}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			validateResponseAgainstSchema(t, document, test.schema, test.value)
		})
	}
}

// loadOpenAPIContract loads the canonical repository contract and fails the calling test on errors.
func loadOpenAPIContract(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile(filepath.Clean(openAPIContractPath))
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	return document
}

// validateResponseAgainstSchema converts a Go response through JSON and applies the named OpenAPI schema.
func validateResponseAgainstSchema(t *testing.T, document *openapi3.T, schemaName string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	schemaReference, exists := document.Components.Schemas[schemaName]
	if !exists || schemaReference.Value == nil {
		t.Fatalf("schema %q is missing", schemaName)
	}
	if err := document.ValidateSchemaJSON(schemaReference.Value, decoded); err != nil {
		t.Fatalf("response %s does not match schema %q: %v", payload, schemaName, err)
	}
}

// contractOperations flattens supported HTTP methods from an OpenAPI path collection.
func contractOperations(document *openapi3.T) []contractOperation {
	operations := make([]contractOperation, 0)
	for path, item := range document.Paths.Map() {
		for _, method := range []string{
			http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
		} {
			operation := item.GetOperation(method)
			if operation == nil {
				continue
			}
			operations = append(operations, contractOperation{
				Method: method, Path: path, OperationID: operation.OperationID,
			})
		}
	}
	sort.Slice(operations, func(left, right int) bool {
		return operations[left].Method+operations[left].Path < operations[right].Method+operations[right].Path
	})
	return operations
}

// formatContractOperations returns deterministic diagnostics for documented operation mismatches.
func formatContractOperations(operations []contractOperation) string {
	values := make([]string, 0, len(operations))
	for _, operation := range operations {
		values = append(values, fmt.Sprintf("%s %s (%s)", operation.Method, operation.Path, operation.OperationID))
	}
	return strings.Join(values, ", ")
}

// formatRoutes returns deterministic diagnostics for registered route mismatches.
func formatRoutes(routes []v1Route) string {
	values := make([]string, 0, len(routes))
	for _, route := range routes {
		values = append(values, fmt.Sprintf("%s %s (%s)", route.Method, route.Path, route.OperationID))
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

// containsString reports whether a required-property collection contains one exact field.
func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
