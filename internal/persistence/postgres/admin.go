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
		Project: project, Applications: applications, Products: products,
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

// adminApplications loads provider scope and safe configuration coverage.
func (repository *transaction) adminApplications(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminApplication, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT a.id, a.provider, a.environment, a.provider_application_id, a.created_at,
			CASE WHEN a.provider = 'huawei_appgallery' THEN COALESCE((
				SELECT credential.revision FROM application_credentials AS credential
				WHERE credential.application_id = a.id AND credential.kind = 'huawei_server_api'
			), 0) ELSE 0 END,
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
