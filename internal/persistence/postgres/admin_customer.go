package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// adminSearchPageSQL adds secret-free access counts to one already bounded page of customer ids.
	adminSearchPageSQL = `
		SELECT page.rank, page.match, customer.id, customer.external_id, customer.merged_into_customer_id,
			customer.created_at, access.entitlement_count, access.allowed_count, observed.last_observed_at,
			aliases.alias_count
		FROM page
		JOIN customers AS customer ON customer.project_id = $1 AND customer.id = page.customer_id
		CROSS JOIN LATERAL (
			SELECT count(*) AS entitlement_count,
				count(*) FILTER (WHERE projection.access_status = 'allowed') AS allowed_count
			FROM customer_entitlements AS projection
			WHERE projection.project_id = customer.project_id AND projection.customer_id = customer.id
		) AS access
		CROSS JOIN LATERAL (
			SELECT max(observation.observed_at) AS last_observed_at
			FROM purchase_observations AS observation
			WHERE observation.project_id = customer.project_id AND observation.customer_id = customer.id
		) AS observed
		CROSS JOIN LATERAL (
			SELECT count(*) AS alias_count
			FROM customer_aliases AS alias
			WHERE alias.project_id = customer.project_id AND alias.canonical_customer_id = customer.id
		) AS aliases
		ORDER BY page.rank, customer.created_at DESC, customer.id
	`
	// adminSearchKeysetSQL keeps rows strictly after the cursor ($2 rank, $3 created_at, $4 id)
	// in rank, newest-first, id order.
	adminSearchKeysetSQL = `(
		$2::integer IS NULL
		OR rank > $2
		OR (rank = $2 AND (created_at < $3 OR (created_at = $3 AND customer_id > $4)))
	)`
)

// adminTimeline merges bounded per-source events into one newest-first list.
type adminTimeline struct {
	events    []persistence.AdminTimelineEvent
	delivered map[string]struct{}
	truncated bool
}

var (
	// adminCustomerStoreContract verifies that Store exposes the customer support read model.
	_ persistence.AdminCustomerStore = (*Store)(nil)
)

// AdminSearchCustomers returns one page of project customers matching untrusted search text.
// The text is only ever a bound parameter. Provider references are matched by keyed fingerprint.
func (store *Store) AdminSearchCustomers(
	ctx context.Context,
	search persistence.AdminCustomerSearch,
) (persistence.AdminCustomerSearchPage, error) {
	if err := validateAdminCustomerSearch(&search); err != nil {
		return persistence.AdminCustomerSearchPage{}, err
	}
	var page persistence.AdminCustomerSearchPage
	err := store.transact(ctx, pgx.RepeatableRead, func(repository *transaction) error {
		var loadErr error
		page, loadErr = repository.adminSearchCustomers(ctx, search)
		return loadErr
	})
	return page, err
}

