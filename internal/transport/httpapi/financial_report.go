package httpapi

import (
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/financialreports"
	"github.com/imariman/iapstack/internal/persistence"
)

// financialReportListResponse is the secret-free import collection.
type financialReportListResponse struct {
	FinancialReports []financialReportResponse `json:"financial_reports"`
}

// financialReportResponse is one stored import without report bytes or order ids.
type financialReportResponse struct {
	ID                    string                          `json:"id"`
	Provider              core.Provider                   `json:"provider"`
	ReportKind            string                          `json:"report_kind"`
	PeriodStart           string                          `json:"period_start"`
	PeriodEnd             string                          `json:"period_end"`
	Source                string                          `json:"source"`
	SHA256                string                          `json:"sha256"`
	ByteSize              int64                           `json:"byte_size"`
	ContentType           string                          `json:"content_type"`
	ParserVersion         string                          `json:"parser_version"`
	Status                string                          `json:"status"`
	Reason                string                          `json:"reason"`
	SupersedesImportID    *string                         `json:"supersedes_import_id,omitempty"`
	Currencies            []string                        `json:"currencies"`
	Amounts               []financialReportAmountResponse `json:"amounts"`
	CoveredApplicationIDs []core.ApplicationID            `json:"covered_application_ids"`
	CreatedAt             time.Time                       `json:"created_at"`
}

// financialReportAmountResponse is one accepted application and currency total.
type financialReportAmountResponse struct {
	Currency             string             `json:"currency"`
	AmountDecimal        string             `json:"amount_decimal"`
	AmountScale          int32              `json:"amount_scale"`
	MinorUnits           *int64             `json:"minor_units"`
	CommissionState      string             `json:"commission_state"`
	TaxState             string             `json:"tax_state"`
	CoveredApplicationID core.ApplicationID `json:"covered_application_id"`
}

// financialReportPullRequest selects one provider report period and no guessed query parameters.
type financialReportPullRequest struct {
	Provider    core.Provider `json:"provider"`
	ReportKind  string        `json:"report_kind"`
	PeriodStart string        `json:"period_start"`
	PeriodEnd   string        `json:"period_end"`
}

// listFinancialReports returns import metadata for one project.
func (api *API) listFinancialReports(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	if !api.requireValidInput(writer, request, projectID.Validate()) {
		return
	}
	reports, err := api.financialReports.List(request.Context(), projectID)
	if err != nil {
		api.writeFinancialError(writer, request, err)
		return
	}
	response := financialReportListResponse{FinancialReports: make([]financialReportResponse, 0, len(reports))}
	for _, report := range reports {
		response.FinancialReports = append(response.FinancialReports, financialReportResponseFrom(report))
	}
	writeJSON(writer, http.StatusOK, response)
}

