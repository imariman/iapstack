package postgres

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// financialReportListLimit bounds one project import listing.
	financialReportListLimit = 200
	// financialReportByteCeiling matches the migration's stored byte-size check.
	financialReportByteCeiling int64 = 32 << 20
)

// productionApplication is one live store application that must be covered before proceeds are shown.
type productionApplication struct {
	id       core.ApplicationID
	provider core.Provider
}

// acceptedFinancialImport is the coverage candidate for one provider period.
type acceptedFinancialImport struct {
	ID          string
	Provider    core.Provider
	ReportKind  string
	PeriodStart time.Time
	PeriodEnd   time.Time
	CreatedAt   time.Time
}

var (
	// financialReportStoreContract verifies Store implements financial report persistence.
	_ persistence.FinancialReportStore = (*Store)(nil)
)

// SaveFinancialReport inserts one import or returns the row with the same digest.
func (store *Store) SaveFinancialReport(
	ctx context.Context,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	if err := validateFinancialReportDraft(draft); err != nil {
		return persistence.FinancialReport{}, false, err
	}
	var report persistence.FinancialReport
	var created bool
	err := store.transact(ctx, pgx.Serializable, func(repository *transaction) error {
		var saveErr error
		report, created, saveErr = repository.saveFinancialReport(ctx, draft)
		return saveErr
	})
	return report, created, err
}

// SupersedeFinancialReport makes a newer accepted import the only current row for its period.
func (store *Store) SupersedeFinancialReport(
	ctx context.Context,
	projectID core.ProjectID,
	importID string,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	if err := projectID.Validate(); err != nil {
		return persistence.FinancialReport{}, false, err
	}
	if err := validateFinancialReportDraft(draft); err != nil {
		return persistence.FinancialReport{}, false, err
	}
	var report persistence.FinancialReport
	var created bool
	err := store.transact(ctx, pgx.Serializable, func(repository *transaction) error {
		var saveErr error
		report, created, saveErr = repository.supersedeFinancialReport(ctx, projectID, importID, draft)
		return saveErr
	})
	return report, created, err
}

// ListFinancialReports returns secret-free import metadata, newest first.
func (store *Store) ListFinancialReports(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.FinancialReport, error) {
	if err := projectID.Validate(); err != nil {
		return nil, err
	}
	var reports []persistence.FinancialReport
	err := store.transact(ctx, pgx.RepeatableRead, func(repository *transaction) error {
		var listErr error
		reports, listErr = repository.listFinancialReports(ctx, projectID)
		return listErr
	})
	return reports, err
}

// saveFinancialReport inserts one draft inside the current transaction.
func (repository *transaction) saveFinancialReport(
	ctx context.Context,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	if err := repository.requireFinancialProject(ctx, draft.ProjectID); err != nil {
		return persistence.FinancialReport{}, false, err
	}
	existing, err := repository.findFinancialReportByDigest(ctx, draft)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		return persistence.FinancialReport{}, false, err
	}
	if draft.Status == persistence.FinancialReportStatusAccepted {
		if err := repository.ensureAcceptedPeriodAvailable(ctx, draft); err != nil {
			return persistence.FinancialReport{}, false, err
		}
	}
	return repository.insertFinancialReport(ctx, draft)
}

// supersedeFinancialReport replaces the locked accepted import inside the current transaction.
func (repository *transaction) supersedeFinancialReport(
	ctx context.Context,
	projectID core.ProjectID,
	importID string,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	if err := repository.requireFinancialProject(ctx, projectID); err != nil {
		return persistence.FinancialReport{}, false, err
	}
	current, err := repository.lockFinancialReport(ctx, projectID, importID)
	if err != nil {
		return persistence.FinancialReport{}, false, err
	}
	if current.Status != persistence.FinancialReportStatusAccepted {
		return persistence.FinancialReport{}, false, persistence.ErrFinancialReportNotAccepted
	}
	if current.Provider != draft.Provider || current.ReportKind != draft.ReportKind ||
		!sameFinancialDate(current.PeriodStart, draft.PeriodStart) ||
		!sameFinancialDate(current.PeriodEnd, draft.PeriodEnd) {
		return persistence.FinancialReport{}, false, persistence.ErrFinancialReportScopeMismatch
	}
	if draft.Status != persistence.FinancialReportStatusAccepted {
		return persistence.FinancialReport{}, false, persistence.ErrFinancialReportNotAccepted
	}
	existing, err := repository.findFinancialReportByDigest(ctx, draft)
	if err == nil {
		if existing.ID == current.ID {
			return existing, false, nil
		}
		if err := repository.markFinancialReportSuperseded(ctx, current.ID); err != nil {
			return persistence.FinancialReport{}, false, err
		}
		if err := repository.reviveFinancialReport(ctx, existing.ID, current.ID, draft); err != nil {
			return persistence.FinancialReport{}, false, err
		}
		report, err := repository.loadFinancialReport(ctx, projectID, existing.ID)
		return report, true, err
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		return persistence.FinancialReport{}, false, err
	}
	if err := repository.markFinancialReportSuperseded(ctx, current.ID); err != nil {
		return persistence.FinancialReport{}, false, err
	}
	draft.SupersedesImportID = current.ID
	return repository.insertFinancialReport(ctx, draft)
}

