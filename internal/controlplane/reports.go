package controlplane

import (
	"context"
	"encoding/hex"
	"sort"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

// FinancialReportList is the secret-free import collection of one project.
type FinancialReportList struct {
	FinancialReports []FinancialReport `json:"financial_reports"`
}

// FinancialReport is one stored import without report bytes or order ids.
type FinancialReport struct {
	ID                    string                  `json:"id"`
	Provider              core.Provider           `json:"provider"`
	ReportKind            string                  `json:"report_kind"`
	PeriodStart           string                  `json:"period_start"`
	PeriodEnd             string                  `json:"period_end"`
	Source                string                  `json:"source"`
	SHA256                string                  `json:"sha256"`
	ByteSize              int64                   `json:"byte_size"`
	ContentType           string                  `json:"content_type"`
	ParserVersion         string                  `json:"parser_version"`
	Status                string                  `json:"status"`
	Reason                string                  `json:"reason"`
	SupersedesImportID    *string                 `json:"supersedes_import_id,omitempty"`
	Currencies            []string                `json:"currencies"`
	Amounts               []FinancialReportAmount `json:"amounts"`
	CoveredApplicationIDs []core.ApplicationID    `json:"covered_application_ids"`
	CreatedAt             time.Time               `json:"created_at"`
}

// FinancialReportAmount is one accepted application and currency total.
type FinancialReportAmount struct {
	Currency             string             `json:"currency"`
	AmountDecimal        string             `json:"amount_decimal"`
	AmountScale          int32              `json:"amount_scale"`
	MinorUnits           *int64             `json:"minor_units"`
	CommissionState      string             `json:"commission_state"`
	TaxState             string             `json:"tax_state"`
	CoveredApplicationID core.ApplicationID `json:"covered_application_id"`
}

// FinancialReports returns the stored financial report imports of one project.
func (service *Service) FinancialReports(
	ctx context.Context,
	actor Actor,
	projectID core.ProjectID,
) (FinancialReportList, error) {
	if err := actor.AuthorizeProject(projectID); err != nil {
		return FinancialReportList{}, err
	}
	if err := projectID.Validate(); err != nil {
		return FinancialReportList{}, validation.Wrap(err)
	}
	if service.dependencies.FinancialReports == nil {
		return FinancialReportList{}, errMissingDependency
	}
	reports, err := service.dependencies.FinancialReports.List(ctx, projectID)
	if err != nil {
		return FinancialReportList{}, err
	}
	response := FinancialReportList{FinancialReports: make([]FinancialReport, 0, len(reports))}
	for _, report := range reports {
		response.FinancialReports = append(response.FinancialReports, FinancialReportView(report))
	}
	return response, nil
}

// FinancialReportView maps stored metadata and omits amounts that were not accepted.
func FinancialReportView(report persistence.FinancialReport) FinancialReport {
	amounts := report.Amounts
	if report.Status != persistence.FinancialReportStatusAccepted {
		amounts = nil
	}
	response := FinancialReport{
		ID: report.ID, Provider: report.Provider, ReportKind: report.ReportKind,
		PeriodStart: report.PeriodStart.UTC().Format(time.DateOnly),
		PeriodEnd:   report.PeriodEnd.UTC().Format(time.DateOnly),
		Source:      report.Source, SHA256: hex.EncodeToString(report.SHA256[:]),
		ByteSize: report.ByteSize, ContentType: report.ContentType, ParserVersion: report.ParserVersion,
		Status: report.Status, Reason: report.Reason, SupersedesImportID: report.SupersedesImportID,
		Currencies: []string{}, Amounts: make([]FinancialReportAmount, 0, len(amounts)),
		CoveredApplicationIDs: []core.ApplicationID{}, CreatedAt: report.CreatedAt,
	}
	currencies := map[string]struct{}{}
	covered := map[core.ApplicationID]struct{}{}
	for _, amount := range amounts {
		response.Amounts = append(response.Amounts, FinancialReportAmount{
			Currency: amount.Currency, AmountDecimal: amount.AmountDecimal, AmountScale: amount.AmountScale,
			MinorUnits: amount.MinorUnits, CommissionState: amount.CommissionState, TaxState: amount.TaxState,
			CoveredApplicationID: amount.CoveredApplicationID,
		})
		currencies[amount.Currency] = struct{}{}
		covered[amount.CoveredApplicationID] = struct{}{}
	}
	for currency := range currencies {
		response.Currencies = append(response.Currencies, currency)
	}
	sort.Strings(response.Currencies)
	for applicationID := range covered {
		response.CoveredApplicationIDs = append(response.CoveredApplicationIDs, applicationID)
	}
	sort.Slice(response.CoveredApplicationIDs, func(left, right int) bool {
		return response.CoveredApplicationIDs[left] < response.CoveredApplicationIDs[right]
	})
	return response
}