// uploadFinancialReport stores one operator-supplied report file.
func (api *API) uploadFinancialReport(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	if !api.requireValidInput(writer, request, projectID.Validate()) {
		return
	}
	upload, ok := api.readFinancialUpload(writer, request, projectID)
	if !ok {
		return
	}
	defer zero(upload.Body)
	report, created, err := api.financialReports.Upload(request.Context(), upload)
	if err != nil {
		api.writeFinancialError(writer, request, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, financialReportResponseFrom(report))
}

// supersedeFinancialReport replaces one accepted period with a newer accepted file.
func (api *API) supersedeFinancialReport(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	importID, ok := strings.CutSuffix(request.PathValue("import_id"), ":supersede")
	if !ok || importID == "" || strings.Contains(importID, ":") {
		api.notFound(writer, request)
		return
	}
	if !api.requireValidInput(writer, request, projectID.Validate()) {
		return
	}
	upload, ok := api.readFinancialUpload(writer, request, projectID)
	if !ok {
		return
	}
	defer zero(upload.Body)
	report, _, err := api.financialReports.Supersede(request.Context(), projectID, importID, upload)
	if err != nil {
		api.writeFinancialError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, financialReportResponseFrom(report))
}

// pullFinancialReport validates one pull and refuses providers whose official contract is not pinned.
func (api *API) pullFinancialReport(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	if !api.requireValidInput(writer, request, projectID.Validate()) {
		return
	}
	var input financialReportPullRequest
	if !api.decode(writer, request, &input) {
		return
	}
	periodStart, err := time.Parse(time.DateOnly, input.PeriodStart)
	if err != nil {
		api.requireValidInput(writer, request, err)
		return
	}
	periodEnd, err := time.Parse(time.DateOnly, input.PeriodEnd)
	if err != nil {
		api.requireValidInput(writer, request, err)
		return
	}
	err = api.financialReports.Pull(request.Context(), financialreports.PullRequest{
		ProjectID: projectID, Provider: input.Provider, ReportKind: input.ReportKind,
		PeriodStart: time.Date(periodStart.Year(), periodStart.Month(), periodStart.Day(), 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(periodEnd.Year(), periodEnd.Month(), periodEnd.Day(), 0, 0, 0, 0, time.UTC),
	})
	api.writeFinancialError(writer, request, err)
}

// readFinancialUpload reads one multipart file without accepting client prices as proceeds.
func (api *API) readFinancialUpload(
	writer http.ResponseWriter,
	request *http.Request,
	projectID core.ProjectID,
) (financialreports.Upload, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		writeAPIError(writer, request, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be multipart/form-data")
		return financialreports.Upload{}, false
	}
	limit := api.financialReports.ByteLimit()
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	reader, err := request.MultipartReader()
	if err != nil {
		api.writeUploadReadError(writer, request, err)
		return financialreports.Upload{}, false
	}
	fields := map[string]string{}
	var body []byte
	var contentType string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			zero(body)
			api.writeUploadReadError(writer, request, err)
			return financialreports.Upload{}, false
		}
		payload, err := readFinancialPart(part, limit)
		_ = part.Close()
		if err != nil {
			zero(body)
			zero(payload)
			api.writeUploadReadError(writer, request, err)
			return financialreports.Upload{}, false
		}
		switch part.FormName() {
		case "file":
			if body != nil {
				zero(body)
				zero(payload)
				writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "request values are invalid")
				return financialreports.Upload{}, false
			}
			body = payload
			contentType = part.Header.Get("Content-Type")
		case "provider", "report_kind", "period_start", "period_end":
			if _, exists := fields[part.FormName()]; exists {
				zero(body)
				zero(payload)
				writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "request values are invalid")
				return financialreports.Upload{}, false
			}
			fields[part.FormName()] = string(payload)
			zero(payload)
		default:
			zero(body)
			zero(payload)
			writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "request values are invalid")
			return financialreports.Upload{}, false
		}
	}
	periodStart, err := optionalFinancialDate(fields["period_start"])
	if err != nil {
		zero(body)
		writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "request values are invalid")
		return financialreports.Upload{}, false
	}
	periodEnd, err := optionalFinancialDate(fields["period_end"])
	if err != nil {
		zero(body)
		writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "request values are invalid")
		return financialreports.Upload{}, false
	}
	return financialreports.Upload{
		ProjectID: projectID, Provider: core.Provider(fields["provider"]), ReportKind: fields["report_kind"],
		ContentType: contentType, PeriodStart: periodStart, PeriodEnd: periodEnd, Body: body,
	}, true
}

// readFinancialPart reads one multipart section with the same byte ceiling as the report.
func readFinancialPart(part *multipart.Part, limit int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return payload, errors.New("financial report part exceeds the byte limit")
	}
	return payload, nil
}

// optionalFinancialDate parses a YYYY-MM-DD form value.
func optionalFinancialDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil, err
	}
	date := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
	return &date, nil
}

// writeUploadReadError maps an oversized upload to the stable too-large response.
func (api *API) writeUploadReadError(writer http.ResponseWriter, request *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "exceeds the byte limit") {
		writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured limit")
		return
	}
	writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "request values are invalid")
}

// writeFinancialError maps import failures onto the stable error envelope.
func (api *API) writeFinancialError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, financialreports.ErrReportTooLarge) {
		writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured limit")
		return
	}
	api.writeError(writer, request, err)
}

// financialReportResponseFrom maps stored metadata and omits amounts that were not accepted.
func financialReportResponseFrom(report persistence.FinancialReport) financialReportResponse {
	amounts := report.Amounts
	if report.Status != persistence.FinancialReportStatusAccepted {
		amounts = nil
	}
	response := financialReportResponse{
		ID: report.ID, Provider: report.Provider, ReportKind: report.ReportKind,
		PeriodStart: report.PeriodStart.UTC().Format(time.DateOnly),
		PeriodEnd:   report.PeriodEnd.UTC().Format(time.DateOnly),
		Source:      report.Source, SHA256: hex.EncodeToString(report.SHA256[:]),
		ByteSize: report.ByteSize, ContentType: report.ContentType, ParserVersion: report.ParserVersion,
		Status: report.Status, Reason: report.Reason, SupersedesImportID: report.SupersedesImportID,
		Currencies: []string{}, Amounts: make([]financialReportAmountResponse, 0, len(amounts)),
		CoveredApplicationIDs: []core.ApplicationID{}, CreatedAt: report.CreatedAt,
	}
	currencies := map[string]struct{}{}
	covered := map[core.ApplicationID]struct{}{}
	for _, amount := range amounts {
		response.Amounts = append(response.Amounts, financialReportAmountResponse{
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
