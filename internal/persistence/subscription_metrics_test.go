package persistence_test

import (
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestComputeSubscriptionMetricsFollowsStatedDefinitions pins every metric to one scripted lineage set.
func TestComputeSubscriptionMetricsFollowsStatedDefinitions(t *testing.T) {
	t.Parallel()

	day := func(month time.Month, value int) time.Time {
		return time.Date(2026, month, value, 0, 0, 0, 0, time.UTC)
	}
	window := persistence.SubscriptionWindow{
		Start: day(time.October, 20), End: day(time.November, 19),
		MeasuredAt: day(time.November, 18).Add(12 * time.Hour),
	}
	query := persistence.SubscriptionMetricsQuery{Environment: core.EnvironmentProduction, WindowDays: 30}
	interval := func(product core.ProductID, start, end time.Time, phase core.OfferPhase) persistence.SubscriptionInterval {
		return persistence.SubscriptionInterval{ProductID: product, StartsAt: start, EndsAt: end, OfferPhase: phase}
	}
	expired := persistence.SubscriptionEvidence{
		State: core.LifecycleExpired, Access: core.AccessDenied, Reason: core.AccessReasonExpired,
		RenewalStatus: core.RenewalDisabled,
	}
	active := persistence.SubscriptionEvidence{
		State: core.LifecycleActive, Access: core.AccessAllowed, Reason: core.AccessReasonPurchaseValid,
		RenewalStatus: core.RenewalEnabled,
	}
	canceled := &persistence.SubscriptionEvidence{
		State: core.LifecycleCanceled, Access: core.AccessAllowed, Reason: core.AccessReasonCanceledAtPeriodEnd,
		RenewalStatus: core.RenewalDisabled,
	}
	onHold := &persistence.SubscriptionEvidence{
		State: core.LifecycleOnHold, Access: core.AccessDenied, Reason: core.AccessReasonBillingIssue,
		RenewalStatus: core.RenewalEnabled,
	}
	lineage := func(key string, provider core.Provider, customer core.CustomerID, intervals ...persistence.SubscriptionInterval) persistence.SubscriptionLineage {
		first := intervals[0].StartsAt
		for _, candidate := range intervals {
			if candidate.StartsAt.Before(first) {
				first = candidate.StartsAt
			}
		}
		return persistence.SubscriptionLineage{
			Key: key, ApplicationID: core.ApplicationID("app-" + string(provider)), Provider: provider,
			CustomerID: customer, FirstStartsAt: first, Intervals: intervals, Latest: active,
			LastLive: &active, PhaseRecorded: provider != core.ProviderHuaweiAppGallery,
		}
	}
	apple, google, huawei := core.ProviderAppleAppStore, core.ProviderGooglePlay, core.ProviderHuaweiAppGallery

	planChange := lineage("plan-change", apple, "customer-a",
		interval("monthly", day(time.September, 1), day(time.November, 10), core.OfferPhaseBase),
		interval("annual", day(time.November, 10), day(time.December, 10), core.OfferPhaseBase))
	converted := lineage("trial-converted", apple, "customer-b",
		interval("monthly", day(time.November, 1), day(time.November, 8), core.OfferPhaseFreeTrial),
		interval("monthly", day(time.November, 8), day(time.December, 8), core.OfferPhaseBase))
	converted.Renewals = []core.ProductID{"monthly"}
	converted.Trial = &persistence.SubscriptionTrialEvidence{StartsAt: day(time.November, 1), EndsAt: day(time.November, 8), Converted: true}
	voluntary := lineage("voluntary", apple, "customer-c", interval("monthly", day(time.October, 1), day(time.November, 1), core.OfferPhaseBase))
	voluntary.Latest, voluntary.LastLive = expired, canceled
	billing := lineage("billing", apple, "customer-d", interval("monthly", day(time.October, 5), day(time.November, 5), core.OfferPhaseBase))
	billing.Latest, billing.LastLive = expired, onHold
	reversed := lineage("reversed", apple, "customer-e", interval("monthly", day(time.October, 25), day(time.November, 10), core.OfferPhaseBase))
	reversed.Latest = persistence.SubscriptionEvidence{State: core.LifecycleRefunded, Access: core.AccessDenied, Reason: core.AccessReasonRefunded}
	replacedOld := lineage("google-old", google, "customer-f", interval("basic", day(time.September, 15), day(time.November, 12), core.OfferPhaseBase))
	replacedOld.Latest = expired
	replacedNew := lineage("google-new", google, "customer-f", interval("premium", day(time.November, 12), day(time.December, 12), core.OfferPhaseBase))
	replacedNew.Linked = true
	paused := lineage("paused", google, "customer-g", interval("basic", day(time.October, 1), day(time.November, 2), core.OfferPhaseBase))
	paused.Latest = persistence.SubscriptionEvidence{State: core.LifecyclePaused, Access: core.AccessDenied, Reason: core.AccessReasonPaused}
	unclassified := lineage("unclassified", google, "customer-h", interval("basic", day(time.October, 1), day(time.November, 3), core.OfferPhaseBase))
	unclassified.Latest = expired
	inTrial := lineage("in-trial", apple, "customer-i", interval("monthly", day(time.November, 15), day(time.November, 22), core.OfferPhaseFreeTrial))
	inTrial.Trial = &persistence.SubscriptionTrialEvidence{StartsAt: day(time.November, 15), EndsAt: day(time.November, 22)}
	lapsedTrial := lineage("lapsed-trial", apple, "customer-j", interval("monthly", day(time.October, 28), day(time.November, 4), core.OfferPhaseFreeTrial))
	lapsedTrial.Latest, lapsedTrial.LastLive = expired, canceled
	lapsedTrial.Trial = &persistence.SubscriptionTrialEvidence{StartsAt: day(time.October, 28), EndsAt: day(time.November, 4)}
	huaweiActive := lineage("huawei", huawei, "customer-k", interval("huawei-monthly", day(time.October, 1), day(time.December, 1), core.OfferPhaseNotRecorded))
	unrecordedAfterTrial := lineage("unrecorded-after-trial", apple, "customer-m",
		interval("monthly", day(time.October, 30), day(time.November, 6), core.OfferPhaseFreeTrial),
		interval("monthly", day(time.November, 6), day(time.December, 6), core.OfferPhaseNotRecorded))
	unrecordedAfterTrial.Renewals = []core.ProductID{"monthly"}
	unrecordedAfterTrial.Trial = &persistence.SubscriptionTrialEvidence{StartsAt: day(time.October, 30), EndsAt: day(time.November, 6), UnknownAfter: true}

	metrics := persistence.ComputeSubscriptionMetrics(query, window,
		[]core.Provider{apple, google, huawei},
		[]persistence.SubscriptionLineage{
			planChange, converted, voluntary, billing, reversed, replacedOld, replacedNew,
			paused, unclassified, inTrial, lapsedTrial, huaweiActive, unrecordedAfterTrial,
		})

	for name, got := range map[string][2]int64{
		"active at start":      {metrics.ActiveAtStart, 7},
		"active at end":        {metrics.ActiveAtEnd, 6},
		"in trial at end":      {metrics.InTrialAtEnd, 1},
		"active during window": {metrics.ActiveDuringWindow, 13},
		"new":                  {metrics.New, 5},
		"renewals":             {metrics.Renewals, 2},
		"churned":              {metrics.Churn.Churned, 5},
		"voluntary":            {metrics.Churn.Voluntary, 2},
		"billing":              {metrics.Churn.Billing, 1},
		"reversed":             {metrics.Churn.Reversed, 1},
		"unclassified":         {metrics.Churn.Unclassified, 1},
		"replaced":             {metrics.Churn.Replaced, 1},
		"paused":               {metrics.Churn.Paused, 1},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %d, want %d", name, got[0], got[1])
		}
	}

	if len(metrics.Providers) != 3 {
		t.Fatalf("providers = %#v, want Apple, Google Play, and Huawei", metrics.Providers)
	}
	appleTrials := metrics.Providers[0]
	if appleTrials.Provider != apple || appleTrials.TrialsStarted != 4 || appleTrials.TrialsEnded != 3 ||
		appleTrials.TrialsConverted != 1 || appleTrials.TrialsNotConverted != 1 || appleTrials.TrialsOutcomeNotRecorded != 1 ||
		appleTrials.ActiveDuringWindow != 8 || appleTrials.WithOfferPhase != 8 {
		t.Fatalf("Apple provider metrics = %#v", appleTrials)
	}
	if huaweiRow := metrics.Providers[2]; huaweiRow.Provider != huawei || huaweiRow.ActiveDuringWindow != 1 || huaweiRow.WithOfferPhase != 0 {
		t.Fatalf("Huawei provider metrics = %#v", huaweiRow)
	}

	if len(metrics.Daily) != 30 || metrics.Daily[0].Date != "2026-10-20" || metrics.Daily[29].Date != "2026-11-18" {
		t.Fatalf("daily series = %#v, want 30 UTC days from 2026-10-20", metrics.Daily)
	}
	if metrics.Daily[0].Active != 7 || metrics.Daily[29].Active != metrics.ActiveAtEnd {
		t.Fatalf("daily active first/last = %d/%d, want 7 and the window-end count", metrics.Daily[0].Active, metrics.Daily[29].Active)
	}

	rows := make(map[string]persistence.SubscriptionBreakdown)
	for _, row := range metrics.Breakdown {
		rows[string(row.ApplicationID)+"/"+string(row.ProductID)] = row
	}
	if row := rows["app-apple_app_store/annual"]; row.ActiveAtEnd != 1 || row.New != 0 {
		t.Fatalf("annual row = %#v, want the plan change counted on the product active at the end", row)
	}
	if row := rows["app-apple_app_store/monthly"]; row.ActiveAtEnd != 3 || row.New != 5 || row.Renewals != 2 || row.Churned != 4 {
		t.Fatalf("Apple monthly row = %#v", row)
	}
	if row := rows["app-google_play/premium"]; row.ActiveAtEnd != 1 || row.New != 0 {
		t.Fatalf("linked Google Play row = %#v, want an active replacement that is not new", row)
	}
	if row := rows["app-google_play/basic"]; row.Churned != 1 {
		t.Fatalf("Google Play basic row = %#v, want only the unclassified churn", row)
	}
	if metrics.BreakdownTruncated {
		t.Fatal("breakdown truncated = true, want false")
	}
}

