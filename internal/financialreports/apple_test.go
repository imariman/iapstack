package financialreports

import (
	"strings"
	"testing"
)

const (
	// appleFinancialHeader is the documented consolidated financial-report header.
	appleFinancialHeader = "Start Date\tEnd Date\tApple Identifier\tQuantity\tPartner Share\tExtended Partner Share\tPartner Share Currency\tSale or Return\tCustomer Price\tCustomer Currency\n"
	// appleConsolidatedFixture is a synthetic file with two currencies, one return, one unknown app, and one sandbox app.
	appleConsolidatedFixture = appleFinancialHeader +
		"09/28/2026\t11/01/2026\t1000000001\t1\t2.50\t2.50\tUSD\tS\t2.99\tUSD\n" +
		"09/28/2026\t11/01/2026\t1000000001\t1\t-1.25\t-1.25\tEUR\tR\t1.49\tEUR\n" +
		"09/28/2026\t11/01/2026\t1000000099\t1\t9.99\t9.99\tUSD\tS\t9.99\tUSD\n" +
		"09/28/2026\t11/01/2026\t1000000002\t1\t4.00\t4.00\tUSD\tS\t4.99\tUSD\n"
	// appleNegativeSaleSentinel is a distinctive Apple Identifier that errors must not echo.
	appleNegativeSaleSentinel = "SENTINEL-APPLE-ID-9f3c2a"
)

// TestParseAppleConsolidatedSumsExtendedPartnerShare verifies recognized rows stay exact and signed.
func TestParseAppleConsolidatedSumsExtendedPartnerShare(t *testing.T) {
	t.Parallel()

	parsed, err := ParseAppleConsolidated([]byte(appleConsolidatedFixture), 100)
	if err != nil {
		t.Fatalf("ParseAppleConsolidated() error = %v", err)
	}
	if parsed.PeriodStart.Format("2006-01-02") != "2026-09-28" || parsed.PeriodEnd.Format("2006-01-02") != "2026-11-01" {
		t.Fatalf("period = %s..%s", parsed.PeriodStart, parsed.PeriodEnd)
	}
	totals := map[string]IdentifierTotal{}
	for _, total := range parsed.Totals {
		totals[total.AppleIdentifier+"/"+total.Currency] = total
		if strings.Contains(total.Amount.String(), "2.99") || strings.Contains(total.Amount.String(), "4.99") {
			t.Fatalf("recognized amount %s used customer price", total.Amount.String())
		}
	}
	usd := totals["1000000001/USD"]
	eur := totals["1000000001/EUR"]
	if usd.Amount.String() != "2.50" || usd.MinorUnits == nil || *usd.MinorUnits != 250 {
		t.Fatalf("USD total = %#v, want 2.50 and 250 minor units", usd)
	}
	if eur.Amount.String() != "-1.25" || eur.MinorUnits == nil || *eur.MinorUnits != -125 {
		t.Fatalf("EUR total = %#v, want -1.25 and -125 minor units", eur)
	}
	if totals["1000000099/USD"].Amount.String() != "9.99" || totals["1000000002/USD"].Amount.String() != "4.00" {
		t.Fatalf("parser dropped identifiers before matching: %#v", parsed.Totals)
	}
}

// TestParseAppleConsolidatedRejectsWholeFile verifies bad files do not return partial totals.
func TestParseAppleConsolidatedRejectsWholeFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		code    string
	}{
		{
			name:    "bad header",
			payload: "Start Date\tEnd Date\tApple Identifier\tQuantity\tPartner Share\tPartner Share Currency\tSale or Return\tCustomer Price\tCustomer Currency\n",
			code:    "header_invalid",
		},
		{
			name: "mixed period",
			payload: appleFinancialHeader +
				"09/28/2026\t11/01/2026\t100\t1\t1.00\t1.00\tUSD\tS\t1.00\tUSD\n" +
				"10/01/2026\t10/31/2026\t100\t1\t1.00\t1.00\tUSD\tS\t1.00\tUSD\n",
			code: "mixed_period",
		},
		{
			name: "negative sale",
			payload: appleFinancialHeader +
				"09/28/2026\t11/01/2026\t" + appleNegativeSaleSentinel + "\t1\t-1.25\t-1.25\tUSD\tS\t1.49\tUSD\n",
			code: "sale_return_sign",
		},
		{
			name: "detailed report",
			payload: "Start Date\tEnd Date\tApple Identifier\tQuantity\tPartner Share\tExtended Partner Share\tPartner Share Currency\tSale or Return\tCustomer Price\tCustomer Currency\tOrder Type\n" +
				"09/28/2026\t11/01/2026\t" + appleNegativeSaleSentinel + "\t1\t1.00\t1.00\tUSD\tS\t1.00\tUSD\t" + appleNegativeSaleSentinel + "\n",
			code: "detailed_report",
		},
		{
			name: "yen scale",
			payload: appleFinancialHeader +
				"09/28/2026\t11/01/2026\t100\t1\t120.00\t120.00\tJPY\tS\t120.00\tJPY\n",
			code: "minor_unit_scale",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := ParseAppleConsolidated([]byte(test.payload), 100)
			if err == nil || len(parsed.Totals) != 0 {
				t.Fatalf("ParseAppleConsolidated() = (%d totals, %v), want whole-file rejection", len(parsed.Totals), err)
			}
			if !strings.Contains(err.Error(), test.code) || strings.Contains(err.Error(), appleNegativeSaleSentinel) {
				t.Fatalf("error = %v, want code %s and no identifier cell", err, test.code)
			}
		})
	}
}

// TestCurrencyExponentMatchesCheckedInISO verifies minor units are not assumed to be cents.
func TestCurrencyExponentMatchesCheckedInISO(t *testing.T) {
	t.Parallel()

	usd, ok := CurrencyExponent("USD")
	jpy, jpyKnown := CurrencyExponent("JPY")
	kwd, kwdKnown := CurrencyExponent("KWD")
	_, unknown := CurrencyExponent("ZZZ")
	if !ok || usd != 2 || !jpyKnown || jpy != 0 || !kwdKnown || kwd != 3 || unknown {
		t.Fatalf("exponents = USD %d JPY %d KWD %d unknown %t", usd, jpy, kwd, unknown)
	}
}
