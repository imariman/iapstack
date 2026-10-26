package financialreports

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"mime"
	"strings"
	"time"
	"unicode"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// DefaultByteLimit is the maximum accepted financial-report body.
	DefaultByteLimit int64 = 32 << 20
	// DefaultRowLimit is the maximum accepted Apple data-row count.
	DefaultRowLimit = 100000
	// ParserVersionAppleConsolidated identifies the consolidated Apple column contract.
	ParserVersionAppleConsolidated = "apple-financial-consolidated-1"
	// ParserVersionFixtureRequired identifies a provider parser that has no pinned fixture.
	ParserVersionFixtureRequired = "fixture-required"
)

var (
	// ErrReportTooLarge indicates that received or decompressed report bytes exceed the limit.
	ErrReportTooLarge = errors.New("financial report exceeds the byte limit")
	// ErrPullUnpinned indicates that a provider pull has no captured official parameter contract.
	ErrPullUnpinned = errors.New("provider financial report pull is not pinned")
)

// Service imports one explicit financial report without estimating revenue.
type Service struct {
	store     persistence.FinancialReportStore
	logger    *slog.Logger
	byteLimit int64
	rowLimit  int
}

// Upload is one operator-supplied report file.
type Upload struct {
	ProjectID   core.ProjectID
	Provider    core.Provider
	ReportKind  string
	ContentType string
	PeriodStart *time.Time
	PeriodEnd   *time.Time
	Body        []byte
}

