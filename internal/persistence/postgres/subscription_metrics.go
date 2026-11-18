package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
)

const (
	// subscriptionLineagesSQL reads one row per subscription lineage with an allowed period that
	// overlaps the window. $1 is the project, $2 the environment, $3 the window start, and $4 the
	// measurement instant. Every subscription observation of the environment is read once; the
	// CTEs join on (application_id, lineage) so no step is quadratic in the number of snapshots.
	subscriptionLineagesSQL = `
		WITH scope AS (
			SELECT application.id AS application_id, application.provider
			FROM applications AS application
			WHERE application.project_id = $1 AND application.environment = $2
		), snapshots AS MATERIALIZED (
			SELECT po.id, po.application_id, scope.provider, po.product_id, po.provider_product_id,
				po.lifecycle_state, po.access_status, po.access_reason, po.renewal_status,
				po.effective_starts_at, po.effective_ends_at, po.observed_at, po.created_at, po.offer_phase,
				po.transaction_reason, po.lineage_started_at, po.cancellation_reason,
				COALESCE(customer.merged_into_customer_id, customer.id) AS customer_id,
				COALESCE(NULLIF(lineage.key, ''), 'observation:' || po.id) AS lineage,
				transaction_reference.key AS transaction_key,
				transaction_reference.reference_id AS transaction_reference_id,
				EXISTS (
					SELECT 1
					FROM observation_references AS linked_link
					JOIN provider_references AS linked_reference
						ON linked_reference.id = linked_link.reference_id
						AND linked_reference.application_id = linked_link.application_id
					WHERE linked_link.observation_id = po.id
						AND linked_link.application_id = po.application_id
						AND linked_reference.role = 'linked_lineage'
				) AS linked
			FROM purchase_observations AS po
			JOIN scope ON scope.application_id = po.application_id
			JOIN customers AS customer
				ON customer.id = po.customer_id AND customer.project_id = po.project_id
			CROSS JOIN LATERAL (SELECT ` + observationLineageSQL + ` AS key) AS lineage
			LEFT JOIN LATERAL (
				SELECT 'reference:' || transaction_ref.id::text AS key, transaction_ref.id AS reference_id
				FROM observation_references AS transaction_link
				JOIN provider_references AS transaction_ref
					ON transaction_ref.id = transaction_link.reference_id
					AND transaction_ref.application_id = transaction_link.application_id
				WHERE transaction_link.observation_id = po.id
					AND transaction_link.application_id = po.application_id
					AND transaction_ref.role = 'transaction'
				ORDER BY transaction_ref.kind, transaction_ref.id
				LIMIT 1
			) AS transaction_reference ON true
			WHERE po.project_id = $1
				AND po.product_kind = 'subscription'
				AND po.ownership <> 'family_shared'
		), reversals AS (
			SELECT application_id, lineage, transaction_key, created_at
			FROM snapshots
			WHERE lifecycle_state IN ('refunded', 'revoked') AND access_status = 'denied'
		), caps AS (
			SELECT allowed.id, min(reversal.created_at) AS cap
			FROM snapshots AS allowed
			JOIN reversals AS reversal
				ON reversal.application_id = allowed.application_id AND reversal.lineage = allowed.lineage
			WHERE allowed.access_status = 'allowed'
				AND (reversal.transaction_key IS NULL OR reversal.transaction_key = allowed.transaction_key)
				AND reversal.created_at > allowed.effective_starts_at
			GROUP BY allowed.id
		), intervals AS (
			SELECT snapshot.application_id, snapshot.lineage, snapshot.product_id, snapshot.offer_phase,
				snapshot.effective_starts_at AS starts_at,
				LEAST(snapshot.effective_ends_at, COALESCE(caps.cap, 'infinity'::timestamptz)) AS ends_at
			FROM snapshots AS snapshot
			LEFT JOIN caps ON caps.id = snapshot.id
			WHERE snapshot.access_status = 'allowed'
				AND snapshot.effective_starts_at IS NOT NULL
				AND snapshot.effective_ends_at IS NOT NULL
		), valid_intervals AS (
			SELECT * FROM intervals WHERE ends_at > starts_at
		), window_intervals AS (
			SELECT application_id, lineage,
				array_agg(product_id ORDER BY starts_at, ends_at, product_id) AS products,
				array_agg(starts_at ORDER BY starts_at, ends_at, product_id) AS starts,
				array_agg(ends_at ORDER BY starts_at, ends_at, product_id) AS ends,
				array_agg(COALESCE(offer_phase, '') ORDER BY starts_at, ends_at, product_id) AS phases
			FROM valid_intervals
			WHERE ends_at >= $3 AND starts_at <= $4
			GROUP BY application_id, lineage
		), lineage_first AS (
			SELECT application_id, lineage, min(starts_at) AS first_starts_at
			FROM valid_intervals
			GROUP BY application_id, lineage
		), lineage_facts AS (
			SELECT application_id, lineage,
				bool_or(linked) AS linked,
				bool_or(offer_phase IS NOT NULL) AS phase_recorded,
				min(lineage_started_at) AS started_at
			FROM snapshots
			GROUP BY application_id, lineage
		), latest AS (
			SELECT DISTINCT ON (application_id, lineage)
				application_id, lineage, provider, customer_id, lifecycle_state, access_status, access_reason, renewal_status,
				cancellation_reason
			FROM snapshots
			ORDER BY application_id, lineage, observed_at DESC, created_at DESC, id DESC
		), last_live AS (
			SELECT DISTINCT ON (application_id, lineage)
				application_id, lineage, lifecycle_state, access_status, access_reason, renewal_status
			FROM snapshots
			WHERE lifecycle_state IN ('active', 'grace_period', 'on_hold', 'canceled')
			ORDER BY application_id, lineage, observed_at DESC, created_at DESC, id DESC
		), transactions AS (
			SELECT application_id, lineage, transaction_key, min(transaction_reference_id) AS transaction_reference_id,
				min(created_at) AS first_stored_at,
				min(effective_starts_at) AS first_starts_at,
				(array_agg(product_id ORDER BY created_at, id))[1] AS product_id,
				(array_agg(provider_product_id ORDER BY created_at, id))[1] AS provider_product_id,
				(array_agg(transaction_reason ORDER BY created_at, id)
					FILTER (WHERE transaction_reason IS NOT NULL))[1] AS reason
			FROM snapshots
			WHERE transaction_key IS NOT NULL
			GROUP BY application_id, lineage, transaction_key
		), ranked_transactions AS (
			-- Transactions stored in one database transaction share first_stored_at, so the signed period
			-- start orders them next (Apple stores an upgrade before the transaction it supersedes), then
			-- the numeric reference id.
			SELECT application_id, lineage, product_id, provider_product_id, first_stored_at, transaction_key, reason,
				row_number() OVER transaction_order AS transaction_rank,
				lag(provider_product_id) OVER transaction_order AS previous_provider_product_id
			FROM transactions
			WINDOW transaction_order AS (
				PARTITION BY application_id, lineage
				ORDER BY first_stored_at, first_starts_at NULLS LAST, transaction_reference_id
			)
		), classified_transactions AS (
			-- Without a signed reason every later transaction stays a renewal, as before migration 014.
			SELECT application_id, lineage, product_id, first_stored_at, transaction_key, transaction_rank,
				CASE
					WHEN reason IS NULL THEN 'renewal'
					WHEN provider_product_id IS DISTINCT FROM previous_provider_product_id THEN 'plan_change'
					WHEN reason = 'purchase' THEN 'resubscription'
					ELSE 'renewal'
				END AS transaction_kind
			FROM ranked_transactions
		), renewals AS (
			SELECT application_id, lineage,
				COALESCE(array_agg(product_id ORDER BY first_stored_at, transaction_key)
					FILTER (WHERE transaction_kind = 'renewal'), ARRAY[]::text[]) AS products,
				count(*) FILTER (WHERE transaction_kind = 'plan_change') AS plan_changes,
				count(*) FILTER (WHERE transaction_kind = 'resubscription') AS resubscriptions
			FROM classified_transactions
			WHERE transaction_rank > 1 AND first_stored_at >= $3 AND first_stored_at < $4
			GROUP BY application_id, lineage
		), trials AS (
			SELECT application_id, lineage,
				min(effective_starts_at) AS starts_at,
				max(effective_ends_at) AS ends_at,
				min(created_at) AS first_stored_at
			FROM snapshots
			WHERE offer_phase = 'free_trial'
				AND effective_starts_at IS NOT NULL
				AND effective_ends_at IS NOT NULL
			GROUP BY application_id, lineage
		), trial_outcomes AS (
			SELECT trial.application_id, trial.lineage, trial.starts_at, trial.ends_at,
				COALESCE(bool_or(later.offer_phase IN ('base', 'discounted')), false) AS converted,
				COALESCE(bool_or(
					later.id IS NOT NULL
					AND (later.offer_phase IS NULL OR later.offer_phase IN ('unknown', 'proration'))
				), false) AS unknown_after
			FROM trials AS trial
			LEFT JOIN snapshots AS later
				ON later.application_id = trial.application_id
				AND later.lineage = trial.lineage
				AND later.access_status = 'allowed'
				AND later.created_at >= trial.first_stored_at
				AND later.effective_starts_at >= trial.starts_at
				AND later.offer_phase IS DISTINCT FROM 'free_trial'
			GROUP BY trial.application_id, trial.lineage, trial.starts_at, trial.ends_at
		)
		SELECT window_intervals.lineage, window_intervals.application_id, latest.provider, latest.customer_id,
			lineage_facts.linked, lineage_facts.phase_recorded, lineage_first.first_starts_at,
			window_intervals.products, window_intervals.starts, window_intervals.ends, window_intervals.phases,
			latest.lifecycle_state, latest.access_status, latest.access_reason, latest.renewal_status,
			last_live.lifecycle_state, last_live.access_status, last_live.access_reason, last_live.renewal_status,
			COALESCE(renewals.products, ARRAY[]::text[]),
			trial_outcomes.starts_at, trial_outcomes.ends_at,
			COALESCE(trial_outcomes.converted, false), COALESCE(trial_outcomes.unknown_after, false),
			lineage_facts.started_at, COALESCE(latest.cancellation_reason, ''),
			COALESCE(renewals.plan_changes, 0), COALESCE(renewals.resubscriptions, 0)
		FROM window_intervals
		JOIN latest USING (application_id, lineage)
		JOIN lineage_facts USING (application_id, lineage)
		JOIN lineage_first USING (application_id, lineage)
		LEFT JOIN last_live USING (application_id, lineage)
		LEFT JOIN renewals USING (application_id, lineage)
		LEFT JOIN trial_outcomes USING (application_id, lineage)
		ORDER BY window_intervals.application_id, window_intervals.lineage
	`
)

