package postgres

import (
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestRecognizeProviderAmountsKeepsCoveredTotals verifies a quiet app and an uncovered provider
// do not change or hide the currency totals of a covered provider.
func TestRecognizeProviderAmountsKeepsCoveredTotals(t *testing.T) {
	t.Parallel()

	applications := []productionApplication{
		{id: "app-apple", provider: core.ProviderAppleAppStore},
		{id: "app-apple-quiet", provider: core.ProviderAppleAppStore},
		{id: "app-google", provider: core.ProviderGooglePlay},
	}
	report := acceptedFinancialImport{
		ID: "import-apple", Provider: core.ProviderAppleAppStore,
		ReportKind:  persistence.FinancialReportKindAppleConsolidated,
		PeriodStart: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}
	usdMinor := int64(250)
	eurMinor := int64(-125)
	rows := []persistence.FinancialReportAmount{
		financialAmount("app-apple", "USD", "2.50", &usdMinor),
		financialAmount("app-apple", "EUR", "-1.25", &eurMinor),
	}
	amounts, uncovered, allCovered := recognizeProviderAmounts(
		applications,
		map[core.Provider]acceptedFinancialImport{core.ProviderAppleAppStore: report},
		map[string][]persistence.FinancialReportAmount{report.ID: rows},
	)
	if allCovered || len(uncovered) != 1 || uncovered[0] != core.ProviderGooglePlay {
		t.Fatalf("coverage = (%t, %v), want Google uncovered", allCovered, uncovered)
	}
	seen := map[string]persistence.AdminRecognizedAmount{}
	for _, amount := range amounts {
		if amount.Provider != core.ProviderAppleAppStore {
			t.Fatalf("amount = %#v, want only Apple proceeds", amount)
		}
		seen[amount.Currency] = amount
	}
	if len(amounts) != 2 || seen["USD"].AmountDecimal != "2.50" || seen["EUR"].AmountDecimal != "-1.25" ||
		seen["USD"].MinorUnits == nil || *seen["USD"].MinorUnits != 250 ||
		seen["EUR"].MinorUnits == nil || *seen["EUR"].MinorUnits != -125 {
		t.Fatalf("amounts = %#v, want unchanged USD 2.50 and EUR -1.25", amounts)
	}
}

// TestRecognizeProviderAmountsTreatsQuietSiblingAsCovered verifies zero sales do not withhold a matched provider.
func TestRecognizeProviderAmountsTreatsQuietSiblingAsCovered(t *testing.T) {
	t.Parallel()

	applications := []productionApplication{
		{id: "app-apple", provider: core.ProviderAppleAppStore},
		{id: "app-apple-quiet", provider: core.ProviderAppleAppStore},
	}
	report := acceptedFinancialImport{
		ID: "import-apple", Provider: core.ProviderAppleAppStore,
		ReportKind: persistence.FinancialReportKindAppleConsolidated,
	}
	amounts, uncovered, allCovered := recognizeProviderAmounts(
		applications,
		map[core.Provider]acceptedFinancialImport{core.ProviderAppleAppStore: report},
		map[string][]persistence.FinancialReportAmount{
			report.ID: {financialAmount("app-apple", "USD", "2.50", nil)},
		},
	)
	if !allCovered || len(uncovered) != 0 || len(amounts) != 1 || amounts[0].AmountDecimal != "2.50" {
		t.Fatalf("quiet coverage = (%t, %v, %#v), want covered USD 2.50", allCovered, uncovered, amounts)
	}
}

// TestRecognizeProviderAmountsLeavesUnmatchedReportUncovered verifies a file that matches no production app is not zero revenue.
func TestRecognizeProviderAmountsLeavesUnmatchedReportUncovered(t *testing.T) {
	t.Parallel()

	applications := []productionApplication{
		{id: "app-apple", provider: core.ProviderAppleAppStore},
	}
	report := acceptedFinancialImport{
		ID: "import-apple", Provider: core.ProviderAppleAppStore,
		ReportKind: persistence.FinancialReportKindAppleConsolidated,
	}
	amounts, uncovered, allCovered := recognizeProviderAmounts(
		applications,
		map[core.Provider]acceptedFinancialImport{core.ProviderAppleAppStore: report},
		map[string][]persistence.FinancialReportAmount{report.ID: {}},
	)
	if allCovered || len(amounts) != 0 || len(uncovered) != 1 || uncovered[0] != core.ProviderAppleAppStore {
		t.Fatalf("unmatched coverage = (%t, %v, %#v), want Apple uncovered and no amounts", allCovered, uncovered, amounts)
	}
}

// financialAmount builds one stored amount row for coverage tests.
func financialAmount(
	applicationID core.ApplicationID,
	currency string,
	decimal string,
	minor *int64,
) persistence.FinancialReportAmount {
	return persistence.FinancialReportAmount{
		Currency: currency, AmountDecimal: decimal, AmountScale: 2, MinorUnits: minor,
		CommissionState:      persistence.FinancialReportCommissionIncluded,
		TaxState:             persistence.FinancialReportTaxNotItemized,
		CoveredApplicationID: applicationID,
	}
}