// PullRequest asks for one provider report period. No provider is called until its contract is pinned.
type PullRequest struct {
	ProjectID   core.ProjectID
	Provider    core.Provider
	ReportKind  string
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// NewService validates the store, logger, and positive import limits.
func NewService(
	store persistence.FinancialReportStore,
	logger *slog.Logger,
	byteLimit int64,
	rowLimit int,
) (*Service, error) {
	if store == nil {
		return nil, errors.New("financial report store is required")
	}
	if logger == nil {
		return nil, errors.New("financial report logger is required")
	}
	if byteLimit <= 0 || rowLimit <= 0 {
		return nil, errors.New("financial report limits must be positive")
	}
	return &Service{store: store, logger: logger, byteLimit: byteLimit, rowLimit: rowLimit}, nil
}

// ByteLimit returns the configured received-byte ceiling.
func (service *Service) ByteLimit() int64 {
	if service == nil {
		return 0
	}
	return service.byteLimit
}

// Upload stores one file or returns the existing digest. Raw bytes are not retained.
func (service *Service) Upload(ctx context.Context, upload Upload) (persistence.FinancialReport, bool, error) {
	draft, err := service.prepareDraft(upload)
	if err != nil {
		service.logFailure(ctx, upload.ProjectID, upload.Provider, upload.ReportKind, upload.PeriodStart, upload.PeriodEnd, "", err)
		return persistence.FinancialReport{}, false, classifyFinancialError(err)
	}
	report, created, err := service.store.SaveFinancialReport(ctx, draft)
	if err != nil {
		service.logFailure(ctx, upload.ProjectID, upload.Provider, upload.ReportKind, &draft.PeriodStart, &draft.PeriodEnd, "", err)
		return persistence.FinancialReport{}, false, classifyFinancialError(err)
	}
	service.logSuccess(ctx, report)
	return report, created, nil
}

// Supersede replaces one accepted period with a newer accepted file and does not sum the two.
func (service *Service) Supersede(
	ctx context.Context,
	projectID core.ProjectID,
	importID string,
	upload Upload,
) (persistence.FinancialReport, bool, error) {
	if err := validImportID(importID); err != nil {
		service.logFailure(ctx, projectID, upload.Provider, upload.ReportKind, nil, nil, importID, err)
		return persistence.FinancialReport{}, false, validation.Wrap(err)
	}
	upload.ProjectID = projectID
	draft, err := service.prepareDraft(upload)
	if err != nil {
		service.logFailure(ctx, projectID, upload.Provider, upload.ReportKind, upload.PeriodStart, upload.PeriodEnd, importID, err)
		return persistence.FinancialReport{}, false, classifyFinancialError(err)
	}
	if draft.Status != persistence.FinancialReportStatusAccepted {
		err = persistence.ErrFinancialReportNotAccepted
		service.logFailure(ctx, projectID, upload.Provider, upload.ReportKind, &draft.PeriodStart, &draft.PeriodEnd, importID, err)
		return persistence.FinancialReport{}, false, validation.Wrap(err)
	}
	report, created, err := service.store.SupersedeFinancialReport(ctx, projectID, importID, draft)
	if err != nil {
		service.logFailure(ctx, projectID, upload.Provider, upload.ReportKind, &draft.PeriodStart, &draft.PeriodEnd, importID, err)
		return persistence.FinancialReport{}, false, classifyFinancialError(err)
	}
	service.logSuccess(ctx, report)
	return report, created, nil
}

// List returns secret-free import metadata for one project.
func (service *Service) List(ctx context.Context, projectID core.ProjectID) ([]persistence.FinancialReport, error) {
	if err := projectID.Validate(); err != nil {
		return nil, validation.Wrap(err)
	}
	reports, err := service.store.ListFinancialReports(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if reports == nil {
		return []persistence.FinancialReport{}, nil
	}
	return reports, nil
}

// Pull validates one explicit provider pull and refuses it until the official contract is pinned.
func (service *Service) Pull(ctx context.Context, request PullRequest) error {
	if err := request.ProjectID.Validate(); err != nil {
		return validation.Wrap(err)
	}
	kind, ok := ReportKind(request.Provider)
	if !ok || request.ReportKind != kind {
		err := errors.New("report kind does not match provider")
		service.logFailure(ctx, request.ProjectID, request.Provider, request.ReportKind, &request.PeriodStart, &request.PeriodEnd, "", err)
		return validation.Wrap(err)
	}
	if request.PeriodStart.IsZero() || request.PeriodEnd.IsZero() || request.PeriodEnd.Before(request.PeriodStart) {
		err := errors.New("financial report period is invalid")
		service.logFailure(ctx, request.ProjectID, request.Provider, request.ReportKind, &request.PeriodStart, &request.PeriodEnd, "", err)
		return validation.Wrap(err)
	}
	service.logFailure(ctx, request.ProjectID, request.Provider, request.ReportKind, &request.PeriodStart, &request.PeriodEnd, "", ErrPullUnpinned)
	return validation.Wrap(ErrPullUnpinned)
}

// ReportKind returns the only financial report kind recognized for a purchase provider.
func ReportKind(provider core.Provider) (string, bool) {
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

// prepareDraft hashes and parses one upload without retaining the bytes.
func (service *Service) prepareDraft(upload Upload) (persistence.FinancialReportDraft, error) {
	if err := upload.ProjectID.Validate(); err != nil {
		return persistence.FinancialReportDraft{}, err
	}
	kind, ok := ReportKind(upload.Provider)
	if !ok || upload.ReportKind != kind {
		return persistence.FinancialReportDraft{}, errors.New("report kind does not match provider")
	}
	if len(upload.Body) == 0 {
		return persistence.FinancialReportDraft{}, errors.New("financial report file is empty")
	}
	if int64(len(upload.Body)) > service.byteLimit {
		return persistence.FinancialReportDraft{}, ErrReportTooLarge
	}
	decoded, owned, err := decodeReportBody(upload.Body, service.byteLimit)
	if err != nil {
		return persistence.FinancialReportDraft{}, err
	}
	if owned {
		defer zeroBytes(decoded)
	}
	draft := persistence.FinancialReportDraft{
		ProjectID:     upload.ProjectID,
		Provider:      upload.Provider,
		ReportKind:    upload.ReportKind,
		Source:        persistence.FinancialReportSourceUpload,
		SHA256:        sha256.Sum256(upload.Body),
		ByteSize:      int64(len(upload.Body)),
		ContentType:   normalizeContentType(upload.ContentType),
		ParserVersion: ParserVersionFixtureRequired,
		Status:        persistence.FinancialReportStatusUnrecognized,
		Reason:        persistence.FinancialReportReasonFixtureRequired,
	}
	switch upload.Provider {
	case core.ProviderAppleAppStore:
		if upload.PeriodStart != nil || upload.PeriodEnd != nil {
			return persistence.FinancialReportDraft{}, errors.New("apple financial report period comes from the file")
		}
		parsed, err := ParseAppleConsolidated(decoded, service.rowLimit)
		if err != nil {
			return persistence.FinancialReportDraft{}, err
		}
		draft.PeriodStart = parsed.PeriodStart
		draft.PeriodEnd = parsed.PeriodEnd
		draft.ParserVersion = ParserVersionAppleConsolidated
		draft.Status = persistence.FinancialReportStatusAccepted
		draft.Reason = ""
		draft.IdentifierTotals = make([]persistence.FinancialReportIdentifierTotal, 0, len(parsed.Totals))
		for _, total := range parsed.Totals {
			draft.IdentifierTotals = append(draft.IdentifierTotals, persistence.FinancialReportIdentifierTotal{
				ProviderApplicationID: total.AppleIdentifier,
				Currency:              total.Currency,
				Amount:                total.Amount,
				MinorUnits:            total.MinorUnits,
			})
		}
	case core.ProviderGooglePlay, core.ProviderHuaweiAppGallery:
		if upload.PeriodStart == nil || upload.PeriodEnd == nil || upload.PeriodEnd.Before(*upload.PeriodStart) {
			return persistence.FinancialReportDraft{}, errors.New("financial report period is required")
		}
		draft.PeriodStart = dateOnly(*upload.PeriodStart)
		draft.PeriodEnd = dateOnly(*upload.PeriodEnd)
	default:
		return persistence.FinancialReportDraft{}, errors.New("financial report provider is unsupported")
	}
	return draft, nil
}

// decodeReportBody unwraps a gzip financial report without reading past the byte limit.
func decodeReportBody(received []byte, limit int64) ([]byte, bool, error) {
	if len(received) < 2 || received[0] != 0x1f || received[1] != 0x8b {
		return received, false, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(received))
	if err != nil {
		return nil, false, errors.New("financial report gzip is invalid")
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, errors.New("financial report gzip is truncated")
	}
	if int64(len(decoded)) > limit {
		return nil, false, ErrReportTooLarge
	}
	return decoded, true, nil
}

// normalizeContentType keeps a media type and drops parameters that could carry a filename.
func normalizeContentType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || mediaType == "" || len(mediaType) > 200 {
		return "application/octet-stream"
	}
	return mediaType
}

// validImportID rejects blank, decorated, or unsafe import identities.
func validImportID(id string) error {
	if id == "" || strings.TrimSpace(id) != id || strings.ContainsAny(id, ":/\\") {
		return errors.New("financial report import id is invalid")
	}
	for _, character := range id {
		if unicode.IsControl(character) {
			return errors.New("financial report import id is invalid")
		}
	}
	return nil
}

// dateOnly converts a timestamp to a UTC civil date.
func dateOnly(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// classifyFinancialError preserves storage outcomes and marks parser failures as invalid input.
func classifyFinancialError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrReportTooLarge), errors.Is(err, persistence.ErrNotFound),
		errors.Is(err, persistence.ErrConflict), errors.Is(err, persistence.ErrUnavailable):
		return err
	default:
		return validation.Wrap(err)
	}
}