// listFinancialReports loads bounded import metadata and accepted amounts.
func (repository *transaction) listFinancialReports(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.FinancialReport, error) {
	if err := repository.requireFinancialProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT id, project_id, provider, report_kind, period_start, period_end, source,
			received_sha256, byte_size, content_type, parser_version, status, reason,
			supersedes_import_id, created_at
		FROM financial_report_imports
		WHERE project_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, projectID, financialReportListLimit)
	if err != nil {
		return nil, classifyError("list financial reports", err)
	}
	defer rows.Close()

	reports := make([]persistence.FinancialReport, 0)
	acceptedIDs := make([]string, 0)
	for rows.Next() {
		report, err := scanFinancialReport(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
		if report.Status == persistence.FinancialReportStatusAccepted {
			acceptedIDs = append(acceptedIDs, report.ID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate financial reports", err)
	}
	amounts, err := repository.financialReportAmounts(ctx, acceptedIDs)
	if err != nil {
		return nil, err
	}
	for index := range reports {
		reports[index].Amounts = amounts[reports[index].ID]
		if reports[index].Amounts == nil {
			reports[index].Amounts = []persistence.FinancialReportAmount{}
		}
	}
	return reports, nil
}

// requireFinancialProject rejects a missing project without revealing other tenants.
func (repository *transaction) requireFinancialProject(ctx context.Context, projectID core.ProjectID) error {
	var exists bool
	err := repository.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1)`, projectID).Scan(&exists)
	if err != nil {
		return classifyError("check financial report project", err)
	}
	if !exists {
		return persistence.ErrNotFound
	}
	return nil
}

// findFinancialReportByDigest loads the idempotent row for one received payload.
func (repository *transaction) findFinancialReportByDigest(
	ctx context.Context,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, error) {
	var importID string
	err := repository.tx.QueryRow(ctx, `
		SELECT id
		FROM financial_report_imports
		WHERE project_id = $1 AND provider = $2 AND report_kind = $3
			AND period_start = $4 AND period_end = $5 AND received_sha256 = $6
	`, draft.ProjectID, draft.Provider, draft.ReportKind, financialDate(draft.PeriodStart), financialDate(draft.PeriodEnd), draft.SHA256[:]).Scan(&importID)
	if err != nil {
		return persistence.FinancialReport{}, classifyError("find financial report digest", err)
	}
	return repository.loadFinancialReport(ctx, draft.ProjectID, importID)
}

// ensureAcceptedPeriodAvailable refuses a second current accepted import for one period.
func (repository *transaction) ensureAcceptedPeriodAvailable(
	ctx context.Context,
	draft persistence.FinancialReportDraft,
) error {
	var importID string
	err := repository.tx.QueryRow(ctx, `
		SELECT id
		FROM financial_report_imports
		WHERE project_id = $1 AND provider = $2 AND report_kind = $3
			AND period_start = $4 AND period_end = $5 AND status = 'accepted'
	`, draft.ProjectID, draft.Provider, draft.ReportKind, financialDate(draft.PeriodStart), financialDate(draft.PeriodEnd)).Scan(&importID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return classifyError("check accepted financial report period", err)
	}
	return persistence.ErrConflict
}

// insertFinancialReport writes one new import and its accepted amounts.
func (repository *transaction) insertFinancialReport(
	ctx context.Context,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	importID, err := newFinancialReportID()
	if err != nil {
		return persistence.FinancialReport{}, false, err
	}
	var supersedes *string
	if draft.SupersedesImportID != "" {
		supersedes = &draft.SupersedesImportID
	}
	_, err = repository.tx.Exec(ctx, `
		INSERT INTO financial_report_imports (
			id, project_id, provider, report_kind, period_start, period_end, source,
			received_sha256, byte_size, content_type, parser_version, status, reason,
			supersedes_import_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13,
			$14
		)
	`, importID, draft.ProjectID, draft.Provider, draft.ReportKind, financialDate(draft.PeriodStart), financialDate(draft.PeriodEnd), draft.Source,
		draft.SHA256[:], draft.ByteSize, draft.ContentType, draft.ParserVersion, draft.Status, draft.Reason, supersedes)
	if err != nil {
		switch financialConstraint(err) {
		case "financial_report_imports_digest_unique":
			existing, readErr := repository.findFinancialReportByDigest(ctx, draft)
			if readErr != nil {
				return persistence.FinancialReport{}, false, readErr
			}
			return existing, false, nil
		case "financial_report_imports_one_accepted_period_idx":
			return persistence.FinancialReport{}, false, persistence.ErrConflict
		default:
			return persistence.FinancialReport{}, false, classifyError("insert financial report", err)
		}
	}
	if err := repository.insertFinancialReportAmounts(ctx, draft.ProjectID, importID, draft); err != nil {
		return persistence.FinancialReport{}, false, err
	}
	report, err := repository.loadFinancialReport(ctx, draft.ProjectID, importID)
	return report, true, err
}

// reviveFinancialReport makes an older digest the accepted import and rewrites its amounts.
func (repository *transaction) reviveFinancialReport(
	ctx context.Context,
	importID string,
	supersedesID string,
	draft persistence.FinancialReportDraft,
) error {
	tag, err := repository.tx.Exec(ctx, `
		UPDATE financial_report_imports
		SET status = 'accepted', reason = '', supersedes_import_id = $2
		WHERE id = $1 AND status <> 'accepted'
	`, importID, supersedesID)
	if err != nil {
		return classifyError("revive financial report", err)
	}
	if tag.RowsAffected() != 1 {
		return persistence.ErrConflict
	}
	if _, err := repository.tx.Exec(ctx, `DELETE FROM financial_report_amounts WHERE import_id = $1`, importID); err != nil {
		return classifyError("clear revived financial report amounts", err)
	}
	return repository.insertFinancialReportAmounts(ctx, draft.ProjectID, importID, draft)
}

// markFinancialReportSuperseded removes one import from coverage without deleting its history.
func (repository *transaction) markFinancialReportSuperseded(ctx context.Context, importID string) error {
	if _, err := repository.tx.Exec(ctx, `DELETE FROM financial_report_amounts WHERE import_id = $1`, importID); err != nil {
		return classifyError("delete superseded financial report amounts", err)
	}
	tag, err := repository.tx.Exec(ctx, `
		UPDATE financial_report_imports
		SET status = 'superseded', reason = 'superseded'
		WHERE id = $1 AND status = 'accepted'
	`, importID)
	if err != nil {
		return classifyError("supersede financial report", err)
	}
	if tag.RowsAffected() != 1 {
		return persistence.ErrConflict
	}
	return nil
}

// insertFinancialReportAmounts matches Apple identifiers to production applications and stores totals.
func (repository *transaction) insertFinancialReportAmounts(
	ctx context.Context,
	projectID core.ProjectID,
	importID string,
	draft persistence.FinancialReportDraft,
) error {
	if draft.Status != persistence.FinancialReportStatusAccepted {
		return nil
	}
	resolved, err := repository.resolveFinancialReportAmounts(ctx, projectID, draft.IdentifierTotals)
	if err != nil {
		return err
	}
	for _, amount := range resolved {
		var minor any
		if amount.MinorUnits != nil {
			minor = *amount.MinorUnits
		}
		_, err := repository.tx.Exec(ctx, `
			INSERT INTO financial_report_amounts (
				import_id, currency, amount_decimal, amount_scale, minor_units,
				commission_state, tax_state, covered_application_id
			) VALUES ($1, $2, $3::numeric, $4, $5, $6, $7, $8)
		`, importID, amount.Currency, amount.AmountDecimal, amount.AmountScale, minor,
			persistence.FinancialReportCommissionIncluded, persistence.FinancialReportTaxNotItemized,
			amount.CoveredApplicationID)
		if err != nil {
			return classifyError("insert financial report amount", err)
		}
	}
	return nil
}

// resolveFinancialReportAmounts keeps production Apple matches and drops every other identifier.
func (repository *transaction) resolveFinancialReportAmounts(
	ctx context.Context,
	projectID core.ProjectID,
	totals []persistence.FinancialReportIdentifierTotal,
) ([]persistence.FinancialReportAmount, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, provider_application_id
		FROM applications
		WHERE project_id = $1 AND provider = 'apple_app_store' AND environment = 'production'
	`, projectID)
	if err != nil {
		return nil, classifyError("list production apple applications", err)
	}
	defer rows.Close()

	matches := map[string][]core.ApplicationID{}
	for rows.Next() {
		var applicationID core.ApplicationID
		var providerApplicationID string
		if err := rows.Scan(&applicationID, &providerApplicationID); err != nil {
			return nil, classifyError("scan production apple application", err)
		}
		matches[providerApplicationID] = append(matches[providerApplicationID], applicationID)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate production apple applications", err)
	}

	resolved := map[string]persistence.FinancialReportAmount{}
	for _, total := range totals {
		if err := validateIdentifierTotal(total); err != nil {
			return nil, err
		}
		applications := matches[total.ProviderApplicationID]
		switch len(applications) {
		case 0:
			continue
		case 1:
			key := string(applications[0]) + "\x00" + total.Currency
			amount, err := financialAmountFromTotal(applications[0], total)
			if err != nil {
				return nil, err
			}
			current, exists := resolved[key]
			if !exists {
				resolved[key] = amount
				continue
			}
			merged, err := mergeFinancialAmounts(current, amount)
			if err != nil {
				return nil, err
			}
			resolved[key] = merged
		default:
			return nil, persistence.ErrFinancialReportAmbiguous
		}
	}
	amounts := make([]persistence.FinancialReportAmount, 0, len(resolved))
	for _, amount := range resolved {
		amounts = append(amounts, amount)
	}
	sort.Slice(amounts, func(left, right int) bool {
		if amounts[left].CoveredApplicationID != amounts[right].CoveredApplicationID {
			return amounts[left].CoveredApplicationID < amounts[right].CoveredApplicationID
		}
		return amounts[left].Currency < amounts[right].Currency
	})
	return amounts, nil
}

