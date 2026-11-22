package controlplane

import (
	"context"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// TrialAvailabilityAvailable means the provider adapter records a signed offer phase.
	TrialAvailabilityAvailable = "available"
	// TrialAvailabilityNotAvailable means the provider adapter records no offer phase, so trials are not counted.
	TrialAvailabilityNotAvailable = "not_available"
)

// SubscriptionMetricsInput selects one project environment and window. Empty values use the defaults.
type SubscriptionMetricsInput struct {
	ProjectID   core.ProjectID
	Environment core.Environment
	// WindowDays is nil for the default window.
	WindowDays *int
}

// SubscriptionMetrics is subscription health for one project environment and UTC window.
type SubscriptionMetrics struct {
	Environment        core.Environment        `json:"environment"`
	WindowDays         int                     `json:"window_days"`
	WindowStart        time.Time               `json:"window_start"`
	WindowEnd          time.Time               `json:"window_end"`
	MeasuredAt         time.Time               `json:"measured_at"`
	ActiveAtStart      int64                   `json:"active_at_start"`
	ActiveAtEnd        int64                   `json:"active_at_end"`
	InTrialAtEnd       int64                   `json:"in_trial_at_end"`
	ActiveDuringWindow int64                   `json:"active_during_window"`
	NewSubscriptions   int64                   `json:"new_subscriptions"`
	Renewals           int64                   `json:"renewals"`
	PlanChanges        int64                   `json:"plan_changes"`
	Resubscriptions    int64                   `json:"resubscriptions"`
	Churn              SubscriptionChurn       `json:"churn"`
	Trials             []SubscriptionTrial     `json:"trials"`
	Breakdown          []SubscriptionBreakdown `json:"breakdown"`
	BreakdownTruncated bool                    `json:"breakdown_truncated"`
	DailyActive        []SubscriptionDaily     `json:"daily_active"`
}

// SubscriptionChurn splits ended subscriptions and states the churn rate.
// Rate is churned divided by active_during_window, or null when nothing was active.
type SubscriptionChurn struct {
	Churned      int64 `json:"churned"`
	Voluntary    int64 `json:"voluntary"`
	Billing      int64 `json:"billing"`
	Reversed     int64 `json:"reversed"`
	Unclassified int64 `json:"unclassified"`
	Replaced     int64 `json:"replaced"`
	Paused       int64 `json:"paused"`
	// AwaitingEvidence counts periods that ended recently with no stored outcome yet. Not churn.
	AwaitingEvidence int64    `json:"awaiting_evidence"`
	Rate             *float64 `json:"rate"`
}

// SubscriptionTrial reports trial evidence for one provider. Counts are null when the provider
// adapter records no offer phase, so an absent signal is never shown as zero trials.
type SubscriptionTrial struct {
	Provider           core.Provider `json:"provider"`
	Availability       string        `json:"availability"`
	ActiveDuringWindow int64         `json:"active_during_window"`
	WithOfferPhase     int64         `json:"with_offer_phase"`
	Started            *int64        `json:"started"`
	Ended              *int64        `json:"ended"`
	Converted          *int64        `json:"converted"`
	NotConverted       *int64        `json:"not_converted"`
	OutcomeNotRecorded *int64        `json:"outcome_not_recorded"`
	ConversionRate     *float64      `json:"conversion_rate"`
}

// SubscriptionBreakdown reports one application and product.
type SubscriptionBreakdown struct {
	ApplicationID    core.ApplicationID `json:"application_id"`
	Provider         core.Provider      `json:"provider"`
	ProductID        core.ProductID     `json:"product_id"`
	ActiveAtEnd      int64              `json:"active_at_end"`
	NewSubscriptions int64              `json:"new_subscriptions"`
	Renewals         int64              `json:"renewals"`
	Churned          int64              `json:"churned"`
}

// SubscriptionDaily is the active subscription count at the end of one UTC day.
type SubscriptionDaily struct {
	Date   string `json:"date"`
	Active int64  `json:"active"`
}

