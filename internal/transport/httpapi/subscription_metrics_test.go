package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// fakeSubscriptionMetrics returns deterministic metrics that echo the requested environment and window.
func fakeSubscriptionMetrics(query persistence.SubscriptionMetricsQuery) persistence.SubscriptionMetrics {
	measuredAt := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.November, 19, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -query.WindowDays)
	daily := make([]persistence.SubscriptionDailyActive, 0, query.WindowDays)
	for day := 0; day < query.WindowDays; day++ {
		daily = append(daily, persistence.SubscriptionDailyActive{
			Date: start.AddDate(0, 0, day).Format(time.DateOnly), Active: int64(10 + day%3),
		})
	}
	return persistence.SubscriptionMetrics{
		Environment: query.Environment, WindowDays: query.WindowDays,
		Window:        persistence.SubscriptionWindow{Start: start, End: end, MeasuredAt: measuredAt},
		ActiveAtStart: 10, ActiveAtEnd: 12, InTrialAtEnd: 2, ActiveDuringWindow: 16, New: 5, Renewals: 7, PlanChanges: 2, Resubscriptions: 1,
		Churn: persistence.SubscriptionChurn{
			Churned: 4, Voluntary: 2, Billing: 1, Reversed: 1, Replaced: 1, Paused: 1,
		},
		Providers: []persistence.SubscriptionProviderMetrics{
			{
				Provider: core.ProviderAppleAppStore, ActiveDuringWindow: 10, WithOfferPhase: 9,
				TrialsStarted: 4, TrialsEnded: 3, TrialsConverted: 2, TrialsNotConverted: 1,
			},
			{Provider: core.ProviderHuaweiAppGallery, ActiveDuringWindow: 6},
		},
		Breakdown: []persistence.SubscriptionBreakdown{{
			ApplicationID: "application-1", Provider: core.ProviderAppleAppStore, ProductID: "premium-monthly",
			ActiveAtEnd: 12, New: 5, Renewals: 7, Churned: 4,
		}},
		Daily: daily,
	}
}

// TestAdminSubscriptionMetricsValidatesQueryAndScope verifies defaults, bounds, authentication, and project scope.
func TestAdminSubscriptionMetricsValidatesQueryAndScope(t *testing.T) {
	t.Parallel()

	authentication, err := auth.NewService(authOperationsStore{}, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	api := &API{admin: fakeAdminQueryStore{}, authentication: authentication}
	call := func(projectID, rawQuery string, authorized bool) *httptest.ResponseRecorder {
		target := "/v1/admin/projects/" + projectID + "/subscription-metrics"
		if rawQuery != "" {
			target += "?" + rawQuery
		}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.SetPathValue("project_id", projectID)
		if authorized {
			request.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
		}
		recorder := httptest.NewRecorder()
		api.adminSubscriptionMetrics(recorder, request)
		return recorder
	}

	if recorder := call("project-1", "", false); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	defaults := call("project-1", "", true)
	if defaults.Code != http.StatusOK {
		t.Fatalf("default status = %d, body = %s", defaults.Code, defaults.Body.String())
	}
	var response controlplane.SubscriptionMetrics
	if err := json.Unmarshal(defaults.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Environment != core.EnvironmentProduction || response.WindowDays != 30 || len(response.DailyActive) != 30 {
		t.Fatalf("default response = %#v, want production over 30 days", response)
	}
	chosen := call("project-1", "environment=sandbox&window_days=7", true)
	if err := json.Unmarshal(chosen.Body.Bytes(), &response); err != nil || chosen.Code != http.StatusOK {
		t.Fatalf("chosen window status = %d, error = %v", chosen.Code, err)
	}
	if response.Environment != core.EnvironmentSandbox || response.WindowDays != 7 {
		t.Fatalf("chosen response = %#v, want sandbox over 7 days", response)
	}
	for _, rawQuery := range []string{"window_days=0", "window_days=91", "window_days=week", "environment=staging"} {
		if recorder := call("project-1", rawQuery, true); recorder.Code != http.StatusBadRequest {
			t.Errorf("query %q status = %d, want %d", rawQuery, recorder.Code, http.StatusBadRequest)
		}
	}
	if recorder := call("project-unknown", "", true); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown project status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// TestSubscriptionMetricsResponseNeverShowsAbsentTrialsAsZero keeps unavailable providers null and the payload secret-free.
func TestSubscriptionMetricsResponseNeverShowsAbsentTrialsAsZero(t *testing.T) {
	t.Parallel()

	response := controlplane.SubscriptionMetricsView(fakeSubscriptionMetrics(persistence.SubscriptionMetricsQuery{
		Environment: core.EnvironmentProduction, WindowDays: 30,
	}))
	if response.Churn.Rate == nil || *response.Churn.Rate != 0.25 {
		t.Fatalf("churn rate = %v, want churned 4 / active during window 16", response.Churn.Rate)
	}
	apple, huawei := response.Trials[0], response.Trials[1]
	if apple.Availability != controlplane.TrialAvailabilityAvailable || apple.Started == nil || *apple.Started != 4 ||
		apple.ConversionRate == nil || *apple.ConversionRate != 2.0/3.0 {
		t.Fatalf("Apple trials = %#v", apple)
	}
	if huawei.Availability != controlplane.TrialAvailabilityNotAvailable || huawei.Started != nil || huawei.Ended != nil ||
		huawei.Converted != nil || huawei.NotConverted != nil || huawei.OutcomeNotRecorded != nil || huawei.ConversionRate != nil {
		t.Fatalf("Huawei trials = %#v, want not_available with null counts", huawei)
	}

	empty := controlplane.SubscriptionMetricsView(persistence.SubscriptionMetrics{Environment: core.EnvironmentTest, WindowDays: 1})
	if empty.Churn.Rate != nil || empty.Trials == nil || empty.Breakdown == nil || empty.DailyActive == nil {
		t.Fatalf("empty response = %#v, want a null rate and empty collections", empty)
	}

	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, forbidden := range []string{"customer", "lineage", "reference", "fingerprint", "token", "price", "revenue", "mrr"} {
		if strings.Contains(strings.ToLower(string(payload)), forbidden) {
			t.Fatalf("subscription metrics JSON contains %q: %s", forbidden, payload)
		}
	}
}
