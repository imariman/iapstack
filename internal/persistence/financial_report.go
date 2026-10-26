package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// FinancialReportKindAppleConsolidated is Apple's all-region consolidated financial report.
	FinancialReportKindAppleConsolidated = "apple_financial_consolidated"
	// FinancialReportKindGoogleEarnings is the Google Play earnings report.
	FinancialReportKindGoogleEarnings = "google_play_earnings"
	// FinancialReportKindHuaweiSettlement is the Huawei AppGallery settlement report.
	FinancialReportKindHuaweiSettlement = "huawei_settlement"

	// FinancialReportStatusAccepted means the file was recognized and may cover production applications.
	FinancialReportStatusAccepted = "accepted"
	// FinancialReportStatusUnrecognized means the file is stored but its columns are not a pinned contract.
	FinancialReportStatusUnrecognized = "unrecognized"
	// FinancialReportStatusRejected means the file was retained as a failed import.
	FinancialReportStatusRejected = "rejected"
	// FinancialReportStatusSuperseded means a newer accepted import replaced this period.
	FinancialReportStatusSuperseded = "superseded"

	// FinancialReportSourceUpload means an operator supplied the file.
	FinancialReportSourceUpload = "upload"
	// FinancialReportSourceProviderAPI means a pinned provider pull supplied the file.
	FinancialReportSourceProviderAPI = "provider_api"

	// FinancialReportReasonFixtureRequired means recognition waits for a captured real report fixture.
	FinancialReportReasonFixtureRequired = "fixture_required"

	// FinancialReportCommissionIncluded means the store figure is post-commission developer proceeds.
	FinancialReportCommissionIncluded = "included"
	// FinancialReportCommissionUnknown means the report did not define a post-commission figure.
	FinancialReportCommissionUnknown = "unknown"
	// FinancialReportTaxNotItemized means tax was not calculated, withheld, or filed here.
	FinancialReportTaxNotItemized = "not_itemized"
)

var (
	// ErrFinancialReportNotAccepted means a supersede target or replacement is not accepted.
	ErrFinancialReportNotAccepted = errors.New("financial report import is not accepted")
	// ErrFinancialReportAmbiguous means one store identifier matches more than one production application.
	ErrFinancialReportAmbiguous = errors.New("financial report application match is ambiguous")
	// ErrFinancialReportScopeMismatch means a replacement does not match the import it would replace.
	ErrFinancialReportScopeMismatch = errors.New("financial report does not match the superseded import")
)

// FinancialReportStore persists recognized report metadata without raw report bytes.
type FinancialReportStore interface {
	// SaveFinancialReport inserts one import or returns the existing digest.
	SaveFinancialReport(context.Context, FinancialReportDraft) (FinancialReport, bool, error)
	// SupersedeFinancialReport makes one newer accepted import the current row for its period.
	SupersedeFinancialReport(context.Context, core.ProjectID, string, FinancialReportDraft) (FinancialReport, bool, error)
	// ListFinancialReports returns secret-free import metadata for one project.
	ListFinancialReports(context.Context, core.ProjectID) ([]FinancialReport, error)
}

// FinancialReportDraft is one parsed import ready to match against production applications.
type FinancialReportDraft struct {
	ProjectID          core.ProjectID
	Provider           core.Provider
	ReportKind         string
	PeriodStart        time.Time
	PeriodEnd          time.Time
	Source             string
	SHA256             [32]byte
	ByteSize           int64
	ContentType        string
	ParserVersion      string
	Status             string
	Reason             string
	IdentifierTotals   []FinancialReportIdentifierTotal
	ResolvedAmounts    []FinancialReportAmount
	SupersedesImportID string
}

// FinancialReportIdentifierTotal is one summed store identifier and currency before application matching.
type FinancialReportIdentifierTotal struct {
	ProviderApplicationID string
	Currency              string
	Amount                Decimal
	MinorUnits            *int64
}

// FinancialReport is secret-free metadata for one stored import.
type FinancialReport struct {
	ID                 string
	ProjectID          core.ProjectID
	Provider           core.Provider
	ReportKind         string
	PeriodStart        time.Time
	PeriodEnd          time.Time
	Source             string
	SHA256             [32]byte
	ByteSize           int64
	ContentType        string
	ParserVersion      string
	Status             string
	Reason             string
	SupersedesImportID *string
	CreatedAt          time.Time
	Amounts            []FinancialReportAmount
}

// FinancialReportAmount is one accepted application and currency total.
type FinancialReportAmount struct {
	Currency             string
	AmountDecimal        string
	AmountScale          int32
	MinorUnits           *int64
	CommissionState      string
	TaxState             string
	CoveredApplicationID core.ApplicationID
}

// FinancialReportKindForProvider returns the report kind required to cover one provider.
func FinancialReportKindForProvider(provider core.Provider) (string, bool) {
	switch provider {
	case core.ProviderAppleAppStore:
		return FinancialReportKindAppleConsolidated, true
	case core.ProviderGooglePlay:
		return FinancialReportKindGoogleEarnings, true
	case core.ProviderHuaweiAppGallery:
		return FinancialReportKindHuaweiSettlement, true
	default:
		return "", false
	}
}