// AdminCustomerDetail returns identity, current access, grants, and a bounded timeline for one customer closure.
func (store *Store) AdminCustomerDetail(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (persistence.AdminCustomerDetail, error) {
	if err := errors.Join(projectID.Validate(), customerID.Validate()); err != nil {
		return persistence.AdminCustomerDetail{}, validation.Wrap(err)
	}
	var detail persistence.AdminCustomerDetail
	err := store.transact(ctx, pgx.RepeatableRead, func(repository *transaction) error {
		var loadErr error
		detail, loadErr = repository.adminCustomerDetail(ctx, projectID, customerID)
		return loadErr
	})
	return detail, err
}

// validateAdminCustomerSearch bounds untrusted search input and fills the default page size.
func validateAdminCustomerSearch(search *persistence.AdminCustomerSearch) error {
	if err := search.ProjectID.Validate(); err != nil {
		return validation.Wrap(err)
	}
	if len(search.Query) > persistence.AdminCustomerSearchMaxQueryBytes {
		return validation.Wrap(errors.New("search text is too long"))
	}
	if search.Query != "" {
		if err := core.ValidateOpaqueIdentifier("search text", search.Query); err != nil {
			return validation.Wrap(err)
		}
	}
	if search.Limit == 0 {
		search.Limit = persistence.AdminCustomerSearchDefaultLimit
	}
	if search.Limit < 1 || search.Limit > persistence.AdminCustomerSearchMaxLimit {
		return validation.Wrap(errors.New("search limit is out of range"))
	}
	if search.After != nil {
		if search.After.Rank < 0 || search.After.CreatedAt.IsZero() || search.After.CustomerID.Validate() != nil {
			return validation.Wrap(errors.New("search page token is invalid"))
		}
	}
	return nil
}

// adminSearchCustomers runs a listing or a bounded identifier match inside one read snapshot.
func (repository *transaction) adminSearchCustomers(
	ctx context.Context,
	search persistence.AdminCustomerSearch,
) (persistence.AdminCustomerSearchPage, error) {
	var projectFound bool
	if err := repository.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1)`, search.ProjectID).
		Scan(&projectFound); err != nil {
		return persistence.AdminCustomerSearchPage{}, classifyError("load admin search project", err)
	}
	if !projectFound {
		return persistence.AdminCustomerSearchPage{}, fmt.Errorf("load admin search project: %w", persistence.ErrNotFound)
	}

	var cursorRank, cursorCreatedAt, cursorID any
	if search.After != nil {
		cursorRank = search.After.Rank
		cursorCreatedAt = normalizeTime(search.After.CreatedAt)
		cursorID = string(search.After.CustomerID)
	}
	if search.Query == "" {
		rows, err := repository.tx.Query(ctx, `
			WITH page AS (
				SELECT rank, match, customer_id, created_at
				FROM (
					SELECT 0 AS rank, ''::text AS match, id AS customer_id, created_at
					FROM customers
					WHERE project_id = $1
				) AS listed
				WHERE `+adminSearchKeysetSQL+`
				ORDER BY created_at DESC, customer_id
				LIMIT $5
			)
		`+adminSearchPageSQL, search.ProjectID, cursorRank, cursorCreatedAt, cursorID, search.Limit+1)
		if err != nil {
			return persistence.AdminCustomerSearchPage{}, classifyError("list admin customers page", err)
		}
		return scanAdminSearchPage(rows, search.Limit)
	}

	lookups, err := repository.adminReferenceLookups(ctx, search)
	if err != nil {
		return persistence.AdminCustomerSearchPage{}, err
	}
	applicationIDs := make([]string, 0, len(lookups))
	roles := make([]string, 0, len(lookups))
	kinds := make([]string, 0, len(lookups))
	fingerprints := make([][]byte, 0, len(lookups))
	for _, lookup := range lookups {
		fingerprint := lookup.Fingerprint
		applicationIDs = append(applicationIDs, string(lookup.ApplicationID))
		roles = append(roles, string(lookup.Role))
		kinds = append(kinds, lookup.Kind)
		fingerprints = append(fingerprints, fingerprint[:])
	}

	rows, err := repository.tx.Query(ctx, `
		WITH reference_matches AS (
			SELECT DISTINCT observation.customer_id,
				CASE WHEN reference.role = 'transaction' THEN 'transaction_id' ELSE 'original_transaction_id' END AS match,
				CASE WHEN reference.role = 'transaction' THEN 4 ELSE 5 END AS rank
			FROM unnest($8::text[], $9::text[], $10::text[], $11::bytea[])
				AS lookup(application_id, role, kind, fingerprint)
			JOIN applications AS application
				ON application.id = lookup.application_id AND application.project_id = $1
			JOIN provider_references AS reference
				ON reference.application_id = lookup.application_id
				AND reference.role = lookup.role
				AND reference.kind = lookup.kind
				AND reference.value_fingerprint = lookup.fingerprint
				AND reference.role IN ('transaction', 'lineage')
			JOIN observation_references AS link
				ON link.reference_id = reference.id AND link.application_id = reference.application_id
			JOIN purchase_observations AS observation
				ON observation.id = link.observation_id AND observation.application_id = link.application_id
			WHERE observation.project_id = $1
		), matches AS (
			SELECT id AS customer_id, 'external_id'::text AS match, 1 AS rank
			FROM customers WHERE project_id = $1 AND external_id = $5
			UNION ALL
			SELECT id, 'customer_id', 2 FROM customers WHERE project_id = $1 AND id = $5
			UNION ALL
			SELECT canonical_customer_id, 'alias', 3
			FROM customer_aliases WHERE project_id = $1 AND external_id = $5
			UNION ALL
			SELECT customer_id, match, rank FROM reference_matches
			UNION ALL
			SELECT id, 'external_id_prefix', 6
			FROM customers WHERE project_id = $1 AND external_id LIKE $6 ESCAPE '\' AND external_id <> $5
			UNION ALL
			SELECT id, 'customer_id_prefix', 7
			FROM customers WHERE project_id = $1 AND id LIKE $6 ESCAPE '\' AND id <> $5
		), best AS (
			SELECT DISTINCT ON (matches.customer_id)
				matches.customer_id, matches.match, matches.rank, customer.created_at
			FROM matches
			JOIN customers AS customer ON customer.project_id = $1 AND customer.id = matches.customer_id
			ORDER BY matches.customer_id, matches.rank
		), page AS (
			SELECT rank, match, customer_id, created_at
			FROM best
			WHERE `+adminSearchKeysetSQL+`
			ORDER BY rank, created_at DESC, customer_id
			LIMIT $7
		)
	`+adminSearchPageSQL,
		search.ProjectID, cursorRank, cursorCreatedAt, cursorID,
		search.Query, escapeLikePrefix(search.Query)+"%", search.Limit+1,
		applicationIDs, roles, kinds, fingerprints,
	)
	if err != nil {
		return persistence.AdminCustomerSearchPage{}, classifyError("search admin customers", err)
	}
	return scanAdminSearchPage(rows, search.Limit)
}

// adminReferenceLookups asks the fingerprinter for the search text in every project application scope.
func (repository *transaction) adminReferenceLookups(
	ctx context.Context,
	search persistence.AdminCustomerSearch,
) ([]persistence.AdminReferenceFingerprint, error) {
	if search.References == nil {
		return nil, nil
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT id FROM applications WHERE project_id = $1 ORDER BY id LIMIT $2
	`, search.ProjectID, adminCollectionLimit)
	if err != nil {
		return nil, classifyError("list admin search applications", err)
	}
	applicationIDs := make([]core.ApplicationID, 0)
	for rows.Next() {
		var applicationID core.ApplicationID
		if err := rows.Scan(&applicationID); err != nil {
			rows.Close()
			return nil, classifyError("scan admin search application", err)
		}
		applicationIDs = append(applicationIDs, applicationID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin search applications", err)
	}
	if len(applicationIDs) == 0 {
		return nil, nil
	}
	lookups, err := search.References.ReferenceFingerprints(ctx, search.ProjectID, applicationIDs)
	if err != nil {
		return nil, fmt.Errorf("fingerprint admin search text: %w", err)
	}
	return lookups, nil
}