// loadFinancialReport reads one project-scoped import and its accepted amounts.
func (repository *transaction) loadFinancialReport(
	ctx context.Context,
	projectID core.ProjectID,
	importID string,
) (persistence.FinancialReport, error) {
	row := repository.tx.QueryRow(ctx, `
		SELECT id, project_id, provider, report_kind, period_start, period_end, source,
			received_sha256, byte_size, content_type, parser_version, status, reason,
			supersedes_import_id, created_at
		FROM financial_report_imports
		WHERE project_id = $1 AND id = $2
	`, projectID, importID)
	report, err := scanFinancialReport(row)
	if err != nil {
		return persistence.FinancialReport{}, err
	}
	if report.Status == persistence.FinancialReportStatusAccepted {
		amounts, err := repository.financialReportAmounts(ctx, []string{report.ID})
		if err != nil {
			return persistence.FinancialReport{}, err
		}
		report.Amounts = amounts[report.ID]
	}
	if report.Amounts == nil {
		report.Amounts = []persistence.FinancialReportAmount{}
	}
	return report, nil
}

// lockFinancialReport locks one import before a supersede decision.
func (repository *transaction) lockFinancialReport(
	ctx context.Context,
	projectID core.ProjectID,
	importID string,
) (persistence.FinancialReport, error) {
	var importKey string
	err := repository.tx.QueryRow(ctx, `
		SELECT id
		FROM financial_report_imports
		WHERE project_id = $1 AND id = $2
		FOR UPDATE
	`, projectID, importID).Scan(&importKey)
	if err != nil {
		return persistence.FinancialReport{}, classifyError("lock financial report", err)
	}
	return repository.loadFinancialReport(ctx, projectID, importKey)
}