// AdminSubscriptionMetrics returns subscription health for one project environment inside one read snapshot.
func (store *Store) AdminSubscriptionMetrics(
	ctx context.Context,
	projectID core.ProjectID,
	query persistence.SubscriptionMetricsQuery,
) (persistence.SubscriptionMetrics, error) {
	if err := projectID.Validate(); err != nil {
		return persistence.SubscriptionMetrics{}, err
	}
	if err := query.Validate(); err != nil {
		return persistence.SubscriptionMetrics{}, err
	}
	var metrics persistence.SubscriptionMetrics
	err := store.transact(ctx, pgx.RepeatableRead, func(repository *transaction) error {
		var loadErr error
		metrics, loadErr = repository.adminSubscriptionMetrics(ctx, projectID, query)
		return loadErr
	})
	return metrics, err
}

// adminSubscriptionMetrics loads the window, the providers in scope, and every relevant lineage.
func (repository *transaction) adminSubscriptionMetrics(
	ctx context.Context,
	projectID core.ProjectID,
	query persistence.SubscriptionMetricsQuery,
) (persistence.SubscriptionMetrics, error) {
	if _, err := repository.adminProject(ctx, projectID); err != nil {
		return persistence.SubscriptionMetrics{}, err
	}
	var window persistence.SubscriptionWindow
	if err := repository.tx.QueryRow(ctx, `
		SELECT CURRENT_TIMESTAMP,
			`+utcTodayStartSQL+`
				- ($1::integer - 1) * INTERVAL '1 day',
			`+utcTodayStartSQL+` + INTERVAL '1 day'
	`, query.WindowDays).Scan(&window.MeasuredAt, &window.Start, &window.End); err != nil {
		return persistence.SubscriptionMetrics{}, classifyError("load subscription metrics window", err)
	}
	window.MeasuredAt = window.MeasuredAt.UTC()
	window.Start = window.Start.UTC()
	window.End = window.End.UTC()

	providers, err := repository.subscriptionProviders(ctx, projectID, query.Environment)
	if err != nil {
		return persistence.SubscriptionMetrics{}, err
	}
	lineages, err := repository.subscriptionLineages(ctx, projectID, query.Environment, window)
	if err != nil {
		return persistence.SubscriptionMetrics{}, err
	}
	return persistence.ComputeSubscriptionMetrics(query, window, providers, lineages), nil
}