// scanAdminSearchPage reads up to limit results and derives the next keyset cursor.
func scanAdminSearchPage(rows pgx.Rows, limit int) (persistence.AdminCustomerSearchPage, error) {
	defer rows.Close()
	page := persistence.AdminCustomerSearchPage{Results: make([]persistence.AdminCustomerSearchResult, 0)}
	for rows.Next() {
		var result persistence.AdminCustomerSearchResult
		var match string
		if err := rows.Scan(
			&result.Rank,
			&match,
			&result.Customer.ID,
			&result.Customer.ExternalID,
			&result.Customer.MergedIntoCustomerID,
			&result.Customer.CreatedAt,
			&result.Customer.EntitlementCount,
			&result.Customer.AllowedCount,
			&result.Customer.LastObservedAt,
			&result.Customer.AliasCount,
		); err != nil {
			return persistence.AdminCustomerSearchPage{}, classifyError("scan admin customer search result", err)
		}
		result.Match = persistence.AdminCustomerMatch(match)
		page.Results = append(page.Results, result)
	}
	if err := rows.Err(); err != nil {
		return persistence.AdminCustomerSearchPage{}, classifyError("iterate admin customer search results", err)
	}
	if len(page.Results) > limit {
		page.Results = page.Results[:limit]
		last := page.Results[len(page.Results)-1]
		page.Next = &persistence.AdminCustomerSearchCursor{
			Rank: last.Rank, CreatedAt: last.Customer.CreatedAt, CustomerID: last.Customer.ID,
		}
	}
	return page, nil
}

