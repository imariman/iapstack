package financialreports

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// appleColumnStartDate is the required Apple financial-report period start.
	appleColumnStartDate = "Start Date"
	// appleColumnEndDate is the required Apple financial-report period end.
	appleColumnEndDate = "End Date"
	// appleColumnIdentifier is the Apple application identifier matched to a production application.
	appleColumnIdentifier = "Apple Identifier"
	// appleColumnQuantity is the integrity-check quantity and is not multiplied again into proceeds.
	appleColumnQuantity = "Quantity"
	// appleColumnPartnerShare is the per-unit proceeds field and is not the recognized total.
	appleColumnPartnerShare = "Partner Share"
	// appleColumnExtendedPartnerShare is the recognized quantity times partner share.
	appleColumnExtendedPartnerShare = "Extended Partner Share"
	// appleColumnPartnerShareCurrency is the currency of the recognized amount.
	appleColumnPartnerShareCurrency = "Partner Share Currency"
	// appleColumnSaleOrReturn is the S or R sale marker.
	appleColumnSaleOrReturn = "Sale or Return"
	// appleColumnCustomerPrice is present for validation and is never summed.
	appleColumnCustomerPrice = "Customer Price"
	// appleColumnCustomerCurrency is present for validation and does not replace partner share currency.
	appleColumnCustomerCurrency = "Customer Currency"
)

var (
	// appleRequiredColumns are the consolidated financial-report names this parser accepts.
	appleRequiredColumns = []string{
		appleColumnStartDate,
		appleColumnEndDate,
		appleColumnIdentifier,
		appleColumnQuantity,
		appleColumnPartnerShare,
		appleColumnExtendedPartnerShare,
		appleColumnPartnerShareCurrency,
		appleColumnSaleOrReturn,
		appleColumnCustomerPrice,
		appleColumnCustomerCurrency,
	}
	// appleRejectedColumns identify the detailed financial report, which is not accepted.
	appleRejectedColumns = []string{"Transaction Date", "Settlement Date", "Order Type"}
)

// RowFailure is a parse rejection that names a row and column but never a cell.
type RowFailure struct {
	Row    int
	Column string
	Code   string
}

// ParsedConsolidated is one accepted Apple consolidated file before application matching.
type ParsedConsolidated struct {
	PeriodStart time.Time
	PeriodEnd   time.Time
	Totals      []IdentifierTotal
}

// IdentifierTotal is the summed Extended Partner Share for one Apple identifier and currency.
type IdentifierTotal struct {
	AppleIdentifier string
	Currency        string
	Amount          persistence.Decimal
	MinorUnits      *int64
}

// appleAccumulator sums one identifier and currency while a file is parsed.
type appleAccumulator struct {
	identifier string
	currency   string
	amount     persistence.Decimal
}

// Error returns a cell-free parse failure.
func (failure *RowFailure) Error() string {
	switch {
	case failure.Row > 0 && failure.Column != "":
		return fmt.Sprintf("financial report row %d column %s failed with code %s", failure.Row, failure.Column, failure.Code)
	case failure.Column != "":
		return fmt.Sprintf("financial report column %s failed with code %s", failure.Column, failure.Code)
	default:
		return fmt.Sprintf("financial report failed with code %s", failure.Code)
	}
}