// SubscriptionMetrics returns subscription health derived from stored purchase observations.
func (service *Service) SubscriptionMetrics(
	ctx context.Context,
	actor Actor,
	input SubscriptionMetricsInput,
) (SubscriptionMetrics, error) {
	if err := actor.AuthorizeProject(input.ProjectID); err != nil {
		return SubscriptionMetrics{}, err
	}
	if err := input.ProjectID.Validate(); err != nil {
		return SubscriptionMetrics{}, validation.Wrap(err)
	}
	query := persistence.SubscriptionMetricsQuery{
		Environment: core.EnvironmentProduction,
		WindowDays:  persistence.SubscriptionMetricsDefaultWindowDays,
	}
	if input.Environment != "" {
		query.Environment = input.Environment
	}
	if input.WindowDays != nil {
		query.WindowDays = *input.WindowDays
	}
	if err := query.Validate(); err != nil {
		return SubscriptionMetrics{}, validation.Wrap(err)
	}
	if service.dependencies.Admin == nil {
		return SubscriptionMetrics{}, errMissingDependency
	}
	metrics, err := service.dependencies.Admin.AdminSubscriptionMetrics(ctx, input.ProjectID, query)
	if err != nil {
		return SubscriptionMetrics{}, err
	}
	return SubscriptionMetricsView(metrics), nil
}

// SubscriptionMetricsView maps the persistence metrics onto the public view.
func SubscriptionMetricsView(metrics persistence.SubscriptionMetrics) SubscriptionMetrics {
	response := SubscriptionMetrics{
		Environment: metrics.Environment, WindowDays: metrics.WindowDays,
		WindowStart: metrics.Window.Start, WindowEnd: metrics.Window.End, MeasuredAt: metrics.Window.MeasuredAt,
		ActiveAtStart: metrics.ActiveAtStart, ActiveAtEnd: metrics.ActiveAtEnd, InTrialAtEnd: metrics.InTrialAtEnd,
		ActiveDuringWindow: metrics.ActiveDuringWindow, NewSubscriptions: metrics.New, Renewals: metrics.Renewals,
		PlanChanges: metrics.PlanChanges, Resubscriptions: metrics.Resubscriptions,
		Churn: SubscriptionChurn{
			Churned: metrics.Churn.Churned, Voluntary: metrics.Churn.Voluntary, Billing: metrics.Churn.Billing,
			Reversed: metrics.Churn.Reversed, Unclassified: metrics.Churn.Unclassified,
			Replaced: metrics.Churn.Replaced, Paused: metrics.Churn.Paused, AwaitingEvidence: metrics.Churn.AwaitingEvidence,
			Rate: ratio(metrics.Churn.Churned, metrics.ActiveDuringWindow),
		},
		Trials:             make([]SubscriptionTrial, 0, len(metrics.Providers)),
		Breakdown:          make([]SubscriptionBreakdown, 0, len(metrics.Breakdown)),
		BreakdownTruncated: metrics.BreakdownTruncated,
		DailyActive:        make([]SubscriptionDaily, 0, len(metrics.Daily)),
	}
	for _, provider := range metrics.Providers {
		trial := SubscriptionTrial{
			Provider: provider.Provider, Availability: TrialAvailabilityNotAvailable,
			ActiveDuringWindow: provider.ActiveDuringWindow, WithOfferPhase: provider.WithOfferPhase,
		}
		if stores.RecordsOfferPhase(provider.Provider) {
			trial.Availability = TrialAvailabilityAvailable
			trial.Started = int64Pointer(provider.TrialsStarted)
			trial.Ended = int64Pointer(provider.TrialsEnded)
			trial.Converted = int64Pointer(provider.TrialsConverted)
			trial.NotConverted = int64Pointer(provider.TrialsNotConverted)
			trial.OutcomeNotRecorded = int64Pointer(provider.TrialsOutcomeNotRecorded)
			trial.ConversionRate = ratio(provider.TrialsConverted, provider.TrialsConverted+provider.TrialsNotConverted)
		}
		response.Trials = append(response.Trials, trial)
	}
	for _, row := range metrics.Breakdown {
		response.Breakdown = append(response.Breakdown, SubscriptionBreakdown{
			ApplicationID: row.ApplicationID, Provider: row.Provider, ProductID: row.ProductID,
			ActiveAtEnd: row.ActiveAtEnd, NewSubscriptions: row.New, Renewals: row.Renewals, Churned: row.Churned,
		})
	}
	for _, day := range metrics.Daily {
		response.DailyActive = append(response.DailyActive, SubscriptionDaily{Date: day.Date, Active: day.Active})
	}
	return response
}

// ratio returns numerator divided by denominator, or nil when the denominator is not positive.
func ratio(numerator, denominator int64) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}

// int64Pointer returns a pointer to a copy of value.
func int64Pointer(value int64) *int64 {
	return &value
}
