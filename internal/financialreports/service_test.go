package financialreports

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

// memoryFinancialStore records drafts without a database.
type memoryFinancialStore struct {
	drafts []persistence.FinancialReportDraft
	saves  int
}

// SaveFinancialReport stores the draft in memory and returns a body-free report.
func (store *memoryFinancialStore) SaveFinancialReport(
	_ context.Context,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	store.saves++
	store.drafts = append(store.drafts, draft)
	return reportFromDraft(draft, "import-1"), true, nil
}

// SupersedeFinancialReport records a replacement only when the draft is accepted.
func (store *memoryFinancialStore) SupersedeFinancialReport(
	_ context.Context,
	_ core.ProjectID,
	importID string,
	draft persistence.FinancialReportDraft,
) (persistence.FinancialReport, bool, error) {
	if draft.Status != persistence.FinancialReportStatusAccepted {
		return persistence.FinancialReport{}, false, persistence.ErrFinancialReportNotAccepted
	}
	store.saves++
	store.drafts = append(store.drafts, draft)
	report := reportFromDraft(draft, "import-2")
	report.SupersedesImportID = &importID
	return report, true, nil
}

// ListFinancialReports returns no rows from the memory double.
func (store *memoryFinancialStore) ListFinancialReports(context.Context, core.ProjectID) ([]persistence.FinancialReport, error) {
	return []persistence.FinancialReport{}, nil
}

// TestUploadRecognizesAppleTotalsAndLeavesOtherProvidersUnrecognized verifies parser boundaries.
func TestUploadRecognizesAppleTotalsAndLeavesOtherProvidersUnrecognized(t *testing.T) {
	t.Parallel()

	store := &memoryFinancialStore{}
	logs := &bytes.Buffer{}
	service := newFinancialService(t, store, logs)
	report, created, err := service.Upload(context.Background(), financialreportsUpload(core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(appleConsolidatedFixture), nil, nil))
	if err != nil || !created || report.Status != persistence.FinancialReportStatusAccepted {
		t.Fatalf("Upload() = (%s, %t, %v), want accepted", report.Status, created, err)
	}
	if len(store.drafts) != 1 || len(store.drafts[0].IdentifierTotals) != 4 {
		t.Fatalf("draft totals = %#v, want four identifier totals before matching", store.drafts)
	}
	for _, total := range store.drafts[0].IdentifierTotals {
		if total.ProviderApplicationID == "1000000001" && total.Currency == "USD" && total.Amount.String() != "2.50" {
			t.Fatalf("USD recognized amount = %s, want extended partner share 2.50", total.Amount.String())
		}
	}
	periodStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	googleBody := []byte("Transaction Type,Description\nCharge,GPA.SENTINEL-ORDER\n")
	google, _, err := service.Upload(context.Background(), financialreportsUpload(core.ProviderGooglePlay, persistence.FinancialReportKindGoogleEarnings, googleBody, &periodStart, &periodEnd))
	if err != nil || google.Status != persistence.FinancialReportStatusUnrecognized || google.Reason != persistence.FinancialReportReasonFixtureRequired {
		t.Fatalf("google Upload() = (%s, %s, %v)", google.Status, google.Reason, err)
	}
	huaweiBody := []byte(`{"totalAmount":"10.00","orderId":"HUAWEI-ORDER-SENTINEL"}`)
	huawei, _, err := service.Upload(context.Background(), financialreportsUpload(core.ProviderHuaweiAppGallery, persistence.FinancialReportKindHuaweiSettlement, huaweiBody, &periodStart, &periodEnd))
	if err != nil || huawei.Status != persistence.FinancialReportStatusUnrecognized || huawei.Reason != persistence.FinancialReportReasonFixtureRequired {
		t.Fatalf("huawei Upload() = (%s, %s, %v)", huawei.Status, huawei.Reason, err)
	}
	logged := logs.String()
	for _, sentinel := range []string{"GPA.SENTINEL-ORDER", "HUAWEI-ORDER-SENTINEL", "2.99"} {
		if strings.Contains(logged, sentinel) {
			t.Fatalf("log contains report cell %q: %s", sentinel, logged)
		}
	}
}

// TestSupersedeRefusesUnrecognizedReplacement verifies Google and Huawei files cannot become accepted.
func TestSupersedeRefusesUnrecognizedReplacement(t *testing.T) {
	t.Parallel()

	store := &memoryFinancialStore{}
	service := newFinancialService(t, store, &bytes.Buffer{})
	periodStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	_, _, err := service.Supersede(context.Background(), "project-1", "import-old", financialreportsUpload(
		core.ProviderGooglePlay, persistence.FinancialReportKindGoogleEarnings, []byte("a,b\n1,2\n"), &periodStart, &periodEnd,
	))
	if !errors.Is(err, persistence.ErrFinancialReportNotAccepted) || !errors.Is(err, validation.ErrInvalid) || store.saves != 0 {
		t.Fatalf("Supersede() = %v saves %d, want not accepted and no save", err, store.saves)
	}
}

// TestFailedAppleRowLogOmitsIdentifierCell verifies parse failures do not echo the Apple Identifier.
func TestFailedAppleRowLogOmitsIdentifierCell(t *testing.T) {
	t.Parallel()

	store := &memoryFinancialStore{}
	logs := &bytes.Buffer{}
	service := newFinancialService(t, store, logs)
	payload := appleFinancialHeader + "09/28/2026\t11/01/2026\t" + appleNegativeSaleSentinel + "\t1\t-1.25\t-1.25\tUSD\tS\t1.49\tUSD\n"
	_, _, err := service.Upload(context.Background(), financialreportsUpload(core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, []byte(payload), nil, nil))
	if err == nil || store.saves != 0 || strings.Contains(logs.String(), appleNegativeSaleSentinel) || strings.Contains(err.Error(), appleNegativeSaleSentinel) {
		t.Fatalf("Upload() error = %v log = %s saves %d", err, logs.String(), store.saves)
	}
}

