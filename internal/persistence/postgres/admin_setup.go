package postgres

import (
	"context"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
)

var (
	// adminSetupStoreContract verifies that Store exposes the application setup read model.
	_ persistence.AdminSetupStore = (*Store)(nil)
)

// AdminApplicationSetup returns credential presence and webhook configuration for the first applications
// of one project, in the same bound and order as the project overview. It never reads credential
// payloads, ciphertext, fingerprints, or webhook secrets.
func (store *Store) AdminApplicationSetup(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminApplicationSetup, error) {
	if err := projectID.Validate(); err != nil {
		return nil, err
	}
	var setups []persistence.AdminApplicationSetup
	err := store.transact(ctx, pgx.RepeatableRead, func(repository *transaction) error {
		var loadErr error
		setups, loadErr = repository.adminApplicationSetup(ctx, projectID)
		return loadErr
	})
	return setups, err
}

// adminApplicationSetup loads bounded application setup rows inside one snapshot.
func (repository *transaction) adminApplicationSetup(
	ctx context.Context,
	projectID core.ProjectID,
) ([]persistence.AdminApplicationSetup, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT a.id, credential.kind, credential.revision, credential.updated_at
		FROM (
			SELECT id FROM applications WHERE project_id = $1 ORDER BY id LIMIT $2
		) AS a
		LEFT JOIN application_credentials AS credential
			ON credential.application_id = a.id AND credential.project_id = $1
		ORDER BY a.id, credential.kind
	`, projectID, adminCollectionLimit)
	if err != nil {
		return nil, classifyError("list admin application credentials", err)
	}
	setups := make([]persistence.AdminApplicationSetup, 0)
	positions := make(map[core.ApplicationID]int)
	for rows.Next() {
		var applicationID core.ApplicationID
		var kind *string
		var credential persistence.AdminCredentialStatus
		var revision *int64
		var updatedAt *time.Time
		if err := rows.Scan(&applicationID, &kind, &revision, &updatedAt); err != nil {
			rows.Close()
			return nil, classifyError("scan admin application credential", err)
		}
		position, exists := positions[applicationID]
		if !exists {
			position = len(setups)
			positions[applicationID] = position
			setups = append(setups, persistence.AdminApplicationSetup{
				ApplicationID: applicationID, Credentials: []persistence.AdminCredentialStatus{},
			})
		}
		if kind != nil && revision != nil && updatedAt != nil {
			credential.Kind = *kind
			credential.Revision = *revision
			credential.UpdatedAt = *updatedAt
			setups[position].Credentials = append(setups[position].Credentials, credential)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, classifyError("iterate admin application credentials", err)
	}
	rows.Close()

	webhooks, err := repository.tx.Query(ctx, `
		SELECT application_id, url, revision, updated_at
		FROM webhook_endpoints
		WHERE project_id = $1
	`, projectID)
	if err != nil {
		return nil, classifyError("list admin application webhooks", err)
	}
	defer webhooks.Close()
	for webhooks.Next() {
		var applicationID core.ApplicationID
		var webhook persistence.AdminWebhookSetup
		if err := webhooks.Scan(&applicationID, &webhook.URL, &webhook.Revision, &webhook.UpdatedAt); err != nil {
			return nil, classifyError("scan admin application webhook", err)
		}
		if position, exists := positions[applicationID]; exists {
			setups[position].Webhook = &webhook
		}
	}
	if err := webhooks.Err(); err != nil {
		return nil, classifyError("iterate admin application webhooks", err)
	}
	return setups, nil
}