// subscriptionProviders lists the providers of applications in one project environment.
func (repository *transaction) subscriptionProviders(
	ctx context.Context,
	projectID core.ProjectID,
	environment core.Environment,
) ([]core.Provider, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT DISTINCT provider
		FROM applications
		WHERE project_id = $1 AND environment = $2
		ORDER BY provider
	`, projectID, environment)
	if err != nil {
		return nil, classifyError("list subscription metric providers", err)
	}
	defer rows.Close()
	providers := make([]core.Provider, 0)
	for rows.Next() {
		var provider core.Provider
		if err := rows.Scan(&provider); err != nil {
			return nil, classifyError("scan subscription metric provider", err)
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate subscription metric providers", err)
	}
	return providers, nil
}

// subscriptionLineages reads one evidence row per lineage with an allowed period overlapping the window.
func (repository *transaction) subscriptionLineages(
	ctx context.Context,
	projectID core.ProjectID,
	environment core.Environment,
	window persistence.SubscriptionWindow,
) ([]persistence.SubscriptionLineage, error) {
	rows, err := repository.tx.Query(ctx, subscriptionLineagesSQL, projectID, environment, window.Start, window.MeasuredAt)
	if err != nil {
		return nil, classifyError("list subscription lineages", err)
	}
	defer rows.Close()

	lineages := make([]persistence.SubscriptionLineage, 0)
	for rows.Next() {
		var lineage persistence.SubscriptionLineage
		var products, phases, renewals []string
		var starts, ends []time.Time
		var liveState, liveAccess, liveReason, liveRenewal *string
		var trialStarts, trialEnds *time.Time
		var converted, unknownAfter bool
		var startedAt *time.Time
		if err := rows.Scan(
			&lineage.Key, &lineage.ApplicationID, &lineage.Provider, &lineage.CustomerID,
			&lineage.Linked, &lineage.PhaseRecorded, &lineage.FirstStartsAt,
			&products, &starts, &ends, &phases,
			&lineage.Latest.State, &lineage.Latest.Access, &lineage.Latest.Reason, &lineage.Latest.RenewalStatus,
			&liveState, &liveAccess, &liveReason, &liveRenewal,
			&renewals, &trialStarts, &trialEnds, &converted, &unknownAfter,
			&startedAt, &lineage.CancellationReason, &lineage.PlanChanges, &lineage.Resubscriptions,
		); err != nil {
			return nil, classifyError("scan subscription lineage", err)
		}
		if len(products) != len(starts) || len(starts) != len(ends) || len(ends) != len(phases) {
			return nil, errors.New("subscription lineage interval arrays differ in length")
		}
		lineage.FirstStartsAt = lineage.FirstStartsAt.UTC()
		if startedAt != nil {
			started := startedAt.UTC()
			lineage.StartedAt = &started
		}
		lineage.Intervals = make([]persistence.SubscriptionInterval, 0, len(products))
		for index := range products {
			lineage.Intervals = append(lineage.Intervals, persistence.SubscriptionInterval{
				ProductID: core.ProductID(products[index]), StartsAt: starts[index].UTC(),
				EndsAt: ends[index].UTC(), OfferPhase: core.OfferPhase(phases[index]),
			})
		}
		if liveState != nil {
			lineage.LastLive = &persistence.SubscriptionEvidence{
				State: core.LifecycleState(*liveState), Access: core.AccessStatus(*liveAccess),
				Reason: core.AccessReason(*liveReason), RenewalStatus: core.RenewalStatus(*liveRenewal),
			}
		}
		lineage.Renewals = make([]core.ProductID, 0, len(renewals))
		for _, productID := range renewals {
			lineage.Renewals = append(lineage.Renewals, core.ProductID(productID))
		}
		if trialStarts != nil && trialEnds != nil {
			lineage.Trial = &persistence.SubscriptionTrialEvidence{
				StartsAt: trialStarts.UTC(), EndsAt: trialEnds.UTC(),
				Converted: converted, UnknownAfter: unknownAfter,
			}
		}
		lineages = append(lineages, lineage)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate subscription lineages", err)
	}
	return lineages, nil
}
