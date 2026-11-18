package persistence

import (
	"errors"
	"sort"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// SubscriptionMetricsDefaultWindowDays is the UTC day window used when a request names none.
	SubscriptionMetricsDefaultWindowDays = 30
	// SubscriptionMetricsMaximumWindowDays bounds one window and its daily active series.
	SubscriptionMetricsMaximumWindowDays = 90
	// SubscriptionMetricsBreakdownLimit bounds the application and product rows in one response.
	SubscriptionMetricsBreakdownLimit = 200
	// subscriptionDay is one UTC calendar day in the daily series.
	subscriptionDay = 24 * time.Hour

	// churnUnclassified marks an ended subscription whose stored evidence names no reason.
	churnUnclassified subscriptionChurnReason = iota
	// churnReversed marks a subscription that ended with a refund or revocation that denies access.
	churnReversed
	// churnBilling marks a subscription whose last live evidence was a billing grace, hold, or issue.
	churnBilling
	// churnVoluntary marks a subscription whose last live evidence had renewal turned off.
	churnVoluntary
)

// subscriptionChurnReason is the classified reason an ended subscription is counted as churn.
type subscriptionChurnReason int

// SubscriptionMetricsQuery selects one application environment and a window of whole UTC days ending today.
type SubscriptionMetricsQuery struct {
	Environment core.Environment
	WindowDays  int
}

// SubscriptionWindow is the evaluated UTC window. Start is inclusive and End is the exclusive midnight
// after the last day. MeasuredAt is the generation time, which is never after End, so no figure is
// evaluated at a future instant.
type SubscriptionWindow struct {
	Start      time.Time
	End        time.Time
	MeasuredAt time.Time
}

// SubscriptionInterval is one allowed, provider-signed subscription period. EndsAt is already cut at
// the time IAPStack stored a refund or revocation of the same transaction.
type SubscriptionInterval struct {
	ProductID  core.ProductID
	StartsAt   time.Time
	EndsAt     time.Time
	OfferPhase core.OfferPhase
}

// SubscriptionEvidence is the normalized state of one stored subscription snapshot.
type SubscriptionEvidence struct {
	State         core.LifecycleState
	Access        core.AccessStatus
	Reason        core.AccessReason
	RenewalStatus core.RenewalStatus
}

// SubscriptionTrialEvidence is the free-trial period of one lineage and what followed it.
// Converted means a later allowed period carries a paid phase. UnknownAfter means a later allowed
// period exists whose phase was not recorded or is not a known paid phase.
type SubscriptionTrialEvidence struct {
	StartsAt     time.Time
	EndsAt       time.Time
	Converted    bool
	UnknownAfter bool
}

// SubscriptionLineage is one subscription purchase lineage with the stored evidence the metrics read.
// Key is an opaque grouping key that is never returned by the API. Intervals holds the allowed
// periods that overlap the window. FirstStartsAt is the earliest allowed period in all stored history.
type SubscriptionLineage struct {
	Key           string
	ApplicationID core.ApplicationID
	Provider      core.Provider
	CustomerID    core.CustomerID
	Linked        bool
	FirstStartsAt time.Time
	Intervals     []SubscriptionInterval
	Latest        SubscriptionEvidence
	LastLive      *SubscriptionEvidence
	Renewals      []core.ProductID
	PhaseRecorded bool
	Trial         *SubscriptionTrialEvidence
}

// SubscriptionChurn splits subscriptions that ended in the window. Churned is the sum of the four
// reasons. Replaced and Paused ended a period but are not churn.
type SubscriptionChurn struct {
	Churned      int64
	Voluntary    int64
	Billing      int64
	Reversed     int64
	Unclassified int64
	Replaced     int64
	Paused       int64
}

// SubscriptionProviderMetrics reports trial evidence and offer phase coverage for one provider.
type SubscriptionProviderMetrics struct {
	Provider                 core.Provider
	ActiveDuringWindow       int64
	WithOfferPhase           int64
	TrialsStarted            int64
	TrialsEnded              int64
	TrialsConverted          int64
	TrialsNotConverted       int64
	TrialsOutcomeNotRecorded int64
}