// escapeLikePrefix escapes LIKE metacharacters so untrusted text only ever matches literally.
func escapeLikePrefix(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// adminCustomerDetail loads one customer closure and every bounded support dataset in one snapshot.
func (repository *transaction) adminCustomerDetail(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (persistence.AdminCustomerDetail, error) {
	detail := persistence.AdminCustomerDetail{}
	if err := repository.tx.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&detail.GeneratedAt); err != nil {
		return persistence.AdminCustomerDetail{}, classifyError("load admin customer clock", err)
	}
	requested, err := repository.adminCustomerIdentity(ctx, projectID, customerID)
	if err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	detail.Customer = requested
	detail.CanonicalCustomerID = requested.ID
	if requested.MergedIntoCustomerID != nil {
		canonical, err := repository.adminCustomerIdentity(ctx, projectID, *requested.MergedIntoCustomerID)
		if err != nil {
			return persistence.AdminCustomerDetail{}, err
		}
		if canonical.MergedIntoCustomerID != nil {
			return persistence.AdminCustomerDetail{}, fmt.Errorf("resolve canonical customer: %w", persistence.ErrConflict)
		}
		detail.CanonicalCustomerID = canonical.ID
	}
	detail.Members, err = repository.adminCustomerMembers(ctx, projectID, detail.CanonicalCustomerID)
	if err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	members := make([]string, 0, len(detail.Members))
	for _, member := range detail.Members {
		members = append(members, string(member.ID))
	}
	if detail.Aliases, err = repository.adminCustomerAliases(ctx, projectID, members); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	if detail.Merges, err = repository.adminCustomerMerges(ctx, projectID, members); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	if detail.Entitlements, err = repository.adminCustomerEntitlements(ctx, projectID, detail.CanonicalCustomerID); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	if detail.ManualGrants, err = repository.adminCustomerManualGrants(ctx, projectID, members); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	if detail.GrantOptions, err = repository.adminGrantOptions(ctx, projectID); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	timeline := newAdminTimeline()
	timeline.addIdentity(detail)
	timeline.addManualGrants(detail.ManualGrants)
	if err := repository.addAdminObservationEvents(ctx, projectID, members, timeline); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	if err := repository.addAdminImportEvents(ctx, projectID, members, timeline); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	if err := repository.addAdminOutboxEvents(ctx, projectID, members, timeline); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	timeline.addUndeliveredProjections(detail.CanonicalCustomerID, detail.Entitlements)
	if err := repository.addAdminReconciliationEvents(ctx, projectID, members, timeline); err != nil {
		return persistence.AdminCustomerDetail{}, err
	}
	detail.Timeline, detail.TimelineTruncated = timeline.sorted()
	return detail, nil
}

// adminCustomerIdentity loads one customer row in project scope.
func (repository *transaction) adminCustomerIdentity(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (persistence.AdminCustomerIdentity, error) {
	var identity persistence.AdminCustomerIdentity
	err := repository.tx.QueryRow(ctx, `
		SELECT id, external_id, merged_into_customer_id, created_at
		FROM customers
		WHERE project_id = $1 AND id = $2
	`, projectID, customerID).Scan(
		&identity.ID, &identity.ExternalID, &identity.MergedIntoCustomerID, &identity.CreatedAt,
	)
	if err != nil {
		return persistence.AdminCustomerIdentity{}, classifyError("load admin customer", err)
	}
	return identity, nil
}

// adminCustomerMembers loads the canonical customer and every customer merged directly into it.
func (repository *transaction) adminCustomerMembers(
	ctx context.Context,
	projectID core.ProjectID,
	canonicalID core.CustomerID,
) ([]persistence.AdminCustomerIdentity, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, external_id, merged_into_customer_id, created_at
		FROM customers
		WHERE project_id = $1 AND (id = $2 OR merged_into_customer_id = $2)
		ORDER BY (id = $2) DESC, created_at, id
		LIMIT $3
	`, projectID, canonicalID, persistence.AdminCustomerTimelineSourceLimit)
	if err != nil {
		return nil, classifyError("list admin customer closure", err)
	}
	defer rows.Close()
	members := make([]persistence.AdminCustomerIdentity, 0, 1)
	for rows.Next() {
		var member persistence.AdminCustomerIdentity
		if err := rows.Scan(&member.ID, &member.ExternalID, &member.MergedIntoCustomerID, &member.CreatedAt); err != nil {
			return nil, classifyError("scan admin customer closure", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin customer closure", err)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("list admin customer closure: %w", persistence.ErrNotFound)
	}
	return members, nil
}

// adminCustomerAliases loads alias audit rows owned by the closure.
func (repository *transaction) adminCustomerAliases(
	ctx context.Context,
	projectID core.ProjectID,
	members []string,
) ([]persistence.AdminCustomerAlias, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT external_id, canonical_customer_id, actor_role, actor_key_id, created_at
		FROM customer_aliases
		WHERE project_id = $1 AND canonical_customer_id = ANY($2)
		ORDER BY created_at DESC, external_id
		LIMIT $3
	`, projectID, members, persistence.AdminCustomerTimelineSourceLimit)
	if err != nil {
		return nil, classifyError("list admin customer aliases", err)
	}
	defer rows.Close()
	aliases := make([]persistence.AdminCustomerAlias, 0)
	for rows.Next() {
		var alias persistence.AdminCustomerAlias
		if err := rows.Scan(
			&alias.ExternalID, &alias.CanonicalCustomerID, &alias.ActorRole, &alias.ActorKeyID, &alias.CreatedAt,
		); err != nil {
			return nil, classifyError("scan admin customer alias", err)
		}
		aliases = append(aliases, alias)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin customer aliases", err)
	}
	return aliases, nil
}

// adminCustomerMerges loads merge audit rows in which a closure member is either side.
func (repository *transaction) adminCustomerMerges(
	ctx context.Context,
	projectID core.ProjectID,
	members []string,
) ([]persistence.AdminCustomerMergeRecord, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT merge.id, merge.canonical_customer_id, merge.source_customer_id, source.external_id,
			merge.actor_key_id, merge.created_at
		FROM customer_merges AS merge
		JOIN customers AS source
			ON source.project_id = merge.project_id AND source.id = merge.source_customer_id
		WHERE merge.project_id = $1
			AND (merge.canonical_customer_id = ANY($2) OR merge.source_customer_id = ANY($2))
		ORDER BY merge.created_at DESC, merge.id
		LIMIT $3
	`, projectID, members, persistence.AdminCustomerTimelineSourceLimit)
	if err != nil {
		return nil, classifyError("list admin customer merges", err)
	}
	defer rows.Close()
	merges := make([]persistence.AdminCustomerMergeRecord, 0)
	for rows.Next() {
		var merge persistence.AdminCustomerMergeRecord
		if err := rows.Scan(
			&merge.ID, &merge.CanonicalCustomerID, &merge.SourceCustomerID, &merge.SourceExternalID,
			&merge.ActorKeyID, &merge.CreatedAt,
		); err != nil {
			return nil, classifyError("scan admin customer merge", err)
		}
		merges = append(merges, merge)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin customer merges", err)
	}
	return merges, nil
}

// adminCustomerEntitlements loads every current projection of the canonical customer with its cited source.
func (repository *transaction) adminCustomerEntitlements(
	ctx context.Context,
	projectID core.ProjectID,
	canonicalID core.CustomerID,
) ([]persistence.AdminCustomerEntitlement, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT projection.entitlement_id, entitlement.key, projection.source_kind,
			COALESCE(projection.source_observation_id, projection.source_import_id, projection.source_manual_grant_id),
			COALESCE(observation.application_id, import.application_id, manual.application_id, ''),
			projection.source_product_id, projection.source_customer_id,
			projection.access_status, projection.access_reason,
			projection.effective_starts_at, projection.effective_ends_at,
			projection.version, projection.updated_at
		FROM customer_entitlements AS projection
		JOIN entitlements AS entitlement
			ON entitlement.id = projection.entitlement_id AND entitlement.project_id = projection.project_id
		LEFT JOIN purchase_observations AS observation
			ON projection.source_kind = 'store_observation' AND observation.id = projection.source_observation_id
		LEFT JOIN import_grants AS import
			ON projection.source_kind = 'import' AND import.id = projection.source_import_id
		LEFT JOIN manual_grants AS manual
			ON projection.source_kind = 'manual' AND manual.id = projection.source_manual_grant_id
		WHERE projection.project_id = $1 AND projection.customer_id = $2
		ORDER BY entitlement.key
	`, projectID, canonicalID)
	if err != nil {
		return nil, classifyError("list admin customer entitlements", err)
	}
	defer rows.Close()
	entitlements := make([]persistence.AdminCustomerEntitlement, 0)
	for rows.Next() {
		var entitlement persistence.AdminCustomerEntitlement
		var startsAt, endsAt pgtype.Timestamptz
		if err := rows.Scan(
			&entitlement.EntitlementID, &entitlement.Key, &entitlement.SourceKind, &entitlement.SourceID,
			&entitlement.SourceApplicationID, &entitlement.SourceProductID, &entitlement.SourceCustomerID,
			&entitlement.Access, &entitlement.AccessReason, &startsAt, &endsAt,
			&entitlement.Version, &entitlement.UpdatedAt,
		); err != nil {
			return nil, classifyError("scan admin customer entitlement", err)
		}
		entitlement.EffectivePeriod = core.EffectivePeriod{
			StartsAt: timestampValue(startsAt), EndsAt: timestampPointer(endsAt),
		}
		entitlements = append(entitlements, entitlement)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin customer entitlements", err)
	}
	return entitlements, nil
}

