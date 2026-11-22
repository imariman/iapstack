package postgres_test

import (
	"bytes"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// TestAdminApplicationSetupReportsPresenceWithoutSecrets verifies setup rows carry credential kinds,
// revisions, and the webhook URL, and only for applications of the requested project.
func TestAdminApplicationSetupReportsPresenceWithoutSecrets(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	putCredential(t, database.ctx, store, credentialWrite(fixture, "credential-sentinel-plaintext", 0))
	mustExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ('application-2', $1, 'google_play', 'production', 'com.example.second')
	`, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO webhook_endpoints (
			project_id, application_id, url, secret_ciphertext, secret_fingerprint, encryption_key_id
		) VALUES ($1, 'application-2', 'https://hooks.example.com/iapstack', $2, $3, 'key-1')
	`, fixture.projectID, []byte("webhook-ciphertext"), bytes.Repeat([]byte{7}, 32))
	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-2')`)

	setups, err := store.AdminApplicationSetup(database.ctx, core.ProjectID(fixture.projectID))
	if err != nil {
		t.Fatalf("AdminApplicationSetup() error = %v", err)
	}
	if len(setups) != 2 {
		t.Fatalf("AdminApplicationSetup() = %#v, want both applications", setups)
	}
	first, second := setups[0], setups[1]
	if first.ApplicationID != core.ApplicationID(fixture.applicationID) || len(first.Credentials) != 1 ||
		first.Credentials[0].Revision != 1 || first.Credentials[0].UpdatedAt.IsZero() || first.Webhook != nil {
		t.Fatalf("first application setup = %#v, want one stored credential and no webhook", first)
	}
	if second.ApplicationID != "application-2" || len(second.Credentials) != 0 || second.Webhook == nil ||
		second.Webhook.URL != "https://hooks.example.com/iapstack" || second.Webhook.Revision != 1 {
		t.Fatalf("second application setup = %#v, want only a webhook", second)
	}

	other, err := store.AdminApplicationSetup(database.ctx, "project-2")
	if err != nil || len(other) != 0 {
		t.Fatalf("AdminApplicationSetup(project-2) = %#v, %v, want no applications", other, err)
	}
}