// SubscriptionBreakdown reports one application and product.
type SubscriptionBreakdown struct {
	ApplicationID core.ApplicationID
	Provider      core.Provider
	ProductID     core.ProductID
	ActiveAtEnd   int64
	New           int64
	Renewals      int64
	Churned       int64
}

// SubscriptionDailyActive is the active subscription count at the end of one UTC day.
type SubscriptionDailyActive struct {
	Date   string
	Active int64
}

// SubscriptionMetrics is subscription health for one project environment and UTC window.
type SubscriptionMetrics struct {
	Environment        core.Environment
	WindowDays         int
	Window             SubscriptionWindow
	ActiveAtStart      int64
	ActiveAtEnd        int64
	InTrialAtEnd       int64
	ActiveDuringWindow int64
	New                int64
	Renewals           int64
	Churn              SubscriptionChurn
	Providers          []SubscriptionProviderMetrics
	Breakdown          []SubscriptionBreakdown
	BreakdownTruncated bool
	Daily              []SubscriptionDailyActive
}

// subscriptionBreakdownKey identifies one application and product row.
type subscriptionBreakdownKey struct {
	applicationID core.ApplicationID
	productID     core.ProductID
}

// subscriptionCustomerKey groups lineages of one canonical customer inside one application.
type subscriptionCustomerKey struct {
	applicationID core.ApplicationID
	customerID    core.CustomerID
}

// Validate checks the environment and the bounded window length.
func (query SubscriptionMetricsQuery) Validate() error {
	if err := query.Environment.Validate(); err != nil {
		return err
	}
	if query.WindowDays < 1 || query.WindowDays > SubscriptionMetricsMaximumWindowDays {
		return errors.New("subscription metrics window must be between 1 and 90 days")
	}
	return nil
}

// activeAt returns the covering interval with the latest start, so a plan change inside one lineage
// is attributed to the product that applies at that instant.
func (lineage SubscriptionLineage) activeAt(instant time.Time) (SubscriptionInterval, bool) {
	var selected SubscriptionInterval
	found := false
	for _, interval := range lineage.Intervals {
		if interval.StartsAt.After(instant) || !interval.EndsAt.After(instant) {
			continue
		}
		if !found || interval.StartsAt.After(selected.StartsAt) ||
			(interval.StartsAt.Equal(selected.StartsAt) && interval.EndsAt.After(selected.EndsAt)) {
			selected = interval
			found = true
		}
	}
	return selected, found
}

// activeDuring reports whether any interval overlaps the half-open window from start to measuredAt.
func (lineage SubscriptionLineage) activeDuring(start, measuredAt time.Time) bool {
	for _, interval := range lineage.Intervals {
		if interval.StartsAt.Before(measuredAt) && interval.EndsAt.After(start) {
			return true
		}
	}
	return false
}

// lastEnd returns the latest interval end and the interval that produced it.
func (lineage SubscriptionLineage) lastEnd() (SubscriptionInterval, bool) {
	var selected SubscriptionInterval
	found := false
	for _, interval := range lineage.Intervals {
		if !found || interval.EndsAt.After(selected.EndsAt) ||
			(interval.EndsAt.Equal(selected.EndsAt) && interval.StartsAt.After(selected.StartsAt)) {
			selected = interval
			found = true
		}
	}
	return selected, found
}

// firstInterval returns the interval that starts at FirstStartsAt when it overlaps the window.
func (lineage SubscriptionLineage) firstInterval() (SubscriptionInterval, bool) {
	for _, interval := range lineage.Intervals {
		if interval.StartsAt.Equal(lineage.FirstStartsAt) {
			return interval, true
		}
	}
	return SubscriptionInterval{}, false
}