// financialReportAmounts loads accepted amount rows grouped by import.
func (repository *transaction) financialReportAmounts(
	ctx context.Context,
	importIDs []string,
) (map[string][]persistence.FinancialReportAmount, error) {
	grouped := map[string][]persistence.FinancialReportAmount{}
	if len(importIDs) == 0 {
		return grouped, nil
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT import_id, currency, amount_decimal::text, amount_scale, minor_units,
			commission_state, tax_state, covered_application_id
		FROM financial_report_amounts
		WHERE import_id = ANY($1)
		ORDER BY import_id, covered_application_id, currency
	`, importIDs)
	if err != nil {
		return nil, classifyError("list financial report amounts", err)
	}
	defer rows.Close()
	for rows.Next() {
		var importID string
		var rawAmount string
		var amount persistence.FinancialReportAmount
		if err := rows.Scan(
			&importID, &amount.Currency, &rawAmount, &amount.AmountScale, &amount.MinorUnits,
			&amount.CommissionState, &amount.TaxState, &amount.CoveredApplicationID,
		); err != nil {
			return nil, classifyError("scan financial report amount", err)
		}
		canonical, err := canonicalFinancialDecimal(rawAmount, amount.AmountScale)
		if err != nil {
			return nil, err
		}
		amount.AmountDecimal = canonical
		grouped[importID] = append(grouped[importID], amount)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate financial report amounts", err)
	}
	return grouped, nil
}

// adminRecognizedRevenue decides whether official reports cover every production application.
func (repository *transaction) adminRecognizedRevenue(
	ctx context.Context,
	projectID core.ProjectID,
	productionCount int64,
	testCount int64,
	query persistence.AdminRevenueQuery,
) (persistence.AdminRevenue, error) {
	if err := validateFinancialPeriod(query); err != nil {
		return persistence.AdminRevenue{}, err
	}
	revenue := persistence.AdminRevenue{
		ProductionApplicationCount: productionCount,
		TestApplicationCount:       testCount,
		Amounts:                    []persistence.AdminRecognizedAmount{},
		UncoveredProviders:         []core.Provider{},
	}
	if productionCount == 0 {
		zero := int64(0)
		revenue.RecognizedMinorUnits = &zero
		if testCount > 0 {
			revenue.Status = persistence.AdminRevenueSandboxOnly
		} else {
			revenue.Status = persistence.AdminRevenueNoApplications
		}
		return revenue, nil
	}

	applications, err := repository.productionApplications(ctx, projectID)
	if err != nil {
		return persistence.AdminRevenue{}, err
	}
	if len(applications) == 0 {
		revenue.Status = persistence.AdminRevenueStoreReportsRequired
		return revenue, nil
	}
	imports, err := repository.acceptedFinancialImports(ctx, projectID)
	if err != nil {
		return persistence.AdminRevenue{}, err
	}
	chosen := chooseFinancialImports(imports, query)
	importIDs := make([]string, 0, len(chosen))
	for _, report := range chosen {
		importIDs = append(importIDs, report.ID)
	}
	amountRows, err := repository.financialReportAmounts(ctx, importIDs)
	if err != nil {
		return persistence.AdminRevenue{}, err
	}

	providers := productionProviders(applications)
	amounts := make([]persistence.AdminRecognizedAmount, 0)
	uncovered := make([]core.Provider, 0)
	allCovered := true
	for _, provider := range providers {
		kind, known := financialReportKind(provider)
		report, selected := chosen[provider]
		rows := amountRows[report.ID]
		if !known || !selected || report.ReportKind != kind || !coversApplications(applications, provider, rows) {
			allCovered = false
			uncovered = append(uncovered, provider)
			continue
		}
		summed, ok := sumRecognizedAmounts(provider, report, rows)
		if !ok {
			allCovered = false
			uncovered = append(uncovered, provider)
			continue
		}
		amounts = append(amounts, summed...)
	}
	if !allCovered {
		revenue.Status = persistence.AdminRevenueStoreReportsRequired
		revenue.UncoveredProviders = uncovered
		return revenue, nil
	}
	revenue.Status = persistence.AdminRevenueReportsImported
	revenue.Amounts = amounts
	return revenue, nil
}

// productionApplications lists every production application without the dashboard collection cap.
func (repository *transaction) productionApplications(
	ctx context.Context,
	projectID core.ProjectID,
) ([]productionApplication, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, provider
		FROM applications
		WHERE project_id = $1 AND environment = 'production'
		ORDER BY provider, id
	`, projectID)
	if err != nil {
		return nil, classifyError("list production applications", err)
	}
	defer rows.Close()
	applications := make([]productionApplication, 0)
	for rows.Next() {
		var application productionApplication
		var provider string
		if err := rows.Scan(&application.id, &provider); err != nil {
			return nil, classifyError("scan production application", err)
		}
		application.provider = core.Provider(provider)
		applications = append(applications, application)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate production applications", err)
	}
	return applications, nil
}