// logSuccess records one completed import without file bytes or store identifiers.
func (service *Service) logSuccess(ctx context.Context, report persistence.FinancialReport) {
	service.logImport(ctx, slog.LevelInfo, report.ProjectID, report.Provider, report.ReportKind, report.Status, "", report.ID, report.PeriodStart, report.PeriodEnd, 0, "")
}

// logFailure records one refusal using a failure code instead of report cells.
func (service *Service) logFailure(
	ctx context.Context,
	projectID core.ProjectID,
	provider core.Provider,
	kind string,
	periodStart *time.Time,
	periodEnd *time.Time,
	importID string,
	err error,
) {
	code, row, column := failureCode(err)
	var start time.Time
	var end time.Time
	if periodStart != nil {
		start = *periodStart
	}
	if periodEnd != nil {
		end = *periodEnd
	}
	service.logImport(ctx, slog.LevelWarn, projectID, provider, kind, "", code, importID, start, end, row, column)
}

// failureCode maps an import error to a log-safe code.
func failureCode(err error) (string, int, string) {
	var rowFailure *RowFailure
	if errors.As(err, &rowFailure) {
		return rowFailure.Code, rowFailure.Row, rowFailure.Column
	}
	switch {
	case errors.Is(err, persistence.ErrFinancialReportAmbiguous):
		return "ambiguous_application", 0, ""
	case errors.Is(err, persistence.ErrFinancialReportNotAccepted):
		return "not_accepted", 0, ""
	case errors.Is(err, persistence.ErrFinancialReportScopeMismatch):
		return "scope_mismatch", 0, ""
	case errors.Is(err, ErrReportTooLarge):
		return "body_too_large", 0, ""
	case errors.Is(err, ErrPullUnpinned):
		return "pull_unpinned", 0, ""
	case errors.Is(err, persistence.ErrConflict):
		return "period_occupied", 0, ""
	default:
		return "invalid_report", 0, ""
	}
}

// logImport writes the allow-listed financial-report log fields.
func (service *Service) logImport(
	ctx context.Context,
	level slog.Level,
	projectID core.ProjectID,
	provider core.Provider,
	kind string,
	status string,
	failureCode string,
	importID string,
	periodStart time.Time,
	periodEnd time.Time,
	row int,
	column string,
) {
	if service == nil || service.logger == nil {
		return
	}
	attributes := []slog.Attr{
		slog.String("project_id", string(projectID)),
		slog.String("provider", string(provider)),
		slog.String("report_kind", kind),
	}
	if status != "" {
		attributes = append(attributes, slog.String("status", status))
	}
	if importID != "" {
		attributes = append(attributes, slog.String("import_id", importID))
	}
	if !periodStart.IsZero() {
		attributes = append(attributes, slog.String("period_start", periodStart.UTC().Format(time.DateOnly)))
	}
	if !periodEnd.IsZero() {
		attributes = append(attributes, slog.String("period_end", periodEnd.UTC().Format(time.DateOnly)))
	}
	if failureCode != "" {
		attributes = append(attributes, slog.String("failure_code", failureCode))
	}
	if row > 0 {
		attributes = append(attributes, slog.Int("row", row))
	}
	if column != "" {
		attributes = append(attributes, slog.String("column", column))
	}
	service.logger.LogAttrs(ctx, level, "financial report import", attributes...)
}

// zeroBytes overwrites a temporary decompressed report after parsing.
func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
