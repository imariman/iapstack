package postgres

import (
	"context"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
)

const (
	// adminProjectLimit bounds the number of projects returned to one dashboard session.
	adminProjectLimit = 200
	// adminCollectionLimit bounds catalog and customer datasets in one project snapshot.
	adminCollectionLimit = 200
	// adminActivityLimit bounds recent transaction and delivery datasets.
	adminActivityLimit = 50
	// adminAnalyticsWindowDays bounds dashboard activity to a fixed UTC window.
	adminAnalyticsWindowDays = 30
)

var (
	// adminQueryStoreContract verifies that Store exposes the administrative read model.
	_ persistence.AdminQueryStore = (*Store)(nil)
)

// AdminProjects returns bounded project summaries ordered by stable identity.
func (store *Store) AdminProjects(ctx context.Context) ([]persistence.AdminProject, error) {
	var projects []persistence.AdminProject
	err := store.transact(ctx, pgx.RepeatableRead, func(repository *transaction) error {
		var loadErr error
		projects, loadErr = repository.adminProjects(ctx)
		return loadErr
	})
	return projects, err
}

// AdminProjectOverview returns one consistent, bounded, project-scoped operational snapshot.
func (store *Store) AdminProjectOverview(
	ctx context.Context,
	projectID core.ProjectID,
) (persistence.AdminProjectOverview, error) {
	if err := projectID.Validate(); err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	var overview persistence.AdminProjectOverview
	err := store.transact(ctx, pgx.RepeatableRead, func(repository *transaction) error {
		var loadErr error
		overview, loadErr = repository.adminProjectOverview(ctx, projectID)
		return loadErr
	})
	return overview, err
}