// ParseAppleConsolidated reads one tab-delimited consolidated financial report.
func ParseAppleConsolidated(payload []byte, rowLimit int) (ParsedConsolidated, error) {
	if rowLimit <= 0 {
		return ParsedConsolidated{}, reportFailure(0, "", "row_limit")
	}
	payload = bytes.TrimPrefix(payload, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(payload) {
		return ParsedConsolidated{}, reportFailure(0, "", "encoding_invalid")
	}
	text := strings.ReplaceAll(string(payload), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	var columns map[string]int
	var width int
	var periodStart time.Time
	var periodEnd time.Time
	scales := map[string]int32{}
	totals := map[string]appleAccumulator{}
	dataRows := 0
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := index + 1
		fields := splitAppleFields(line)
		if columns == nil {
			parsedColumns, err := appleHeader(fields)
			if err != nil {
				return ParsedConsolidated{}, err
			}
			columns = parsedColumns
			width = len(fields)
			continue
		}
		dataRows++
		if dataRows > rowLimit {
			return ParsedConsolidated{}, reportFailure(row, "", "row_limit")
		}
		if len(fields) != width {
			return ParsedConsolidated{}, reportFailure(row, "", "truncated_row")
		}
		start, end, err := applePeriod(fields, columns, row)
		if err != nil {
			return ParsedConsolidated{}, err
		}
		if periodStart.IsZero() {
			periodStart = start
			periodEnd = end
		} else if !periodStart.Equal(start) || !periodEnd.Equal(end) {
			return ParsedConsolidated{}, reportFailure(row, appleColumnStartDate, "mixed_period")
		}
		identifier := fields[columns[appleColumnIdentifier]]
		if identifier == "" {
			return ParsedConsolidated{}, reportFailure(row, appleColumnIdentifier, "identifier_missing")
		}
		quantity, err := appleMoney(fields[columns[appleColumnQuantity]], row, appleColumnQuantity)
		if err != nil {
			return ParsedConsolidated{}, err
		}
		partnerShare, err := appleMoney(fields[columns[appleColumnPartnerShare]], row, appleColumnPartnerShare)
		if err != nil {
			return ParsedConsolidated{}, err
		}
		extended, err := appleMoney(fields[columns[appleColumnExtendedPartnerShare]], row, appleColumnExtendedPartnerShare)
		if err != nil {
			return ParsedConsolidated{}, err
		}
		if _, err := appleMoney(fields[columns[appleColumnCustomerPrice]], row, appleColumnCustomerPrice); err != nil {
			return ParsedConsolidated{}, err
		}
		currency := fields[columns[appleColumnPartnerShareCurrency]]
		if !ValidCurrencyCode(currency) {
			return ParsedConsolidated{}, reportFailure(row, appleColumnPartnerShareCurrency, "currency_invalid")
		}
		if !ValidCurrencyCode(fields[columns[appleColumnCustomerCurrency]]) {
			return ParsedConsolidated{}, reportFailure(row, appleColumnCustomerCurrency, "currency_invalid")
		}
		if err := appleSaleSign(fields[columns[appleColumnSaleOrReturn]], extended, row); err != nil {
			return ParsedConsolidated{}, err
		}
		if quantity.Mul(partnerShare).Cmp(extended) != 0 {
			return ParsedConsolidated{}, reportFailure(row, appleColumnExtendedPartnerShare, "quantity_mismatch")
		}
		if previous, ok := scales[currency]; ok && previous != extended.Scale() {
			return ParsedConsolidated{}, reportFailure(row, appleColumnExtendedPartnerShare, "mixed_scale")
		}
		scales[currency] = extended.Scale()
		key := identifier + "\x00" + currency
		current, ok := totals[key]
		if !ok {
			totals[key] = appleAccumulator{identifier: identifier, currency: currency, amount: extended}
			continue
		}
		summed, err := current.amount.Add(extended)
		if err != nil {
			return ParsedConsolidated{}, reportFailure(row, appleColumnExtendedPartnerShare, "mixed_scale")
		}
		current.amount = summed
		totals[key] = current
	}
	if columns == nil {
		return ParsedConsolidated{}, reportFailure(0, "", "header_missing")
	}
	if periodStart.IsZero() {
		return ParsedConsolidated{}, reportFailure(0, appleColumnStartDate, "period_missing")
	}

	parsed := ParsedConsolidated{PeriodStart: periodStart, PeriodEnd: periodEnd}
	for _, total := range totals {
		minor, err := appleMinorUnits(total.currency, total.amount)
		if err != nil {
			return ParsedConsolidated{}, err
		}
		parsed.Totals = append(parsed.Totals, IdentifierTotal{
			AppleIdentifier: total.identifier,
			Currency:        total.currency,
			Amount:          total.amount,
			MinorUnits:      minor,
		})
	}
	sort.Slice(parsed.Totals, func(left, right int) bool {
		if parsed.Totals[left].AppleIdentifier != parsed.Totals[right].AppleIdentifier {
			return parsed.Totals[left].AppleIdentifier < parsed.Totals[right].AppleIdentifier
		}
		return parsed.Totals[left].Currency < parsed.Totals[right].Currency
	})
	return parsed, nil
}

// reportFailure builds a cell-free row error.
func reportFailure(row int, column, code string) error {
	return &RowFailure{Row: row, Column: column, Code: code}
}

// appleHeader checks the consolidated column contract and rejects a detailed report.
func appleHeader(fields []string) (map[string]int, error) {
	if len(fields) == 0 {
		return nil, reportFailure(1, "", "header_missing")
	}
	columns := make(map[string]int, len(fields))
	for index, field := range fields {
		if field == "" {
			return nil, reportFailure(1, "", "header_invalid")
		}
		if _, exists := columns[field]; exists {
			return nil, reportFailure(1, field, "duplicate_column")
		}
		columns[field] = index
	}
	for _, column := range appleRejectedColumns {
		if _, exists := columns[column]; exists {
			return nil, reportFailure(1, column, "detailed_report")
		}
	}
	for _, column := range appleRequiredColumns {
		if _, exists := columns[column]; !exists {
			return nil, reportFailure(1, column, "header_invalid")
		}
	}
	return columns, nil
}

// splitAppleFields splits one tab-delimited row and trims surrounding spaces.
func splitAppleFields(line string) []string {
	fields := strings.Split(line, "\t")
	for index := range fields {
		fields[index] = strings.TrimSpace(fields[index])
	}
	return fields
}

// applePeriod reads one MM/DD/YYYY start and end pair.
func applePeriod(fields []string, columns map[string]int, row int) (time.Time, time.Time, error) {
	start, err := appleDate(fields[columns[appleColumnStartDate]])
	if err != nil {
		return time.Time{}, time.Time{}, reportFailure(row, appleColumnStartDate, "date_invalid")
	}
	end, err := appleDate(fields[columns[appleColumnEndDate]])
	if err != nil {
		return time.Time{}, time.Time{}, reportFailure(row, appleColumnEndDate, "date_invalid")
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, reportFailure(row, appleColumnEndDate, "period_invalid")
	}
	return start, end, nil
}

// appleDate parses an Apple financial-report date as a UTC civil date.
func appleDate(value string) (time.Time, error) {
	parsed, err := time.Parse("01/02/2006", value)
	if err != nil {
		return time.Time{}, err
	}
	year, month, day := parsed.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

// appleMoney parses one exact money or quantity field.
func appleMoney(value string, row int, column string) (persistence.Decimal, error) {
	decimal, err := persistence.ParseDecimal(value)
	if err != nil {
		return persistence.Decimal{}, reportFailure(row, column, "money_invalid")
	}
	return decimal, nil
}

// appleSaleSign rejects a sale with negative proceeds or a return with positive proceeds.
func appleSaleSign(value string, extended persistence.Decimal, row int) error {
	switch value {
	case "S":
		if extended.Sign() < 0 {
			return reportFailure(row, appleColumnExtendedPartnerShare, "sale_return_sign")
		}
	case "R":
		if extended.Sign() > 0 {
			return reportFailure(row, appleColumnExtendedPartnerShare, "sale_return_sign")
		}
	default:
		return reportFailure(row, appleColumnSaleOrReturn, "sale_return_invalid")
	}
	return nil
}

// appleMinorUnits fills minor units only when the file scale matches the checked-in exponent.
func appleMinorUnits(currency string, amount persistence.Decimal) (*int64, error) {
	exponent, known := CurrencyExponent(currency)
	if !known {
		return nil, nil
	}
	minor, err := amount.MinorUnits(exponent)
	if err != nil {
		return nil, reportFailure(0, appleColumnExtendedPartnerShare, "minor_unit_scale")
	}
	return minor, nil
}