// TestComputeSubscriptionMetricsUsesSignedLineageEvidence pins the rules that apply only when the fields were recorded.
func TestComputeSubscriptionMetricsUsesSignedLineageEvidence(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC)
	measuredAt := start.Add(29*24*time.Hour + 12*time.Hour)
	window := persistence.SubscriptionWindow{Start: start, End: start.Add(30 * 24 * time.Hour), MeasuredAt: measuredAt}
	day := func(offset int) time.Time { return start.Add(time.Duration(offset) * 24 * time.Hour) }
	expired := persistence.SubscriptionEvidence{State: core.LifecycleExpired, Access: core.AccessDenied, Reason: core.AccessReasonExpired}
	active := persistence.SubscriptionEvidence{
		State: core.LifecycleActive, Access: core.AccessAllowed, Reason: core.AccessReasonPurchaseValid, RenewalStatus: core.RenewalEnabled,
	}
	lineage := func(key string, customer core.CustomerID, from, to time.Time) persistence.SubscriptionLineage {
		return persistence.SubscriptionLineage{
			Key: key, ApplicationID: "app-1", Provider: core.ProviderGooglePlay, CustomerID: customer, FirstStartsAt: from,
			Intervals: []persistence.SubscriptionInterval{{ProductID: "monthly", StartsAt: from, EndsAt: to}},
			Latest:    active, LastLive: &active,
		}
	}
	originalStart := day(-200)
	firstSeenAtRenewal := lineage("apple-existing", "customer-1", day(5), day(35))
	firstSeenAtRenewal.Provider = core.ProviderAppleAppStore
	firstSeenAtRenewal.StartedAt = &originalStart
	firstSeenAtRenewal.PlanChanges, firstSeenAtRenewal.Resubscriptions = 2, 1
	originalInWindow := lineage("apple-new", "customer-2", day(7), day(37))
	originalInWindow.Provider = core.ProviderAppleAppStore
	originalInWindow.StartedAt = &originalInWindow.FirstStartsAt
	replacedWithGap := lineage("google-old", "customer-3", day(-40), day(10))
	replacedWithGap.Latest, replacedWithGap.CancellationReason = expired, core.CancellationReasonReplacement
	replacement := lineage("google-new", "customer-3", day(12), day(42))
	replacement.Linked = true
	customerCanceled := lineage("google-customer", "customer-4", day(-10), day(11))
	customerCanceled.Latest, customerCanceled.CancellationReason = expired, core.CancellationReasonCustomer
	systemCanceled := lineage("google-system", "customer-5", day(-10), day(12))
	systemCanceled.Latest, systemCanceled.CancellationReason = expired, core.CancellationReasonSystem
	developerCanceled := lineage("google-developer", "customer-6", day(-10), day(13))
	developerCanceled.Latest, developerCanceled.CancellationReason = expired, core.CancellationReasonDeveloper

	metrics := persistence.ComputeSubscriptionMetrics(
		persistence.SubscriptionMetricsQuery{Environment: core.EnvironmentProduction, WindowDays: 30}, window, nil,
		[]persistence.SubscriptionLineage{
			firstSeenAtRenewal, originalInWindow, replacedWithGap, replacement, customerCanceled, systemCanceled, developerCanceled,
		},
	)
	for name, got := range map[string][2]int64{
		"new":             {metrics.New, 1},
		"plan changes":    {metrics.PlanChanges, 2},
		"resubscriptions": {metrics.Resubscriptions, 1},
		"replaced":        {metrics.Churn.Replaced, 1},
		"churned":         {metrics.Churn.Churned, 3},
		"voluntary":       {metrics.Churn.Voluntary, 1},
		"billing":         {metrics.Churn.Billing, 1},
		"unclassified":    {metrics.Churn.Unclassified, 1},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %d, want %d", name, got[0], got[1])
		}
	}
}

