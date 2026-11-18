package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// trialAvailabilityAvailable means the provider adapter records a signed offer phase.
	trialAvailabilityAvailable = "available"
	// trialAvailabilityNotAvailable means the provider adapter records no offer phase, so trials are not counted.
	trialAvailabilityNotAvailable = "not_available"
)

// adminSubscriptionMetricsResponse is subscription health for one project environment and UTC window.
type adminSubscriptionMetricsResponse struct {
	Environment        core.Environment                     `json:"environment"`
	WindowDays         int                                  `json:"window_days"`
	WindowStart        time.Time                            `json:"window_start"`
	WindowEnd          time.Time                            `json:"window_end"`
	MeasuredAt         time.Time                            `json:"measured_at"`
	ActiveAtStart      int64                                `json:"active_at_start"`
	ActiveAtEnd        int64                                `json:"active_at_end"`
	InTrialAtEnd       int64                                `json:"in_trial_at_end"`
	ActiveDuringWindow int64                                `json:"active_during_window"`
	NewSubscriptions   int64                                `json:"new_subscriptions"`
	Renewals           int64                                `json:"renewals"`
	PlanChanges        int64                                `json:"plan_changes"`
	Resubscriptions    int64                                `json:"resubscriptions"`
	Churn              adminSubscriptionChurnResponse       `json:"churn"`
	Trials             []adminSubscriptionTrialResponse     `json:"trials"`
	Breakdown          []adminSubscriptionBreakdownResponse `json:"breakdown"`
	BreakdownTruncated bool                                 `json:"breakdown_truncated"`
	DailyActive        []adminSubscriptionDailyResponse     `json:"daily_active"`
}

// adminSubscriptionChurnResponse splits ended subscriptions and states the churn rate.
// Rate is churned divided by active_during_window, or null when nothing was active.
type adminSubscriptionChurnResponse struct {
	Churned      int64    `json:"churned"`
	Voluntary    int64    `json:"voluntary"`
	Billing      int64    `json:"billing"`
	Reversed     int64    `json:"reversed"`
	Unclassified int64    `json:"unclassified"`
	Replaced     int64    `json:"replaced"`
	Paused       int64    `json:"paused"`
	Rate         *float64 `json:"rate"`
}

// adminSubscriptionTrialResponse reports trial evidence for one provider. Counts are null when the
// provider adapter records no offer phase, so an absent signal is never shown as zero trials.
type adminSubscriptionTrialResponse struct {
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

// adminSubscriptionBreakdownResponse reports one application and product.
type adminSubscriptionBreakdownResponse struct {
	ApplicationID    core.ApplicationID `json:"application_id"`
	Provider         core.Provider      `json:"provider"`
	ProductID        core.ProductID     `json:"product_id"`
	ActiveAtEnd      int64              `json:"active_at_end"`
	NewSubscriptions int64              `json:"new_subscriptions"`
	Renewals         int64              `json:"renewals"`
	Churned          int64              `json:"churned"`
}

// adminSubscriptionDailyResponse is the active subscription count at the end of one UTC day.
type adminSubscriptionDailyResponse struct {
	Date   string `json:"date"`
	Active int64  `json:"active"`
}

// adminSubscriptionMetrics returns subscription health derived from stored purchase observations.
func (api *API) adminSubscriptionMetrics(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	if !api.requireValidInput(writer, request, projectID.Validate()) {
		return
	}
	query, err := subscriptionMetricsQuery(request)
	if !api.requireValidInput(writer, request, err) {
		return
	}
	metrics, err := api.admin.AdminSubscriptionMetrics(request.Context(), projectID, query)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, subscriptionMetricsFromRecord(metrics))
}

// subscriptionMetricsQuery reads the optional environment and window length.
func subscriptionMetricsQuery(request *http.Request) (persistence.SubscriptionMetricsQuery, error) {
	query := persistence.SubscriptionMetricsQuery{
		Environment: core.EnvironmentProduction,
		WindowDays:  persistence.SubscriptionMetricsDefaultWindowDays,
	}
	values := request.URL.Query()
	if raw := values.Get("environment"); raw != "" {
		query.Environment = core.Environment(raw)
	}
	if raw := values.Get("window_days"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil {
			return persistence.SubscriptionMetricsQuery{}, errors.New("window_days must be an integer")
		}
		query.WindowDays = days
	}
	if err := query.Validate(); err != nil {
		return persistence.SubscriptionMetricsQuery{}, err
	}
	return query, nil
}

// subscriptionMetricsFromRecord maps the persistence metrics onto the public contract.
func subscriptionMetricsFromRecord(metrics persistence.SubscriptionMetrics) adminSubscriptionMetricsResponse {
	response := adminSubscriptionMetricsResponse{
		Environment: metrics.Environment, WindowDays: metrics.WindowDays,
		WindowStart: metrics.Window.Start, WindowEnd: metrics.Window.End, MeasuredAt: metrics.Window.MeasuredAt,
		ActiveAtStart: metrics.ActiveAtStart, ActiveAtEnd: metrics.ActiveAtEnd, InTrialAtEnd: metrics.InTrialAtEnd,
		ActiveDuringWindow: metrics.ActiveDuringWindow, NewSubscriptions: metrics.New, Renewals: metrics.Renewals,
		PlanChanges: metrics.PlanChanges, Resubscriptions: metrics.Resubscriptions,
		Churn: adminSubscriptionChurnResponse{
			Churned: metrics.Churn.Churned, Voluntary: metrics.Churn.Voluntary, Billing: metrics.Churn.Billing,
			Reversed: metrics.Churn.Reversed, Unclassified: metrics.Churn.Unclassified,
			Replaced: metrics.Churn.Replaced, Paused: metrics.Churn.Paused,
			Rate: ratio(metrics.Churn.Churned, metrics.ActiveDuringWindow),
		},
		Trials:             make([]adminSubscriptionTrialResponse, 0, len(metrics.Providers)),
		Breakdown:          make([]adminSubscriptionBreakdownResponse, 0, len(metrics.Breakdown)),
		BreakdownTruncated: metrics.BreakdownTruncated,
		DailyActive:        make([]adminSubscriptionDailyResponse, 0, len(metrics.Daily)),
	}
	for _, provider := range metrics.Providers {
		trial := adminSubscriptionTrialResponse{
			Provider: provider.Provider, Availability: trialAvailabilityNotAvailable,
			ActiveDuringWindow: provider.ActiveDuringWindow, WithOfferPhase: provider.WithOfferPhase,
		}
		if stores.RecordsOfferPhase(provider.Provider) {
			trial.Availability = trialAvailabilityAvailable
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
		response.Breakdown = append(response.Breakdown, adminSubscriptionBreakdownResponse{
			ApplicationID: row.ApplicationID, Provider: row.Provider, ProductID: row.ProductID,
			ActiveAtEnd: row.ActiveAtEnd, NewSubscriptions: row.New, Renewals: row.Renewals, Churned: row.Churned,
		})
	}
	for _, day := range metrics.Daily {
		response.DailyActive = append(response.DailyActive, adminSubscriptionDailyResponse{Date: day.Date, Active: day.Active})
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
