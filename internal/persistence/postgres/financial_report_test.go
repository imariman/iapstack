package postgres_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/financialreports"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

const (
	// financialAppleHeader is the synthetic consolidated header used by store tests.
	financialAppleHeader = "Start Date\tEnd Date\tApple Identifier\tQuantity\tPartner Share\tExtended Partner Share\tPartner Share Currency\tSale or Return\tCustomer Price\tCustomer Currency\n"
	// financialAppleFixture recognizes one production app in USD and EUR and ignores unknown and sandbox apps.
	financialAppleFixture = financialAppleHeader +
		"09/28/2026\t11/01/2026\t1000000001\t1\t2.50\t2.50\tUSD\tS\t2.99\tUSD\n" +
		"09/28/2026\t11/01/2026\t1000000001\t1\t-1.25\t-1.25\tEUR\tR\t1.49\tEUR\n" +
		"09/28/2026\t11/01/2026\t1000000099\t1\t9.99\t9.99\tUSD\tS\t9.99\tUSD\n" +
		"09/28/2026\t11/01/2026\t1000000002\t1\t4.00\t4.00\tUSD\tS\t4.99\tUSD\n"
)

// TestFinancialReportsRoundTripAcceptedAndUnrecognized verifies stored metadata without raw report bytes.
func TestFinancialReportsRoundTripAcceptedAndUnrecognized(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-reports')`)
	insertFinancialApplication(t, database, "app-apple-prod", "project-reports", "apple_app_store", "production", "1000000001")
	insertFinancialApplication(t, database, "app-apple-sandbox", "project-reports", "apple_app_store", "sandbox", "1000000002")
	service := newFinancialReportService(t, store)

	report, created, err := service.Upload(database.ctx, financialUpload("project-reports", core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(financialAppleFixture), nil, nil))
	if err != nil || !created || report.Status != persistence.FinancialReportStatusAccepted {
		t.Fatalf("apple Upload() = (%s, %t, %v)", report.Status, created, err)
	}
	if len(report.Amounts) != 2 {
		t.Fatalf("amounts = %#v, want USD and EUR for the production app only", report.Amounts)
	}
	for _, amount := range report.Amounts {
		if amount.CoveredApplicationID != "app-apple-prod" {
			t.Fatalf("covered application = %s", amount.CoveredApplicationID)
		}
		if amount.CommissionState != persistence.FinancialReportCommissionIncluded || amount.TaxState != persistence.FinancialReportTaxNotItemized {
			t.Fatalf("amount state = %s %s", amount.CommissionState, amount.TaxState)
		}
	}
	again, created, err := service.Upload(database.ctx, financialUpload("project-reports", core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(financialAppleFixture), nil, nil))
	if err != nil || created || again.ID != report.ID {
		t.Fatalf("idempotent Upload() = (%s, %t, %v), want existing %s", again.ID, created, err, report.ID)
	}

	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	googleBody := []byte("Transaction Type,Description\nCharge,GPA.SENTINEL-ORDER\n")
	google, _, err := service.Upload(database.ctx, financialUpload("project-reports", core.ProviderGooglePlay, persistence.FinancialReportKindGoogleEarnings, googleBody, &start, &end))
	if err != nil || google.Status != persistence.FinancialReportStatusUnrecognized || google.Reason != persistence.FinancialReportReasonFixtureRequired || len(google.Amounts) != 0 {
		t.Fatalf("google Upload() = %#v %v", google, err)
	}
	listed, err := service.List(database.ctx, "project-reports")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	encoded := listed[0].ID + listed[1].ID
	for _, report := range listed {
		encoded += report.Reason + report.Status
		for _, amount := range report.Amounts {
			encoded += amount.AmountDecimal + string(amount.CoveredApplicationID)
		}
	}
	if strings.Contains(encoded, "GPA.SENTINEL-ORDER") || strings.Contains(encoded, "1000000099") || strings.Contains(encoded, "1000000002") {
		t.Fatalf("list leaked report cells: %#v", listed)
	}
	var payloadColumns int
	if err := database.conn.QueryRow(database.ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_name IN ('financial_report_imports', 'financial_report_amounts')
			AND column_name IN ('payload', 'body', 'raw_report', 'order_id', 'price_milliunits')
	`).Scan(&payloadColumns); err != nil {
		t.Fatalf("inspect columns: %v", err)
	}
	if payloadColumns != 0 {
		t.Fatalf("raw report columns = %d", payloadColumns)
	}
}

// TestOverviewRecognizedProceedsStaySeparateFromPurchasePrices verifies coverage rules and null single totals.
func TestOverviewRecognizedProceedsStaySeparateFromPurchasePrices(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	writes := newPurchaseWrites(t, fixture)
	observedAt := time.Now().UTC().Truncate(time.Second)
	writes.evidence.ReceivedAt = observedAt.Add(-2 * time.Minute)
	writes.observation.Observation.ObservedAt = observedAt
	writes.observation.Observation.OccurredAt = observedAt.Add(-time.Minute)
	writes.observation.Observation.EffectivePeriod = core.EffectivePeriod{StartsAt: observedAt.Add(-time.Minute)}
	writes.observation.Observation.Price = &core.PurchasePrice{Milliunits: 4990, Currency: "USD"}
	writes.projection.EffectivePeriod = writes.observation.Observation.EffectivePeriod
	writes.outbox.OccurredAt = observedAt
	writes.outbox.AvailableAt = observedAt
	persistPurchaseWrites(t, database.ctx, store, writes)
	service := newFinancialReportService(t, store)
	if _, _, err := service.Upload(database.ctx, financialUpload(fixture.projectID, core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(financialAppleFixture), nil, nil)); err != nil {
		t.Fatalf("sandbox Upload() error = %v", err)
	}
	sandboxOnly, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID), persistence.AdminRevenueQuery{})
	if err != nil {
		t.Fatalf("sandbox AdminProjectOverview() error = %v", err)
	}
	if sandboxOnly.Analytics.Revenue.Status != persistence.AdminRevenueSandboxOnly ||
		sandboxOnly.Analytics.Revenue.RecognizedMinorUnits == nil ||
		*sandboxOnly.Analytics.Revenue.RecognizedMinorUnits != 0 ||
		len(sandboxOnly.Analytics.Revenue.Amounts) != 0 {
		t.Fatalf("sandbox revenue = %#v, want zero real revenue after an import", sandboxOnly.Analytics.Revenue)
	}
	if _, err := database.conn.Exec(database.ctx, `DELETE FROM financial_report_amounts`); err != nil {
		t.Fatalf("clear sandbox import amounts: %v", err)
	}
	if _, err := database.conn.Exec(database.ctx, `DELETE FROM financial_report_imports`); err != nil {
		t.Fatalf("clear sandbox imports: %v", err)
	}

	insertFinancialApplication(t, database, "app-apple-prod", fixture.projectID, "apple_app_store", "production", "1000000001")

	noImport, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID), persistence.AdminRevenueQuery{})
	if err != nil {
		t.Fatalf("AdminProjectOverview() error = %v", err)
	}
	if noImport.Analytics.Revenue.Status != persistence.AdminRevenueStoreReportsRequired ||
		noImport.Analytics.Revenue.RecognizedMinorUnits != nil ||
		len(noImport.Analytics.Revenue.Amounts) != 0 ||
		noImport.Analytics.SandboxValue.TransactionCount != 1 ||
		noImport.Analytics.SandboxValue.Amounts[0].Milliunits != 4990 {
		t.Fatalf("no-import revenue = %#v sandbox %#v", noImport.Analytics.Revenue, noImport.Analytics.SandboxValue)
	}

	if _, _, err := service.Upload(database.ctx, financialUpload(fixture.projectID, core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(financialAppleFixture), nil, nil)); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	insertFinancialApplication(t, database, "app-google-prod", fixture.projectID, "google_play", "production", "com.example.game")
	partial, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID), persistence.AdminRevenueQuery{})
	if err != nil {
		t.Fatalf("partial AdminProjectOverview() error = %v", err)
	}
	if partial.Analytics.Revenue.Status != persistence.AdminRevenueStoreReportsRequired || len(partial.Analytics.Revenue.Amounts) != 0 {
		t.Fatalf("partial revenue = %#v, want store_reports_required without amounts", partial.Analytics.Revenue)
	}
	if _, err := database.conn.Exec(database.ctx, `DELETE FROM applications WHERE id = 'app-google-prod'`); err != nil {
		t.Fatalf("remove google application: %v", err)
	}

	covered, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID), persistence.AdminRevenueQuery{})
	if err != nil {
		t.Fatalf("covered AdminProjectOverview() error = %v", err)
	}
	if covered.Analytics.Revenue.Status != persistence.AdminRevenueReportsImported ||
		covered.Analytics.Revenue.RecognizedMinorUnits != nil || covered.Analytics.Revenue.Currency != nil ||
		len(covered.Analytics.Revenue.Amounts) != 2 {
		t.Fatalf("covered revenue = %#v", covered.Analytics.Revenue)
	}
	seen := map[string]string{}
	for _, amount := range covered.Analytics.Revenue.Amounts {
		seen[amount.Currency] = amount.AmountDecimal
		if amount.MinorUnits != nil && *amount.MinorUnits == 4990 {
			t.Fatalf("recognized minor units copied sandbox milliunits: %#v", amount)
		}
	}
	if seen["USD"] != "2.50" || seen["EUR"] != "-1.25" {
		t.Fatalf("recognized amounts = %#v", covered.Analytics.Revenue.Amounts)
	}
	if covered.Analytics.SandboxValue.TransactionCount != 1 || covered.Analytics.SandboxValue.Amounts[0].Milliunits != 4990 {
		t.Fatalf("sandbox value changed: %#v", covered.Analytics.SandboxValue)
	}

	otherStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	otherEnd := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	filtered, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID), persistence.AdminRevenueQuery{
		FinancialPeriodStart: &otherStart, FinancialPeriodEnd: &otherEnd,
	})
	if err != nil {
		t.Fatalf("filtered AdminProjectOverview() error = %v", err)
	}
	if filtered.Analytics.Revenue.Status != persistence.AdminRevenueStoreReportsRequired || len(filtered.Analytics.Revenue.Amounts) != 0 {
		t.Fatalf("filtered revenue = %#v, want the requested period to stay uncovered", filtered.Analytics.Revenue)
	}
}

// TestSupersedeReplacesAcceptedPeriodWithoutSumming verifies a newer file replaces rather than adds.
func TestSupersedeReplacesAcceptedPeriodWithoutSumming(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-supersede')`)
	insertFinancialApplication(t, database, "app-apple-prod", "project-supersede", "apple_app_store", "production", "1000000001")
	service := newFinancialReportService(t, store)
	first, _, err := service.Upload(database.ctx, financialUpload("project-supersede", core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(financialAppleFixture), nil, nil))
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	replacement := strings.Replace(financialAppleFixture, "\t2.50\t2.50\tUSD", "\t5.00\t5.00\tUSD", 1)
	second, _, err := service.Supersede(database.ctx, "project-supersede", first.ID, financialUpload(
		"project-supersede", core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(replacement), nil, nil,
	))
	if err != nil || second.Status != persistence.FinancialReportStatusAccepted || second.SupersedesImportID == nil || *second.SupersedesImportID != first.ID {
		t.Fatalf("Supersede() = %#v %v", second, err)
	}
	overview, err := store.AdminProjectOverview(database.ctx, "project-supersede", persistence.AdminRevenueQuery{})
	if err != nil {
		t.Fatalf("AdminProjectOverview() error = %v", err)
	}
	if overview.Analytics.Revenue.Status != persistence.AdminRevenueReportsImported {
		t.Fatalf("superseded revenue = %#v, want reports_imported", overview.Analytics.Revenue)
	}
	replacedUSD := false
	for _, amount := range overview.Analytics.Revenue.Amounts {
		if amount.Currency == "USD" {
			replacedUSD = true
			if amount.AmountDecimal != "5.00" {
				t.Fatalf("USD amount = %s, want replacement 5.00 not a sum", amount.AmountDecimal)
			}
		}
	}
	if !replacedUSD {
		t.Fatalf("superseded amounts = %#v, want USD 5.00", overview.Analytics.Revenue.Amounts)
	}
	if _, _, err := service.Upload(database.ctx, financialUpload("project-supersede", core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(strings.Replace(replacement, "5.00", "6.00", 2)), nil, nil)); err == nil {
		t.Fatal("second accepted upload succeeded, want conflict")
	}
}