// ComputeSubscriptionMetrics derives every subscription metric from stored lineage evidence.
// It reads only provider-signed periods, normalized states, and IAPStack storage times. It never
// projects a period forward, converts a currency, or infers a trial from a price.
func ComputeSubscriptionMetrics(
	query SubscriptionMetricsQuery,
	window SubscriptionWindow,
	providers []core.Provider,
	lineages []SubscriptionLineage,
) SubscriptionMetrics {
	metrics := SubscriptionMetrics{
		Environment: query.Environment, WindowDays: query.WindowDays, Window: window,
		Breakdown: make([]SubscriptionBreakdown, 0), Daily: make([]SubscriptionDailyActive, 0, query.WindowDays),
	}
	start, measuredAt := window.Start, window.MeasuredAt
	lineages = append([]SubscriptionLineage(nil), lineages...)
	for index := range lineages {
		lineages[index].Intervals = nonEmptyIntervals(lineages[index].Intervals)
	}

	byCustomer := make(map[subscriptionCustomerKey][]int)
	for index, lineage := range lineages {
		key := subscriptionCustomerKey{applicationID: lineage.ApplicationID, customerID: lineage.CustomerID}
		byCustomer[key] = append(byCustomer[key], index)
	}
	// otherCovers reports whether another lineage of the same customer and application has an
	// interval that satisfies covers. It identifies plan changes that use a new lineage.
	otherCovers := func(index int, covers func(SubscriptionInterval) bool) bool {
		lineage := lineages[index]
		key := subscriptionCustomerKey{applicationID: lineage.ApplicationID, customerID: lineage.CustomerID}
		for _, other := range byCustomer[key] {
			if other == index {
				continue
			}
			for _, interval := range lineages[other].Intervals {
				if covers(interval) {
					return true
				}
			}
		}
		return false
	}

	rows := make(map[subscriptionBreakdownKey]*SubscriptionBreakdown)
	providerRows := make(map[core.Provider]*SubscriptionProviderMetrics, len(providers))
	for _, provider := range providers {
		providerRows[provider] = &SubscriptionProviderMetrics{Provider: provider}
	}
	row := func(lineage SubscriptionLineage, productID core.ProductID) *SubscriptionBreakdown {
		key := subscriptionBreakdownKey{applicationID: lineage.ApplicationID, productID: productID}
		if existing, ok := rows[key]; ok {
			return existing
		}
		created := &SubscriptionBreakdown{
			ApplicationID: lineage.ApplicationID, Provider: lineage.Provider, ProductID: productID,
		}
		rows[key] = created
		return created
	}

	for index, lineage := range lineages {
		providerRow, ok := providerRows[lineage.Provider]
		if !ok {
			providerRow = &SubscriptionProviderMetrics{Provider: lineage.Provider}
			providerRows[lineage.Provider] = providerRow
		}
		if _, active := lineage.activeAt(start); active {
			metrics.ActiveAtStart++
		}
		if interval, active := lineage.activeAt(measuredAt); active {
			metrics.ActiveAtEnd++
			row(lineage, interval.ProductID).ActiveAtEnd++
			if interval.OfferPhase == core.OfferPhaseFreeTrial {
				metrics.InTrialAtEnd++
			}
		}
		during := lineage.activeDuring(start, measuredAt)
		if during {
			metrics.ActiveDuringWindow++
			providerRow.ActiveDuringWindow++
			if lineage.PhaseRecorded {
				providerRow.WithOfferPhase++
			}
		}

		first := lineage.FirstStartsAt
		if !first.Before(start) && first.Before(measuredAt) && !lineage.Linked &&
			!otherCovers(index, func(interval SubscriptionInterval) bool {
				return interval.StartsAt.Before(first) && !interval.EndsAt.Before(first)
			}) {
			if interval, found := lineage.firstInterval(); found {
				metrics.New++
				row(lineage, interval.ProductID).New++
			}
		}

		for _, productID := range lineage.Renewals {
			metrics.Renewals++
			row(lineage, productID).Renewals++
		}

		if ending, found := lineage.lastEnd(); found && ending.EndsAt.After(start) && !ending.EndsAt.After(measuredAt) {
			endedAt := ending.EndsAt
			switch {
			case otherCovers(index, func(interval SubscriptionInterval) bool {
				return !interval.StartsAt.After(endedAt) && interval.EndsAt.After(endedAt)
			}):
				metrics.Churn.Replaced++
			case lineage.Latest.State == core.LifecyclePaused:
				metrics.Churn.Paused++
			default:
				switch churnReason(lineage) {
				case churnReversed:
					metrics.Churn.Reversed++
				case churnBilling:
					metrics.Churn.Billing++
				case churnVoluntary:
					metrics.Churn.Voluntary++
				default:
					metrics.Churn.Unclassified++
				}
				metrics.Churn.Churned++
				row(lineage, ending.ProductID).Churned++
			}
		}

		if trial := lineage.Trial; trial != nil {
			if !trial.StartsAt.Before(start) && trial.StartsAt.Before(measuredAt) {
				providerRow.TrialsStarted++
			}
			if trial.EndsAt.After(start) && !trial.EndsAt.After(measuredAt) {
				providerRow.TrialsEnded++
				switch {
				case trial.Converted:
					providerRow.TrialsConverted++
				case trial.UnknownAfter:
					providerRow.TrialsOutcomeNotRecorded++
				default:
					providerRow.TrialsNotConverted++
				}
			}
		}
	}

	for day := 0; day < query.WindowDays; day++ {
		dayStart := start.Add(time.Duration(day) * subscriptionDay)
		if dayStart.After(measuredAt) {
			break
		}
		instant := dayStart.Add(subscriptionDay)
		if instant.After(measuredAt) {
			instant = measuredAt
		}
		var active int64
		for _, lineage := range lineages {
			if _, ok := lineage.activeAt(instant); ok {
				active++
			}
		}
		metrics.Daily = append(metrics.Daily, SubscriptionDailyActive{
			Date: dayStart.UTC().Format(time.DateOnly), Active: active,
		})
	}

	for _, breakdown := range rows {
		metrics.Breakdown = append(metrics.Breakdown, *breakdown)
	}
	sort.Slice(metrics.Breakdown, func(left, right int) bool {
		if metrics.Breakdown[left].ApplicationID != metrics.Breakdown[right].ApplicationID {
			return metrics.Breakdown[left].ApplicationID < metrics.Breakdown[right].ApplicationID
		}
		return metrics.Breakdown[left].ProductID < metrics.Breakdown[right].ProductID
	})
	if len(metrics.Breakdown) > SubscriptionMetricsBreakdownLimit {
		metrics.Breakdown = metrics.Breakdown[:SubscriptionMetricsBreakdownLimit]
		metrics.BreakdownTruncated = true
	}

	metrics.Providers = make([]SubscriptionProviderMetrics, 0, len(providerRows))
	for _, providerRow := range providerRows {
		metrics.Providers = append(metrics.Providers, *providerRow)
	}
	sort.Slice(metrics.Providers, func(left, right int) bool {
		return metrics.Providers[left].Provider < metrics.Providers[right].Provider
	})
	return metrics
}