// TestComputeSubscriptionMetricsNeverEvaluatesTheFuture keeps the daily series at or before generation time.
func TestComputeSubscriptionMetricsNeverEvaluatesTheFuture(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.November, 12, 0, 0, 0, 0, time.UTC)
	measuredAt := start.Add(6*24*time.Hour + 3*time.Hour)
	window := persistence.SubscriptionWindow{Start: start, End: start.Add(7 * 24 * time.Hour), MeasuredAt: measuredAt}
	endsSoon := persistence.SubscriptionLineage{
		Key: "ends-after-measurement", ApplicationID: "app-1", Provider: core.ProviderAppleAppStore,
		CustomerID: "customer-1", FirstStartsAt: start.Add(-24 * time.Hour),
		Intervals: []persistence.SubscriptionInterval{{
			ProductID: "monthly", StartsAt: start.Add(-24 * time.Hour), EndsAt: measuredAt.Add(time.Hour),
		}},
		Latest: persistence.SubscriptionEvidence{State: core.LifecycleCanceled, Access: core.AccessAllowed},
	}
	metrics := persistence.ComputeSubscriptionMetrics(
		persistence.SubscriptionMetricsQuery{Environment: core.EnvironmentSandbox, WindowDays: 7},
		window, nil, []persistence.SubscriptionLineage{endsSoon},
	)
	if metrics.ActiveAtEnd != 1 || metrics.Churn.Churned != 0 {
		t.Fatalf("metrics = %#v, want a subscription ending after generation time to stay active, not churned", metrics)
	}
	if got := metrics.Daily[len(metrics.Daily)-1]; got.Active != 1 {
		t.Fatalf("last daily point = %#v, want evaluation at generation time", got)
	}
	if len(metrics.Providers) != 1 || metrics.Providers[0].Provider != core.ProviderAppleAppStore {
		t.Fatalf("providers = %#v, want the lineage provider even when no application listed it", metrics.Providers)
	}
}

// TestSubscriptionMetricsQueryValidation bounds the window and requires a known environment.
func TestSubscriptionMetricsQueryValidation(t *testing.T) {
	t.Parallel()

	for _, query := range []persistence.SubscriptionMetricsQuery{
		{Environment: core.EnvironmentProduction, WindowDays: 0},
		{Environment: core.EnvironmentProduction, WindowDays: 91},
		{Environment: "staging", WindowDays: 30},
	} {
		if err := query.Validate(); err == nil {
			t.Errorf("Validate(%#v) error = nil", query)
		}
	}
	if err := (persistence.SubscriptionMetricsQuery{Environment: core.EnvironmentTest, WindowDays: 90}).Validate(); err != nil {
		t.Fatalf("Validate() valid query error = %v", err)
	}
}