// TestFinancialReportsConcurrentDigestUpload verifies one digest winner without aborted-transaction reads.
func TestFinancialReportsConcurrentDigestUpload(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-concurrent-reports')`)
	insertFinancialApplication(t, database, "app-apple-prod", "project-concurrent-reports", "apple_app_store", "production", "1000000001")
	service := newFinancialReportService(t, store)
	type uploadResult struct {
		id      string
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan uploadResult, 4)
	for range 4 {
		go func() {
			<-start
			report, created, err := service.Upload(database.ctx, financialUpload(
				"project-concurrent-reports", core.ProviderAppleAppStore,
				persistence.FinancialReportKindAppleConsolidated, []byte(financialAppleFixture), nil, nil,
			))
			results <- uploadResult{id: report.ID, created: created, err: err}
		}()
	}
	close(start)
	createdCount := 0
	reportID := ""
	for range 4 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent Upload() error = %v", result.err)
		}
		if result.created {
			createdCount++
		}
		if reportID == "" {
			reportID = result.id
		}
		if result.id != reportID {
			t.Fatalf("digest IDs = %q and %q, want one stored report", reportID, result.id)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want one", createdCount)
	}
}

// TestFinancialReportsAcceptExactZeroExponentTrailingZeros verifies JPY amounts keep file scale and minor units.
func TestFinancialReportsAcceptExactZeroExponentTrailingZeros(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-yen-reports')`)
	insertFinancialApplication(t, database, "app-apple-prod", "project-yen-reports", "apple_app_store", "production", "1000000001")
	service := newFinancialReportService(t, store)
	fixture := financialAppleHeader +
		"09/28/2026\t11/01/2026\t1000000001\t1\t120.00\t120.00\tJPY\tS\t120.00\tJPY\n"
	report, created, err := service.Upload(database.ctx, financialUpload(
		"project-yen-reports", core.ProviderAppleAppStore,
		persistence.FinancialReportKindAppleConsolidated, []byte(fixture), nil, nil,
	))
	if err != nil || !created || len(report.Amounts) != 1 {
		t.Fatalf("JPY Upload() = (%#v, %t, %v)", report, created, err)
	}
	amount := report.Amounts[0]
	if amount.AmountDecimal != "120.00" || amount.AmountScale != 2 || amount.MinorUnits == nil || *amount.MinorUnits != 120 {
		t.Fatalf("JPY amount = %#v, want 120.00 at scale 2 and 120 minor units", amount)
	}
}

// newFinancialReportService builds the importer against the integration store.
func newFinancialReportService(t *testing.T, store *postgres.Store) *financialreports.Service {
	t.Helper()
	service, err := financialreports.NewService(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), financialreports.DefaultByteLimit, financialreports.DefaultRowLimit)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// financialUpload builds one service upload.
func financialUpload(project string, provider core.Provider, kind string, body []byte, start, end *time.Time) financialreports.Upload {
	return financialreports.Upload{
		ProjectID: core.ProjectID(project), Provider: provider, ReportKind: kind,
		ContentType: "text/tab-separated-values", PeriodStart: start, PeriodEnd: end, Body: body,
	}
}

// insertFinancialApplication adds one store application for coverage tests.
func insertFinancialApplication(t *testing.T, database *testDatabase, id, projectID, provider, environment, providerApplicationID string) {
	t.Helper()
	mustExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ($1, $2, $3, $4, $5)
	`, id, projectID, provider, environment, providerApplicationID)
}