// nonEmptyIntervals returns a copy without periods whose end is not after their start.
func nonEmptyIntervals(intervals []SubscriptionInterval) []SubscriptionInterval {
	kept := make([]SubscriptionInterval, 0, len(intervals))
	for _, interval := range intervals {
		if interval.EndsAt.After(interval.StartsAt) {
			kept = append(kept, interval)
		}
	}
	return kept
}

// churnReason classifies an ended lineage from its latest and last live evidence, in that order.
// A refund or revocation wins. Billing evidence comes before a disabled renewal, because a customer
// can turn renewal off while a payment is already failing. Missing evidence stays unclassified.
func churnReason(lineage SubscriptionLineage) subscriptionChurnReason {
	if (lineage.Latest.State == core.LifecycleRefunded || lineage.Latest.State == core.LifecycleRevoked) &&
		lineage.Latest.Access == core.AccessDenied {
		return churnReversed
	}
	live := lineage.LastLive
	if live == nil {
		return churnUnclassified
	}
	if live.State == core.LifecycleGracePeriod || live.State == core.LifecycleOnHold ||
		live.Reason == core.AccessReasonBillingIssue {
		return churnBilling
	}
	if live.RenewalStatus == core.RenewalDisabled || live.State == core.LifecycleCanceled ||
		live.Reason == core.AccessReasonCanceledAtPeriodEnd {
		return churnVoluntary
	}
	return churnUnclassified
}