// TestPullRefusesUnpinnedProviders verifies no provider call and no guessed query is produced.
func TestPullRefusesUnpinnedProviders(t *testing.T) {
	t.Parallel()

	store := &memoryFinancialStore{}
	logs := &bytes.Buffer{}
	service := newFinancialService(t, store, logs)
	for _, provider := range []core.Provider{core.ProviderAppleAppStore, core.ProviderGooglePlay, core.ProviderHuaweiAppGallery} {
		kind, _ := ReportKind(provider)
		err := service.Pull(context.Background(), PullRequest{
			ProjectID: "project-1", Provider: provider, ReportKind: kind,
			PeriodStart: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
			PeriodEnd:   time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		})
		if !errors.Is(err, ErrPullUnpinned) || !errors.Is(err, validation.ErrInvalid) {
			t.Fatalf("Pull(%s) error = %v", provider, err)
		}
		for _, forbidden := range []string{"://", "regionCode", "vendorNumber", "filter[", "reportType", "earnings_", "reconciliationUrl"} {
			if strings.Contains(err.Error(), forbidden) || strings.Contains(logs.String(), forbidden) {
				t.Fatalf("pull exposed %q in %v or %s", forbidden, err, logs.String())
			}
		}
	}
	if store.saves != 0 {
		t.Fatalf("pull saved %d imports", store.saves)
	}
}

// TestGzipUploadHashesReceivedBytes verifies the digest is the uploaded gzip, not the text inside it.
func TestGzipUploadHashesReceivedBytes(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(appleConsolidatedFixture)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	store := &memoryFinancialStore{}
	service := newFinancialService(t, store, &bytes.Buffer{})
	_, _, err := service.Upload(context.Background(), financialreportsUpload(
		core.ProviderAppleAppStore, persistence.FinancialReportKindAppleConsolidated, compressed.Bytes(), nil, nil,
	))
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	sum := sha256.Sum256(compressed.Bytes())
	if store.drafts[0].SHA256 != sum || store.drafts[0].ByteSize != int64(compressed.Len()) {
		t.Fatalf("digest = %x size %d, want received gzip", store.drafts[0].SHA256, store.drafts[0].ByteSize)
	}
	if len(store.drafts[0].IdentifierTotals) != 4 {
		t.Fatalf("gzip totals = %d, want 4", len(store.drafts[0].IdentifierTotals))
	}
}

// TestFinancialReportSourcesDoNotReadPurchasePrices verifies recognized amounts have no price shortcut.
func TestFinancialReportSourcesDoNotReadPurchasePrices(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, forbidden := range []string{"price_milliunits", "regionCode", "vendorNumber", "filter[", "earnings_", "reconciliationUrl", "settlementFileUrl"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s contains %q", entry.Name(), forbidden)
			}
		}
	}
	body, err := os.ReadFile("../persistence/postgres/financial_report.go")
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if strings.Contains(string(body), "price_milliunits") {
		t.Fatal("financial report store reads purchase prices")
	}
}

// TestSafeLogTextNeutralizesLogForging verifies request-derived values cannot create log entries.
func TestSafeLogTextNeutralizesLogForging(t *testing.T) {
	t.Parallel()

	forged := "customer\r\nlevel=error msg=forged"
	if got := safeLogText(forged); got != "customer__level_error_msg_forged" || strings.ContainsAny(got, "\r\n") {
		t.Fatalf("safeLogText() = %q", got)
	}
	if got := safeLogText(strings.Repeat("a", 200)); len(got) != 128 {
		t.Fatalf("safeLogText() length = %d, want 128", len(got))
	}
}

// newFinancialService builds a service with a captured logger.
func newFinancialService(t *testing.T, store persistence.FinancialReportStore, logs *bytes.Buffer) *Service {
	t.Helper()
	service, err := NewService(store, slog.New(slog.NewJSONHandler(logs, nil)), DefaultByteLimit, DefaultRowLimit)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// financialreportsUpload builds one upload. The name avoids clashing with the Upload type in composite literals.
func financialreportsUpload(provider core.Provider, kind string, body []byte, start, end *time.Time) Upload {
	return Upload{
		ProjectID: "project-1", Provider: provider, ReportKind: kind, ContentType: "text/plain",
		PeriodStart: start, PeriodEnd: end, Body: body,
	}
}

// reportFromDraft copies safe metadata and drops identifier cells from the returned report.
func reportFromDraft(draft persistence.FinancialReportDraft, id string) persistence.FinancialReport {
	return persistence.FinancialReport{
		ID: id, ProjectID: draft.ProjectID, Provider: draft.Provider, ReportKind: draft.ReportKind,
		PeriodStart: draft.PeriodStart, PeriodEnd: draft.PeriodEnd, Source: draft.Source,
		SHA256: draft.SHA256, ByteSize: draft.ByteSize, ContentType: draft.ContentType,
		ParserVersion: draft.ParserVersion, Status: draft.Status, Reason: draft.Reason,
		Amounts: []persistence.FinancialReportAmount{},
	}
}