// acceptedFinancialImports loads current accepted imports, latest period first.
func (repository *transaction) acceptedFinancialImports(
	ctx context.Context,
	projectID core.ProjectID,
) ([]acceptedFinancialImport, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, provider, report_kind, period_start, period_end, created_at
		FROM financial_report_imports
		WHERE project_id = $1 AND status = 'accepted'
		ORDER BY provider, period_end DESC, period_start DESC, created_at DESC, id DESC
	`, projectID)
	if err != nil {
		return nil, classifyError("list accepted financial reports", err)
	}
	defer rows.Close()
	imports := make([]acceptedFinancialImport, 0)
	for rows.Next() {
		var report acceptedFinancialImport
		var provider string
		if err := rows.Scan(
			&report.ID, &provider, &report.ReportKind,
			&report.PeriodStart, &report.PeriodEnd, &report.CreatedAt,
		); err != nil {
			return nil, classifyError("scan accepted financial report", err)
		}
		report.Provider = core.Provider(provider)
		imports = append(imports, report)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate accepted financial reports", err)
	}
	return imports, nil
}

// chooseFinancialImports keeps the latest accepted import per provider, or the requested period only.
func chooseFinancialImports(
	imports []acceptedFinancialImport,
	query persistence.AdminRevenueQuery,
) map[core.Provider]acceptedFinancialImport {
	chosen := map[core.Provider]acceptedFinancialImport{}
	for _, report := range imports {
		if query.FinancialPeriodStart != nil {
			if !sameFinancialDate(report.PeriodStart, *query.FinancialPeriodStart) ||
				!sameFinancialDate(report.PeriodEnd, *query.FinancialPeriodEnd) {
				continue
			}
		}
		if _, exists := chosen[report.Provider]; exists {
			continue
		}
		chosen[report.Provider] = report
	}
	return chosen
}

// productionProviders returns the distinct production providers in stable order.
func productionProviders(applications []productionApplication) []core.Provider {
	seen := map[core.Provider]struct{}{}
	providers := make([]core.Provider, 0)
	for _, application := range applications {
		if _, exists := seen[application.provider]; exists {
			continue
		}
		seen[application.provider] = struct{}{}
		providers = append(providers, application.provider)
	}
	sort.Slice(providers, func(left, right int) bool { return providers[left] < providers[right] })
	return providers
}

// coversApplications reports whether every production application for one provider has an amount row.
func coversApplications(
	applications []productionApplication,
	provider core.Provider,
	amounts []persistence.FinancialReportAmount,
) bool {
	covered := map[core.ApplicationID]struct{}{}
	for _, amount := range amounts {
		covered[amount.CoveredApplicationID] = struct{}{}
	}
	for _, application := range applications {
		if application.provider != provider {
			continue
		}
		if _, ok := covered[application.id]; !ok {
			return false
		}
	}
	return true
}

// sumRecognizedAmounts totals one provider's covered applications without converting currency.
func sumRecognizedAmounts(
	provider core.Provider,
	report acceptedFinancialImport,
	rows []persistence.FinancialReportAmount,
) ([]persistence.AdminRecognizedAmount, bool) {
	grouped := map[string][]persistence.FinancialReportAmount{}
	for _, row := range rows {
		grouped[row.Currency] = append(grouped[row.Currency], row)
	}
	currencies := make([]string, 0, len(grouped))
	for currency := range grouped {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	amounts := make([]persistence.AdminRecognizedAmount, 0, len(currencies))
	for _, currency := range currencies {
		var total persistence.Decimal
		var minor *int64
		minorValid := true
		var running int64
		commission := ""
		tax := ""
		for index, row := range grouped[currency] {
			parsed, err := persistence.ParseDecimal(row.AmountDecimal)
			if err != nil {
				return nil, false
			}
			if commission == "" {
				commission = row.CommissionState
				tax = row.TaxState
				total = parsed
			} else if commission != row.CommissionState || tax != row.TaxState {
				return nil, false
			} else {
				summed, err := total.Add(parsed)
				if err != nil {
					return nil, false
				}
				total = summed
			}
			if row.MinorUnits == nil {
				minorValid = false
				continue
			}
			if !minorValid {
				continue
			}
			if index == 0 {
				running = *row.MinorUnits
				continue
			}
			next, ok := addInt64(running, *row.MinorUnits)
			if !ok {
				minorValid = false
				continue
			}
			running = next
		}
		if minorValid {
			minor = &running
		}
		amounts = append(amounts, persistence.AdminRecognizedAmount{
			Provider: provider, ReportKind: report.ReportKind,
			PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd,
			Currency: currency, AmountDecimal: total.String(), AmountScale: total.Scale(),
			MinorUnits: minor, CommissionState: commission, TaxState: tax,
		})
	}
	return amounts, true
}

// financialReportKind returns the report a production provider must have before it is covered.
func financialReportKind(provider core.Provider) (string, bool) {
	switch provider {
	case core.ProviderAppleAppStore:
		return persistence.FinancialReportKindAppleConsolidated, true
	case core.ProviderGooglePlay:
		return persistence.FinancialReportKindGoogleEarnings, true
	case core.ProviderHuaweiAppGallery:
		return persistence.FinancialReportKindHuaweiSettlement, true
	default:
		return "", false
	}
}

// validateFinancialReportDraft rejects imports that cannot be stored safely.
func validateFinancialReportDraft(draft persistence.FinancialReportDraft) error {
	kind, known := financialReportKind(draft.Provider)
	if err := draft.ProjectID.Validate(); err != nil || !known || draft.ReportKind != kind {
		return validation.Wrap(errors.New("financial report scope is invalid"))
	}
	if draft.PeriodStart.IsZero() || draft.PeriodEnd.IsZero() || draft.PeriodEnd.Before(draft.PeriodStart) {
		return validation.Wrap(errors.New("financial report period is invalid"))
	}
	if draft.Source != persistence.FinancialReportSourceUpload && draft.Source != persistence.FinancialReportSourceProviderAPI {
		return validation.Wrap(errors.New("financial report source is invalid"))
	}
	if draft.ByteSize <= 0 || draft.ByteSize > financialReportByteCeiling {
		return validation.Wrap(errors.New("financial report size is invalid"))
	}
	if draft.ContentType == "" || draft.ParserVersion == "" {
		return validation.Wrap(errors.New("financial report metadata is invalid"))
	}
	switch draft.Status {
	case persistence.FinancialReportStatusAccepted:
		if draft.Reason != "" || draft.Provider != core.ProviderAppleAppStore {
			return validation.Wrap(persistence.ErrFinancialReportNotAccepted)
		}
	case persistence.FinancialReportStatusUnrecognized, persistence.FinancialReportStatusRejected, persistence.FinancialReportStatusSuperseded:
		if draft.Reason == "" || len(draft.IdentifierTotals) > 0 {
			return validation.Wrap(errors.New("financial report status is invalid"))
		}
	default:
		return validation.Wrap(errors.New("financial report status is invalid"))
	}
	return nil
}

// validateIdentifierTotal checks one summed identifier without including its value in the error.
func validateIdentifierTotal(total persistence.FinancialReportIdentifierTotal) error {
	if total.ProviderApplicationID == "" || total.Amount.Sign() == 0 && total.Amount.String() == "" {
		return validation.Wrap(errors.New("financial report amount is invalid"))
	}
	if !currencyCodeValid(total.Currency) {
		return validation.Wrap(errors.New("financial report currency is invalid"))
	}
	if total.MinorUnits != nil {
		coefficient, ok := total.Amount.ExactInt64()
		if !ok || coefficient != *total.MinorUnits {
			return validation.Wrap(errors.New("financial report minor units do not match the decimal"))
		}
	}
	return nil
}

// financialAmountFromTotal copies one matched total into a stored amount row.
func financialAmountFromTotal(
	applicationID core.ApplicationID,
	total persistence.FinancialReportIdentifierTotal,
) (persistence.FinancialReportAmount, error) {
	if total.Amount.String() == "" {
		return persistence.FinancialReportAmount{}, validation.Wrap(errors.New("financial report amount is invalid"))
	}
	return persistence.FinancialReportAmount{
		Currency: total.Currency, AmountDecimal: total.Amount.String(), AmountScale: total.Amount.Scale(),
		MinorUnits: total.MinorUnits, CommissionState: persistence.FinancialReportCommissionIncluded,
		TaxState: persistence.FinancialReportTaxNotItemized, CoveredApplicationID: applicationID,
	}, nil
}

// mergeFinancialAmounts adds two rows for the same application and currency.
func mergeFinancialAmounts(left, right persistence.FinancialReportAmount) (persistence.FinancialReportAmount, error) {
	leftDecimal, err := persistence.ParseDecimal(left.AmountDecimal)
	if err != nil {
		return persistence.FinancialReportAmount{}, validation.Wrap(err)
	}
	rightDecimal, err := persistence.ParseDecimal(right.AmountDecimal)
	if err != nil {
		return persistence.FinancialReportAmount{}, validation.Wrap(err)
	}
	summed, err := leftDecimal.Add(rightDecimal)
	if err != nil {
		return persistence.FinancialReportAmount{}, validation.Wrap(err)
	}
	left.AmountDecimal = summed.String()
	left.AmountScale = summed.Scale()
	left.MinorUnits = combineMinorUnits(left.MinorUnits, right.MinorUnits)
	if left.MinorUnits != nil {
		coefficient, ok := summed.ExactInt64()
		if !ok || coefficient != *left.MinorUnits {
			return persistence.FinancialReportAmount{}, validation.Wrap(errors.New("financial report minor units do not match the decimal"))
		}
	}
	return left, nil
}

// combineMinorUnits sums minor units only when both sides are present.
func combineMinorUnits(left, right *int64) *int64 {
	if left == nil || right == nil {
		return nil
	}
	sum, ok := addInt64(*left, *right)
	if !ok {
		return nil
	}
	return &sum
}

// canonicalFinancialDecimal restores the stored scale after a numeric text conversion.
func canonicalFinancialDecimal(raw string, scale int32) (string, error) {
	parsed, err := persistence.ParseDecimal(raw)
	if err != nil {
		return "", fmt.Errorf("parse stored financial amount: %w", err)
	}
	scaled, err := parsed.Rescale(scale)
	if err != nil {
		return "", fmt.Errorf("restore stored financial amount scale: %w", err)
	}
	return scaled.String(), nil
}

// scanFinancialReport reads one import row from a query or row.
func scanFinancialReport(row interface{ Scan(...any) error }) (persistence.FinancialReport, error) {
	var report persistence.FinancialReport
	var provider string
	var digest []byte
	var supersedes *string
	err := row.Scan(
		&report.ID, &report.ProjectID, &provider, &report.ReportKind,
		&report.PeriodStart, &report.PeriodEnd, &report.Source,
		&digest, &report.ByteSize, &report.ContentType, &report.ParserVersion,
		&report.Status, &report.Reason, &supersedes, &report.CreatedAt,
	)
	if err != nil {
		return persistence.FinancialReport{}, classifyError("scan financial report", err)
	}
	if len(digest) != len(report.SHA256) {
		return persistence.FinancialReport{}, errors.New("financial report digest has an invalid length")
	}
	report.Provider = core.Provider(provider)
	copy(report.SHA256[:], digest)
	report.SupersedesImportID = supersedes
	report.Amounts = []persistence.FinancialReportAmount{}
	return report, nil
}

// validateFinancialPeriod requires both bounds or neither, and a forward range.
func validateFinancialPeriod(query persistence.AdminRevenueQuery) error {
	if (query.FinancialPeriodStart == nil) != (query.FinancialPeriodEnd == nil) {
		return validation.Wrap(errors.New("financial period requires both bounds"))
	}
	if query.FinancialPeriodStart != nil && query.FinancialPeriodEnd.Before(*query.FinancialPeriodStart) {
		return validation.Wrap(errors.New("financial period end is before the start"))
	}
	return nil
}

// financialDate returns the UTC civil date stored for a report period.
func financialDate(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// sameFinancialDate compares two timestamps as UTC civil dates.
func sameFinancialDate(left, right time.Time) bool {
	return financialDate(left).Equal(financialDate(right))
}

// currencyCodeValid reports whether a currency is three ASCII letters.
func currencyCodeValid(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, character := range code {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

// financialConstraint returns a PostgreSQL constraint name when present.
func financialConstraint(err error) string {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.ConstraintName
	}
	return ""
}

// newFinancialReportID returns a random hex import identity.
func newFinancialReportID() (string, error) {
	var buf [16]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("create financial report id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// addInt64 adds two minor-unit totals and reports overflow.
func addInt64(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, false
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, false
	}
	return left + right, true
}
