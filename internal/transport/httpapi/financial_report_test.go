package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/financialreports"
	"github.com/imariman/iapstack/internal/persistence"
)

// recordingFinancialStore counts saves and returns body-free metadata.
type recordingFinancialStore struct {
	saves int
}

// SaveFinancialReport records one draft without echoing its identifier cells.
func (store *recordingFinancialStore) SaveFinancialReport(
	_ context.Context,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	store.saves++
	return persistence.FinancialReport{
		ID: "import-1", ProjectID: draft.ProjectID, Provider: draft.Provider, ReportKind: draft.ReportKind,
		PeriodStart: draft.PeriodStart, PeriodEnd: draft.PeriodEnd, Source: draft.Source, SHA256: draft.SHA256,
		ByteSize: draft.ByteSize, ContentType: draft.ContentType, ParserVersion: draft.ParserVersion,
		Status: draft.Status, Reason: draft.Reason, CreatedAt: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
		Amounts: []persistence.FinancialReportAmount{},
	}, true, nil
}

// SupersedeFinancialReport refuses non-accepted replacements.
func (store *recordingFinancialStore) SupersedeFinancialReport(
	context.Context, core.ProjectID, string, persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	return persistence.FinancialReport{}, false, persistence.ErrFinancialReportNotAccepted
}

// ListFinancialReports returns an empty collection.
func (store *recordingFinancialStore) ListFinancialReports(context.Context, core.ProjectID) ([]persistence.FinancialReport, error) {
	return []persistence.FinancialReport{}, nil
}

// TestFinancialReportHTTPRefusesPricesPullsAndBadFiles verifies the admin routes stay secret-free.
func TestFinancialReportHTTPRefusesPricesPullsAndBadFiles(t *testing.T) {
	t.Parallel()

	store := &recordingFinancialStore{}
	service, err := financialreports.NewService(store, slog.New(slog.DiscardHandler), 1<<20, 100)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	authentication, err := auth.NewService(authOperationsStore{}, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() authentication error = %v", err)
	}
	api := &API{authentication: authentication, financialReports: service, bodyLimit: 1 << 20}

	pull := httptest.NewRequest(http.MethodPost, "/v1/admin/projects/project-1/financial-reports:pull", strings.NewReader(
		`{"provider":"apple_app_store","report_kind":"apple_financial_consolidated","period_start":"2026-09-28","period_end":"2026-11-01","price_milliunits":4990}`,
	))
	pull.SetPathValue("project_id", "project-1")
	pull.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	pull.Header.Set("Content-Type", "application/json")
	pullRecorder := httptest.NewRecorder()
	api.pullFinancialReport(pullRecorder, pull)
	if pullRecorder.Code != http.StatusBadRequest || strings.Contains(pullRecorder.Body.String(), "regionCode") || strings.Contains(pullRecorder.Body.String(), "://") {
		t.Fatalf("priced pull = %d %s", pullRecorder.Code, pullRecorder.Body.String())
	}

	validPull := httptest.NewRequest(http.MethodPost, "/v1/admin/projects/project-1/financial-reports:pull", strings.NewReader(
		`{"provider":"google_play","report_kind":"google_play_earnings","period_start":"2026-10-01","period_end":"2026-10-31"}`,
	))
	validPull.SetPathValue("project_id", "project-1")
	validPull.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	validPull.Header.Set("Content-Type", "application/json")
	validPullRecorder := httptest.NewRecorder()
	api.pullFinancialReport(validPullRecorder, validPull)
	if validPullRecorder.Code != http.StatusBadRequest || store.saves != 0 {
		t.Fatalf("pull = %d saves %d %s", validPullRecorder.Code, store.saves, validPullRecorder.Body.String())
	}

	sentinel := "SENTINEL-APPLE-ID-9f3c2a"
	bad := "Start Date\tEnd Date\tApple Identifier\tQuantity\tPartner Share\tExtended Partner Share\tPartner Share Currency\tSale or Return\tCustomer Price\tCustomer Currency\n" +
		"09/28/2026\t11/01/2026\t" + sentinel + "\t1\t-1.25\t-1.25\tUSD\tS\t1.49\tUSD\n"
	badRecorder := httptest.NewRecorder()
	api.uploadFinancialReport(badRecorder, financialMultipart(t, map[string]string{
		"provider": "apple_app_store", "report_kind": "apple_financial_consolidated",
	}, bad))
	if badRecorder.Code != http.StatusBadRequest || strings.Contains(badRecorder.Body.String(), sentinel) || store.saves != 0 {
		t.Fatalf("bad upload = %d saves %d %s", badRecorder.Code, store.saves, badRecorder.Body.String())
	}

	priceRecorder := httptest.NewRecorder()
	api.uploadFinancialReport(priceRecorder, financialMultipart(t, map[string]string{
		"provider": "google_play", "report_kind": "google_play_earnings",
		"period_start": "2026-10-01", "period_end": "2026-10-31", "price_milliunits": "4990",
	}, "Transaction Type,Description\nCharge,GPA.SENTINEL-ORDER\n"))
	if priceRecorder.Code != http.StatusBadRequest || store.saves != 0 {
		t.Fatalf("priced upload = %d saves %d", priceRecorder.Code, store.saves)
	}

	googleRecorder := httptest.NewRecorder()
	api.uploadFinancialReport(googleRecorder, financialMultipart(t, map[string]string{
		"provider": "google_play", "report_kind": "google_play_earnings",
		"period_start": "2026-10-01", "period_end": "2026-10-31",
	}, "Transaction Type,Description\nCharge,GPA.SENTINEL-ORDER\n"))
	if googleRecorder.Code != http.StatusCreated || strings.Contains(googleRecorder.Body.String(), "GPA.SENTINEL-ORDER") ||
		!strings.Contains(googleRecorder.Body.String(), `"status":"unrecognized"`) ||
		!strings.Contains(googleRecorder.Body.String(), `"reason":"fixture_required"`) {
		t.Fatalf("google upload = %d %s", googleRecorder.Code, googleRecorder.Body.String())
	}
}

// TestFinancialReportSupersedeRouteMatchesColonSegment verifies the public custom method reaches the handler.
func TestFinancialReportSupersedeRouteMatchesColonSegment(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+serveMuxPath("/v1/admin/projects/{project_id}/financial-reports/{import_id}:supersede"), func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(request.PathValue("import_id")))
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/projects/project-1/financial-reports/abc123:supersede", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "abc123:supersede" {
		t.Fatalf("route = %d %q", recorder.Code, recorder.Body.String())
	}
}

// financialMultipart builds an authenticated upload request.
func financialMultipart(t *testing.T, fields map[string]string, file string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	part, err := writer.CreateFormFile("file", "report.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(file)); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/projects/project-1/financial-reports", &body)
	request.SetPathValue("project_id", "project-1")
	request.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