// adminProjects loads secret-free project summaries within the dashboard result bound.
func (repository *transaction) adminProjects(ctx context.Context) ([]persistence.AdminProject, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT p.id, p.created_at,
			(SELECT count(*) FROM applications AS a WHERE a.project_id = p.id),
			(SELECT count(*) FROM customers AS c WHERE c.project_id = p.id),
			(SELECT count(*) FROM products AS product WHERE product.project_id = p.id)
		FROM projects AS p
		ORDER BY p.id
		LIMIT $1
	`, adminProjectLimit)
	if err != nil {
		return nil, classifyError("list admin projects", err)
	}
	defer rows.Close()

	projects := make([]persistence.AdminProject, 0)
	for rows.Next() {
		var project persistence.AdminProject
		if err := rows.Scan(
			&project.ID,
			&project.CreatedAt,
			&project.ApplicationCount,
			&project.CustomerCount,
			&project.ProductCount,
		); err != nil {
			return nil, classifyError("scan admin project", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin projects", err)
	}
	return projects, nil
}

// adminProjectOverview loads every bounded dashboard section inside one read transaction.
func (repository *transaction) adminProjectOverview(
	ctx context.Context,
	projectID core.ProjectID,
) (persistence.AdminProjectOverview, error) {
	project, err := repository.adminProject(ctx, projectID)
	if err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	analytics, err := repository.adminAnalytics(ctx, projectID)
	if err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	applications, err := repository.adminApplications(ctx, projectID)
	if err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	products, err := repository.adminProducts(ctx, projectID)
	if err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	customers, err := repository.adminCustomers(ctx, projectID)
	if err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	transactions, err := repository.adminTransactions(ctx, projectID)
	if err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	queues, err := repository.adminQueues(ctx, projectID)
	if err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	webhookEvents, err := repository.adminWebhookEvents(ctx, projectID)
	if err != nil {
		return persistence.AdminProjectOverview{}, err
	}
	return persistence.AdminProjectOverview{
		Project: project, Analytics: analytics, Applications: applications, Products: products,
		Customers: customers, RecentTransactions: transactions, Queues: queues,
		RecentWebhookEvents: webhookEvents,
	}, nil
}

// adminProject loads one project summary or returns a scope-safe not-found result.
func (repository *transaction) adminProject(
	ctx context.Context,
	projectID core.ProjectID,
) (persistence.AdminProject, error) {
	var project persistence.AdminProject
	err := repository.tx.QueryRow(ctx, `
		SELECT p.id, p.created_at,
			(SELECT count(*) FROM applications AS a WHERE a.project_id = p.id),
			(SELECT count(*) FROM customers AS c WHERE c.project_id = p.id),
			(SELECT count(*) FROM products AS product WHERE product.project_id = p.id)
		FROM projects AS p
		WHERE p.id = $1
	`, projectID).Scan(
		&project.ID,
		&project.CreatedAt,
		&project.ApplicationCount,
		&project.CustomerCount,
		&project.ProductCount,
	)
	if err != nil {
		return persistence.AdminProject{}, classifyError("load admin project", err)
	}
	return project, nil
}

// adminAnalytics loads a fixed 30-day UTC activity series and authoritative revenue state.
func (repository *transaction) adminAnalytics(
	ctx context.Context,
	projectID core.ProjectID,
) (persistence.AdminAnalytics, error) {
	analytics := persistence.AdminAnalytics{WindowDays: adminAnalyticsWindowDays}
	err := repository.tx.QueryRow(ctx, `
		WITH bounds AS (
			SELECT CURRENT_TIMESTAMP AS generated_at,
				(date_trunc('day', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
					- ($2::integer - 1) * INTERVAL '1 day' AS window_start,
				(date_trunc('day', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
					+ INTERVAL '1 day' AS window_end
		), application_counts AS (
			SELECT count(*) FILTER (WHERE environment = 'production') AS production_count,
				count(*) FILTER (WHERE environment IN ('sandbox', 'test')) AS test_count
			FROM applications
			WHERE project_id = $1
		), entitlement_counts AS (
			SELECT count(*) AS active_count
			FROM customer_entitlements, bounds
			WHERE project_id = $1
				AND access_status = 'allowed'
				AND effective_starts_at <= bounds.generated_at
				AND (effective_ends_at IS NULL OR effective_ends_at > bounds.generated_at)
		), observations AS (
			SELECT observation.id, observation.access_status, observation.lifecycle_state
			FROM purchase_observations AS observation, bounds
			WHERE observation.project_id = $1
				AND observation.observed_at >= bounds.window_start
				AND observation.observed_at < bounds.window_end
		)
		SELECT bounds.generated_at,
			application_counts.production_count,
			application_counts.test_count,
			entitlement_counts.active_count,
			count(observations.id),
			count(observations.id) FILTER (WHERE observations.access_status = 'allowed'),
			count(observations.id) FILTER (WHERE observations.access_status = 'denied'),
			count(observations.id) FILTER (WHERE observations.access_status = 'unresolved'),
			count(observations.id) FILTER (WHERE observations.lifecycle_state IN ('refunded', 'revoked'))
		FROM bounds
		CROSS JOIN application_counts
		CROSS JOIN entitlement_counts
		LEFT JOIN observations ON true
		GROUP BY bounds.generated_at, application_counts.production_count,
			application_counts.test_count, entitlement_counts.active_count
	`, projectID, adminAnalyticsWindowDays).Scan(
		&analytics.GeneratedAt,
		&analytics.Revenue.ProductionApplicationCount,
		&analytics.Revenue.TestApplicationCount,
		&analytics.ActiveEntitlementCount,
		&analytics.VerifiedCount,
		&analytics.AllowedCount,
		&analytics.DeniedCount,
		&analytics.UnresolvedCount,
		&analytics.ReversedCount,
	)
	if err != nil {
		return persistence.AdminAnalytics{}, classifyError("load admin analytics", err)
	}

	zero := int64(0)
	switch {
	case analytics.Revenue.ProductionApplicationCount > 0:
		analytics.Revenue.Status = persistence.AdminRevenueStoreReportsRequired
	case analytics.Revenue.TestApplicationCount > 0:
		analytics.Revenue.Status = persistence.AdminRevenueSandboxOnly
		analytics.Revenue.RecognizedMinorUnits = &zero
	default:
		analytics.Revenue.Status = persistence.AdminRevenueNoApplications
		analytics.Revenue.RecognizedMinorUnits = &zero
	}

	sandboxValue, err := repository.adminSandboxValue(ctx, projectID)
	if err != nil {
		return persistence.AdminAnalytics{}, err
	}
	analytics.SandboxValue = sandboxValue

	dailyActivity, err := repository.adminDailyActivity(ctx, projectID)
	if err != nil {
		return persistence.AdminAnalytics{}, err
	}
	analytics.DailyActivity = dailyActivity
	return analytics, nil
}

// adminSandboxValue sums the latest non-reversed snapshot of each provider transaction by signed currency.
func (repository *transaction) adminSandboxValue(
	ctx context.Context,
	projectID core.ProjectID,
) (persistence.AdminSandboxValue, error) {
	rows, err := repository.tx.Query(ctx, `
		WITH bounds AS (
			SELECT (date_trunc('day', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
					- ($2::integer - 1) * INTERVAL '1 day' AS window_start,
				(date_trunc('day', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
					+ INTERVAL '1 day' AS window_end
		), latest_transactions AS (
			SELECT DISTINCT ON (observation.application_id, reference.value_fingerprint)
				observation.price_milliunits, observation.price_currency,
				observation.lifecycle_state, observation.ownership
			FROM purchase_observations AS observation
			JOIN applications AS application
				ON application.id = observation.application_id
				AND application.project_id = observation.project_id
			JOIN LATERAL (
				SELECT provider_reference.value_fingerprint
				FROM observation_references AS observation_reference
				JOIN provider_references AS provider_reference
					ON provider_reference.id = observation_reference.reference_id
					AND provider_reference.application_id = observation_reference.application_id
				WHERE observation_reference.observation_id = observation.id
					AND observation_reference.application_id = observation.application_id
					AND provider_reference.role = 'transaction'
				ORDER BY provider_reference.kind, provider_reference.value_fingerprint
				LIMIT 1
			) AS reference ON true
			CROSS JOIN bounds
			WHERE observation.project_id = $1
				AND application.environment IN ('sandbox', 'test')
				AND observation.occurred_at >= bounds.window_start
				AND observation.occurred_at < bounds.window_end
			ORDER BY observation.application_id, reference.value_fingerprint,
				observation.observed_at DESC, observation.created_at DESC, observation.id DESC
		), eligible_transactions AS (
			SELECT price_milliunits, price_currency
			FROM latest_transactions
			WHERE ownership = 'purchased'
				AND lifecycle_state NOT IN ('pending', 'refunded', 'revoked', 'unresolved')
		)
		SELECT price_currency, COALESCE(sum(price_milliunits), 0)::bigint, count(*)::bigint
		FROM eligible_transactions
		GROUP BY price_currency
		ORDER BY price_currency NULLS LAST
	`, projectID, adminAnalyticsWindowDays)
	if err != nil {
		return persistence.AdminSandboxValue{}, classifyError("load admin sandbox value", err)
	}
	defer rows.Close()

	value := persistence.AdminSandboxValue{Amounts: make([]persistence.AdminMoney, 0)}
	for rows.Next() {
		var currency *string
		var milliunits int64
		var count int64
		if err := rows.Scan(&currency, &milliunits, &count); err != nil {
			return persistence.AdminSandboxValue{}, classifyError("scan admin sandbox value", err)
		}
		if currency == nil {
			value.MissingPriceCount += count
			continue
		}
		value.TransactionCount += count
		value.Amounts = append(value.Amounts, persistence.AdminMoney{
			Milliunits: milliunits,
			Currency:   *currency,
		})
	}
	if err := rows.Err(); err != nil {
		return persistence.AdminSandboxValue{}, classifyError("iterate admin sandbox value", err)
	}
	return value, nil
}

// adminDailyActivity returns one zero-filled bucket for every UTC day in the analytics window.
func (repository *transaction) adminDailyActivity(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminDailyActivity, error) {
	rows, err := repository.tx.Query(ctx, `
		WITH bounds AS (
			SELECT (date_trunc('day', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
					- ($2::integer - 1) * INTERVAL '1 day' AS window_start,
				(date_trunc('day', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') AS window_end
		), days AS (
			SELECT generate_series(bounds.window_start, bounds.window_end, INTERVAL '1 day') AS day
			FROM bounds
		), activity AS (
			SELECT date_trunc('day', observation.observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS day,
				count(*) AS verified,
				count(*) FILTER (WHERE observation.access_status = 'allowed') AS allowed,
				count(*) FILTER (WHERE observation.access_status = 'denied') AS denied,
				count(*) FILTER (WHERE observation.access_status = 'unresolved') AS unresolved,
				count(*) FILTER (WHERE observation.lifecycle_state IN ('refunded', 'revoked')) AS reversed
			FROM purchase_observations AS observation, bounds
			WHERE observation.project_id = $1
				AND observation.observed_at >= bounds.window_start
				AND observation.observed_at < bounds.window_end + INTERVAL '1 day'
			GROUP BY day
		)
		SELECT to_char(days.day AT TIME ZONE 'UTC', 'YYYY-MM-DD'),
			COALESCE(activity.verified, 0), COALESCE(activity.allowed, 0),
			COALESCE(activity.denied, 0), COALESCE(activity.unresolved, 0),
			COALESCE(activity.reversed, 0)
		FROM days
		LEFT JOIN activity ON activity.day = days.day
		ORDER BY days.day
	`, projectID, adminAnalyticsWindowDays)
	if err != nil {
		return nil, classifyError("list admin daily activity", err)
	}
	defer rows.Close()

	activity := make([]persistence.AdminDailyActivity, 0, adminAnalyticsWindowDays)
	for rows.Next() {
		var day persistence.AdminDailyActivity
		if err := rows.Scan(
			&day.Date, &day.Verified, &day.Allowed, &day.Denied, &day.Unresolved, &day.Reversed,
		); err != nil {
			return nil, classifyError("scan admin daily activity", err)
		}
		activity = append(activity, day)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin daily activity", err)
	}
	return activity, nil
}

// adminApplications loads provider scope and safe configuration coverage.
func (repository *transaction) adminApplications(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminApplication, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT a.id, a.provider, a.environment, a.provider_application_id, a.created_at,
			COALESCE((
				SELECT max(credential.revision) FROM application_credentials AS credential
				WHERE credential.application_id = a.id
			), 0),
			COALESCE((
				SELECT webhook.revision FROM webhook_endpoints AS webhook
				WHERE webhook.application_id = a.id
			), 0)
		FROM applications AS a
		WHERE a.project_id = $1
		ORDER BY a.id
		LIMIT $2
	`, projectID, adminCollectionLimit)
	if err != nil {
		return nil, classifyError("list admin applications", err)
	}
	defer rows.Close()

	applications := make([]persistence.AdminApplication, 0)
	for rows.Next() {
		var application persistence.AdminApplication
		if err := rows.Scan(
			&application.ID,
			&application.Provider,
			&application.Environment,
			&application.ProviderApplicationID,
			&application.CreatedAt,
			&application.CredentialRevision,
			&application.WebhookRevision,
		); err != nil {
			return nil, classifyError("scan admin application", err)
		}
		application.CredentialConfigured = application.CredentialRevision > 0
		application.WebhookConfigured = application.WebhookRevision > 0
		applications = append(applications, application)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin applications", err)
	}
	return applications, nil
}

// adminProducts loads product grants and provider mapping coverage.
func (repository *transaction) adminProducts(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminProduct, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT product.id, product.kind, product.created_at,
			COALESCE(array_agg(DISTINCT entitlement.key ORDER BY entitlement.key)
				FILTER (WHERE entitlement.key IS NOT NULL), ARRAY[]::text[]),
			count(DISTINCT (mapping.application_id, mapping.provider_product_id))
		FROM products AS product
		LEFT JOIN product_entitlements AS entitlement_grant
			ON entitlement_grant.project_id = product.project_id AND entitlement_grant.product_id = product.id
		LEFT JOIN entitlements AS entitlement
			ON entitlement.project_id = entitlement_grant.project_id
			AND entitlement.id = entitlement_grant.entitlement_id
		LEFT JOIN store_products AS mapping
			ON mapping.project_id = product.project_id AND mapping.product_id = product.id
		WHERE product.project_id = $1
		GROUP BY product.id, product.kind, product.created_at
		ORDER BY product.id
		LIMIT $2
	`, projectID, adminCollectionLimit)
	if err != nil {
		return nil, classifyError("list admin products", err)
	}
	defer rows.Close()

	products := make([]persistence.AdminProduct, 0)
	for rows.Next() {
		var product persistence.AdminProduct
		if err := rows.Scan(
			&product.ID,
			&product.Kind,
			&product.CreatedAt,
			&product.EntitlementKeys,
			&product.StoreMappingCount,
		); err != nil {
			return nil, classifyError("scan admin product", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin products", err)
	}
	return products, nil
}

// adminCustomers loads safe customer identities and current access counts.
func (repository *transaction) adminCustomers(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminCustomer, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT customer.id, customer.external_id, customer.created_at,
			count(DISTINCT projection.entitlement_id),
			count(DISTINCT projection.entitlement_id) FILTER (WHERE projection.access_status = 'allowed'),
			max(observation.observed_at)
		FROM customers AS customer
		LEFT JOIN customer_entitlements AS projection
			ON projection.project_id = customer.project_id AND projection.customer_id = customer.id
		LEFT JOIN purchase_observations AS observation
			ON observation.project_id = customer.project_id AND observation.customer_id = customer.id
		WHERE customer.project_id = $1
		GROUP BY customer.id, customer.external_id, customer.created_at
		ORDER BY customer.created_at DESC, customer.id
		LIMIT $2
	`, projectID, adminCollectionLimit)
	if err != nil {
		return nil, classifyError("list admin customers", err)
	}
	defer rows.Close()

	customers := make([]persistence.AdminCustomer, 0)
	for rows.Next() {
		var customer persistence.AdminCustomer
		if err := rows.Scan(
			&customer.ID,
			&customer.ExternalID,
			&customer.CreatedAt,
			&customer.EntitlementCount,
			&customer.AllowedCount,
			&customer.LastObservedAt,
		); err != nil {
			return nil, classifyError("scan admin customer", err)
		}
		customers = append(customers, customer)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin customers", err)
	}
	return customers, nil
}

// adminTransactions loads recent normalized observations without purchase payloads or references.
func (repository *transaction) adminTransactions(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminTransaction, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, application_id, customer_id, product_id, provider_product_id,
			lifecycle_state, access_status, access_reason, observed_at, effective_ends_at
		FROM purchase_observations
		WHERE project_id = $1
		ORDER BY observed_at DESC, id DESC
		LIMIT $2
	`, projectID, adminActivityLimit)
	if err != nil {
		return nil, classifyError("list admin transactions", err)
	}
	defer rows.Close()

	transactions := make([]persistence.AdminTransaction, 0)
	for rows.Next() {
		var transaction persistence.AdminTransaction
		if err := rows.Scan(
			&transaction.ID,
			&transaction.ApplicationID,
			&transaction.CustomerID,
			&transaction.ProductID,
			&transaction.ProviderProductID,
			&transaction.LifecycleState,
			&transaction.Access,
			&transaction.AccessReason,
			&transaction.ObservedAt,
			&transaction.EffectiveEndsAt,
		); err != nil {
			return nil, classifyError("scan admin transaction", err)
		}
		transactions = append(transactions, transaction)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin transactions", err)
	}
	return transactions, nil
}

// adminQueues loads aggregate outcomes for each fixed durable queue.
func (repository *transaction) adminQueues(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminQueue, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT 'inbox',
			count(*) FILTER (WHERE processed_at IS NULL AND failed_at IS NULL),
			count(*) FILTER (WHERE processed_at IS NOT NULL),
			count(*) FILTER (WHERE failed_at IS NOT NULL)
		FROM inbox_messages WHERE project_id = $1
		UNION ALL
		SELECT 'reconciliation',
			count(*) FILTER (WHERE processed_at IS NULL AND failed_at IS NULL),
			count(*) FILTER (WHERE processed_at IS NOT NULL),
			count(*) FILTER (WHERE failed_at IS NOT NULL)
		FROM reconciliation_jobs WHERE project_id = $1
		UNION ALL
		SELECT 'outbox',
			count(*) FILTER (WHERE delivered_at IS NULL AND failed_at IS NULL),
			count(*) FILTER (WHERE delivered_at IS NOT NULL),
			count(*) FILTER (WHERE failed_at IS NOT NULL)
		FROM outbox_events WHERE project_id = $1
	`, projectID)
	if err != nil {
		return nil, classifyError("load admin queues", err)
	}
	defer rows.Close()

	queues := make([]persistence.AdminQueue, 0, 3)
	for rows.Next() {
		var queue persistence.AdminQueue
		if err := rows.Scan(&queue.Name, &queue.Pending, &queue.Completed, &queue.Failed); err != nil {
			return nil, classifyError("scan admin queue", err)
		}
		queues = append(queues, queue)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin queues", err)
	}
	return queues, nil
}

// adminWebhookEvents loads recent delivery metadata without returning event payloads.
func (repository *transaction) adminWebhookEvents(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminWebhookEvent, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, application_id, event_type, aggregate_id,
			CASE
				WHEN delivered_at IS NOT NULL THEN 'delivered'
				WHEN failed_at IS NOT NULL THEN 'failed'
				ELSE 'pending'
			END,
			COALESCE(last_error_code, ''), occurred_at, delivered_at, failed_at
		FROM outbox_events
		WHERE project_id = $1
		ORDER BY occurred_at DESC, id DESC
		LIMIT $2
	`, projectID, adminActivityLimit)
	if err != nil {
		return nil, classifyError("list admin webhook events", err)
	}
	defer rows.Close()

	events := make([]persistence.AdminWebhookEvent, 0)
	for rows.Next() {
		var event persistence.AdminWebhookEvent
		if err := rows.Scan(
			&event.ID,
			&event.ApplicationID,
			&event.EventType,
			&event.AggregateID,
			&event.Status,
			&event.ErrorCode,
			&event.OccurredAt,
			&event.DeliveredAt,
			&event.FailedAt,
		); err != nil {
			return nil, classifyError("scan admin webhook event", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin webhook events", err)
	}
	return events, nil
}