// adminCustomerManualGrants loads the newest operator grants of the closure.
func (repository *transaction) adminCustomerManualGrants(
	ctx context.Context,
	projectID core.ProjectID,
	members []string,
) ([]persistence.ManualGrant, error) {
	rows, err := repository.tx.Query(ctx, manualGrantSelectSQL+`
		WHERE mg.project_id = $1 AND mg.customer_id = ANY($2)
		ORDER BY mg.created_at DESC, mg.id
		LIMIT $3
	`, projectID, members, persistence.AdminCustomerTimelineSourceLimit)
	if err != nil {
		return nil, classifyError("list admin customer manual grants", err)
	}
	defer rows.Close()
	grants := make([]persistence.ManualGrant, 0)
	for rows.Next() {
		grant, err := scanManualGrantRow(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin customer manual grants", err)
	}
	return grants, nil
}

// adminGrantOptions loads the non-consumable product and entitlement pairs a manual grant may cite.
func (repository *transaction) adminGrantOptions(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminGrantOption, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT mapping.entitlement_id, entitlement.key, mapping.product_id, product.kind,
			COALESCE(
				array_agg(DISTINCT store_product.application_id ORDER BY store_product.application_id)
					FILTER (WHERE store_product.application_id IS NOT NULL),
				ARRAY[]::text[]
			)
		FROM product_entitlements AS mapping
		JOIN entitlements AS entitlement
			ON entitlement.id = mapping.entitlement_id AND entitlement.project_id = mapping.project_id
		JOIN products AS product
			ON product.id = mapping.product_id AND product.project_id = mapping.project_id
		LEFT JOIN store_products AS store_product
			ON store_product.project_id = mapping.project_id AND store_product.product_id = mapping.product_id
		WHERE mapping.project_id = $1 AND product.kind <> 'consumable'
		GROUP BY mapping.entitlement_id, entitlement.key, mapping.product_id, product.kind
		ORDER BY entitlement.key, mapping.product_id
		LIMIT $2
	`, projectID, adminCollectionLimit)
	if err != nil {
		return nil, classifyError("list admin grant options", err)
	}
	defer rows.Close()
	options := make([]persistence.AdminGrantOption, 0)
	for rows.Next() {
		var option persistence.AdminGrantOption
		if err := rows.Scan(
			&option.EntitlementID, &option.EntitlementKey, &option.ProductID, &option.ProductKind, &option.ApplicationIDs,
		); err != nil {
			return nil, classifyError("scan admin grant option", err)
		}
		options = append(options, option)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate admin grant options", err)
	}
	return options, nil
}

// addAdminObservationEvents adds verified store observations with each lineage's previous lifecycle state.
func (repository *transaction) addAdminObservationEvents(
	ctx context.Context,
	projectID core.ProjectID,
	members []string,
	timeline *adminTimeline,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, customer_id, application_id, product_id, provider_product_id, lifecycle_state,
			COALESCE(previous_state, ''), access_status, access_reason, observed_at, effective_ends_at
		FROM (
			SELECT po.id, po.customer_id, po.application_id, po.product_id, po.provider_product_id,
				po.lifecycle_state, po.access_status, po.access_reason, po.observed_at, po.effective_ends_at,
				lag(po.lifecycle_state) OVER (
					PARTITION BY po.application_id, po.product_id, lineage.source_lineage
					ORDER BY po.observed_at, po.id
				) AS previous_state
			FROM purchase_observations AS po
			CROSS JOIN LATERAL (SELECT `+observationLineageSQL+` AS source_lineage) AS lineage
			WHERE po.project_id = $1 AND po.customer_id = ANY($2)
		) AS observations
		ORDER BY observed_at DESC, id DESC
		LIMIT $3
	`, projectID, members, persistence.AdminCustomerTimelineSourceLimit)
	if err != nil {
		return classifyError("list admin customer observations", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		event := persistence.AdminTimelineEvent{Kind: persistence.AdminTimelinePurchaseObserved,
			SourceKind: persistence.SourceKindStoreObservation}
		var endsAt pgtype.Timestamptz
		if err := rows.Scan(
			&event.SourceID, &event.CustomerID, &event.ApplicationID, &event.ProductID, &event.ProviderProductID,
			&event.LifecycleState, &event.PreviousLifecycleState, &event.Access, &event.AccessReason,
			&event.OccurredAt, &endsAt,
		); err != nil {
			return classifyError("scan admin customer observation", err)
		}
		event.EffectiveEndsAt = timestampPointer(endsAt)
		timeline.add(event)
		count++
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate admin customer observations", err)
	}
	timeline.noteSource(count)
	return nil
}

// addAdminImportEvents adds historical import grants. They are never shown as store observations.
func (repository *transaction) addAdminImportEvents(
	ctx context.Context,
	projectID core.ProjectID,
	members []string,
	timeline *adminTimeline,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT grant_row.id, grant_row.customer_id, grant_row.application_id, grant_row.product_id,
			grant_row.entitlement_id, entitlement.key, grant_row.origin_store, grant_row.access_status,
			grant_row.access_reason, grant_row.effective_ends_at, grant_row.created_at
		FROM import_grants AS grant_row
		JOIN entitlements AS entitlement
			ON entitlement.id = grant_row.entitlement_id AND entitlement.project_id = grant_row.project_id
		WHERE grant_row.project_id = $1 AND grant_row.customer_id = ANY($2)
		ORDER BY grant_row.created_at DESC, grant_row.id
		LIMIT $3
	`, projectID, members, persistence.AdminCustomerTimelineSourceLimit)
	if err != nil {
		return classifyError("list admin customer import grants", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		event := persistence.AdminTimelineEvent{Kind: persistence.AdminTimelineImportGrantRecorded,
			SourceKind: persistence.SourceKindImport}
		var endsAt pgtype.Timestamptz
		if err := rows.Scan(
			&event.SourceID, &event.CustomerID, &event.ApplicationID, &event.ProductID, &event.EntitlementID,
			&event.EntitlementKey, &event.OriginStore, &event.Access, &event.AccessReason, &endsAt, &event.OccurredAt,
		); err != nil {
			return classifyError("scan admin customer import grant", err)
		}
		event.EffectiveEndsAt = timestampPointer(endsAt)
		timeline.add(event)
		count++
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate admin customer import grants", err)
	}
	timeline.noteSource(count)
	return nil
}

// addAdminOutboxEvents adds entitlement changes and their webhook delivery outcomes without payload bodies.
func (repository *transaction) addAdminOutboxEvents(
	ctx context.Context,
	projectID core.ProjectID,
	members []string,
	timeline *adminTimeline,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, aggregate_id, application_id, occurred_at, delivered_at, failed_at,
			COALESCE(last_error_code, ''),
			COALESCE(payload->>'entitlement_id', ''),
			COALESCE(payload->>'entitlement_key', ''),
			COALESCE(payload->>'access', ''),
			COALESCE(payload->>'access_reason', ''),
			COALESCE(NULLIF(payload->>'source', ''), 'store_observation'),
			COALESCE(payload->>'source_observation_id', payload->>'source_import_id',
				payload->>'source_manual_grant_id', ''),
			COALESCE(payload->>'source_product_id', ''),
			CASE WHEN payload->>'version' ~ '^[0-9]{1,18}$' THEN (payload->>'version')::bigint ELSE 0 END
		FROM outbox_events
		WHERE project_id = $1
			AND aggregate_type = 'customer'
			AND aggregate_id = ANY($2)
			AND event_type = 'entitlement.changed'
		ORDER BY occurred_at DESC, id DESC
		LIMIT $3
	`, projectID, members, persistence.AdminCustomerTimelineSourceLimit)
	if err != nil {
		return classifyError("list admin customer webhook events", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var outboxID, customerID, applicationID, errorCode, entitlementID, key, access, reason, source, sourceID, productID string
		var occurredAt time.Time
		var deliveredAt, failedAt pgtype.Timestamptz
		var version int64
		if err := rows.Scan(
			&outboxID, &customerID, &applicationID, &occurredAt, &deliveredAt, &failedAt, &errorCode,
			&entitlementID, &key, &access, &reason, &source, &sourceID, &productID, &version,
		); err != nil {
			return classifyError("scan admin customer webhook event", err)
		}
		status := "pending"
		if deliveredAt.Valid {
			status = "delivered"
		} else if failedAt.Valid {
			status = "failed"
		}
		change := persistence.AdminTimelineEvent{
			Kind: persistence.AdminTimelineEntitlementChanged, OccurredAt: occurredAt,
			CustomerID: core.CustomerID(customerID), SourceID: sourceID, SourceKind: persistence.SourceKind(source),
			ApplicationID: core.ApplicationID(applicationID), ProductID: core.ProductID(productID),
			EntitlementID: core.EntitlementID(entitlementID), EntitlementKey: key,
			Access: core.AccessStatus(access), AccessReason: core.AccessReason(reason),
			Version: version, Status: status,
		}
		timeline.add(change)
		timeline.noteDelivery(change.EntitlementID, version)
		delivery := persistence.AdminTimelineEvent{
			CustomerID: change.CustomerID, SourceID: outboxID, ApplicationID: change.ApplicationID,
			EntitlementID: change.EntitlementID, EntitlementKey: key, Version: version, Status: status,
		}
		if deliveredAt.Valid {
			delivery.Kind, delivery.OccurredAt = persistence.AdminTimelineWebhookDelivered, deliveredAt.Time
			timeline.add(delivery)
		} else if failedAt.Valid {
			delivery.Kind, delivery.OccurredAt, delivery.ErrorCode = persistence.AdminTimelineWebhookFailed, failedAt.Time, errorCode
			timeline.add(delivery)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate admin customer webhook events", err)
	}
	timeline.noteSource(count)
	return nil
}

// addAdminReconciliationEvents adds durable reconciliation job outcomes as audit events.
func (repository *transaction) addAdminReconciliationEvents(
	ctx context.Context,
	projectID core.ProjectID,
	members []string,
	timeline *adminTimeline,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, customer_id, application_id, created_at, processed_at, failed_at, COALESCE(last_error_code, '')
		FROM reconciliation_jobs
		WHERE project_id = $1 AND customer_id = ANY($2)
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, projectID, members, persistence.AdminCustomerTimelineSourceLimit)
	if err != nil {
		return classifyError("list admin customer reconciliation jobs", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		event := persistence.AdminTimelineEvent{Kind: persistence.AdminTimelineReconciliation}
		var createdAt time.Time
		var processedAt, failedAt pgtype.Timestamptz
		if err := rows.Scan(
			&event.SourceID, &event.CustomerID, &event.ApplicationID, &createdAt, &processedAt, &failedAt, &event.ErrorCode,
		); err != nil {
			return classifyError("scan admin customer reconciliation job", err)
		}
		switch {
		case processedAt.Valid:
			event.Status, event.OccurredAt = "processed", processedAt.Time
		case failedAt.Valid:
			event.Status, event.OccurredAt = "failed", failedAt.Time
		default:
			event.Status, event.OccurredAt = "pending", createdAt
		}
		timeline.add(event)
		count++
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate admin customer reconciliation jobs", err)
	}
	timeline.noteSource(count)
	return nil
}

// newAdminTimeline returns an empty timeline accumulator.
func newAdminTimeline() *adminTimeline {
	return &adminTimeline{delivered: make(map[string]struct{})}
}

// add appends one event.
func (timeline *adminTimeline) add(event persistence.AdminTimelineEvent) {
	timeline.events = append(timeline.events, event)
}

// noteSource marks the timeline truncated when one source returned a full bounded page.
func (timeline *adminTimeline) noteSource(count int) {
	if count >= persistence.AdminCustomerTimelineSourceLimit {
		timeline.truncated = true
	}
}

// noteDelivery records that an outbox event exists for one projection version.
func (timeline *adminTimeline) noteDelivery(entitlementID core.EntitlementID, version int64) {
	timeline.delivered[fmt.Sprintf("%s\x00%d", entitlementID, version)] = struct{}{}
}

// addIdentity adds customer creation, alias, and merge audit rows.
func (timeline *adminTimeline) addIdentity(detail persistence.AdminCustomerDetail) {
	for _, member := range detail.Members {
		timeline.add(persistence.AdminTimelineEvent{
			Kind: persistence.AdminTimelineCustomerCreated, OccurredAt: member.CreatedAt,
			CustomerID: member.ID, SourceID: string(member.ID), ExternalID: member.ExternalID,
		})
	}
	for _, alias := range detail.Aliases {
		timeline.add(persistence.AdminTimelineEvent{
			Kind: persistence.AdminTimelineAliasAdded, OccurredAt: alias.CreatedAt,
			CustomerID: alias.CanonicalCustomerID, ExternalID: alias.ExternalID,
			ActorRole: alias.ActorRole, ActorKeyID: alias.ActorKeyID,
		})
	}
	timeline.noteSource(len(detail.Aliases))
	for _, merge := range detail.Merges {
		timeline.add(persistence.AdminTimelineEvent{
			Kind: persistence.AdminTimelineCustomerMerged, OccurredAt: merge.CreatedAt,
			CustomerID: merge.CanonicalCustomerID, SourceID: merge.ID,
			ExternalID: merge.SourceExternalID, ActorKeyID: merge.ActorKeyID,
			RelatedCustomerID: merge.SourceCustomerID,
		})
	}
	timeline.noteSource(len(detail.Merges))
}

// addManualGrants adds the creation and any early revocation of each operator grant.
func (timeline *adminTimeline) addManualGrants(grants []persistence.ManualGrant) {
	for _, grant := range grants {
		endsAt := grant.EndsAt
		timeline.add(persistence.AdminTimelineEvent{
			Kind: persistence.AdminTimelineManualGrantCreated, OccurredAt: grant.CreatedAt,
			CustomerID: grant.CustomerID, SourceID: grant.ID, SourceKind: persistence.SourceKindManual,
			ApplicationID: grant.ApplicationID, ProductID: grant.ProductID,
			EntitlementID: grant.EntitlementID, EntitlementKey: grant.EntitlementKey,
			Access: core.AccessAllowed, AccessReason: core.AccessReasonManualGrant,
			EffectiveEndsAt: &endsAt, ActorKeyID: grant.ActorKeyID, Reason: grant.Reason,
		})
		if grant.RevokedAt != nil {
			timeline.add(persistence.AdminTimelineEvent{
				Kind: persistence.AdminTimelineManualGrantRevoked, OccurredAt: *grant.RevokedAt,
				CustomerID: grant.CustomerID, SourceID: grant.ID, SourceKind: persistence.SourceKindManual,
				ApplicationID: grant.ApplicationID, ProductID: grant.ProductID,
				EntitlementID: grant.EntitlementID, EntitlementKey: grant.EntitlementKey,
				Access: core.AccessDenied, AccessReason: core.AccessReasonRevoked,
				ActorKeyID: grant.RevokedByKeyID, Reason: grant.RevocationReason,
			})
		}
	}
	timeline.noteSource(len(grants))
}

// addUndeliveredProjections adds the current projection change when no outbox row records that version.
func (timeline *adminTimeline) addUndeliveredProjections(
	canonicalID core.CustomerID,
	entitlements []persistence.AdminCustomerEntitlement,
) {
	for _, entitlement := range entitlements {
		if _, exists := timeline.delivered[fmt.Sprintf("%s\x00%d", entitlement.EntitlementID, entitlement.Version)]; exists {
			continue
		}
		timeline.add(persistence.AdminTimelineEvent{
			Kind: persistence.AdminTimelineEntitlementChanged, OccurredAt: entitlement.UpdatedAt,
			CustomerID: canonicalID, SourceID: entitlement.SourceID, SourceKind: entitlement.SourceKind,
			ApplicationID: entitlement.SourceApplicationID, ProductID: entitlement.SourceProductID,
			EntitlementID: entitlement.EntitlementID, EntitlementKey: entitlement.Key,
			Access: entitlement.Access, AccessReason: entitlement.AccessReason,
			EffectiveEndsAt: entitlement.EffectivePeriod.EndsAt, Version: entitlement.Version,
		})
	}
}

// sorted returns newest-first events capped at the timeline limit and whether anything was left out.
func (timeline *adminTimeline) sorted() ([]persistence.AdminTimelineEvent, bool) {
	sort.SliceStable(timeline.events, func(left, right int) bool {
		a, b := timeline.events[left], timeline.events[right]
		if !a.OccurredAt.Equal(b.OccurredAt) {
			return a.OccurredAt.After(b.OccurredAt)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.SourceID < b.SourceID
	})
	truncated := timeline.truncated
	if len(timeline.events) > persistence.AdminCustomerTimelineLimit {
		timeline.events = timeline.events[:persistence.AdminCustomerTimelineLimit]
		truncated = true
	}
	return timeline.events, truncated
}
