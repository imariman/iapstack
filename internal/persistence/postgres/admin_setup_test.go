package postgres_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// TestAdminProjectSetupReportsPresenceWithoutSecrets verifies the setup read returns applications, their
// credential kinds, revisions, and webhook URL, and queue outcomes, only for the requested project, and
// that the narrow project read and the setup read both report a missing project.
func TestAdminProjectSetupReportsPresenceWithoutSecrets(t *testing.T) {
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

	setup, err := store.AdminProjectSetup(database.ctx, core.ProjectID(fixture.projectID))
	if err != nil {
		t.Fatalf("AdminProjectSetup() error = %v", err)
	}
	setups := setup.ApplicationSetups
	if len(setups) != 2 || len(setup.Applications) != 2 {
		t.Fatalf("AdminProjectSetup() = %#v, want both applications", setup)
	}
	for index, application := range setup.Applications {
		if application.ID != setups[index].ApplicationID {
			t.Fatalf("application %d = %q, setup row = %q, want the same order", index, application.ID, setups[index].ApplicationID)
		}
	}
	if len(setup.Queues) == 0 || setup.RecentWebhookEvents == nil {
		t.Fatalf("AdminProjectSetup() queues = %#v, events = %#v, want queue outcomes and a delivery list",
			setup.Queues, setup.RecentWebhookEvents)
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

	other, err := store.AdminProjectSetup(database.ctx, "project-2")
	if err != nil || len(other.Applications) != 0 || len(other.ApplicationSetups) != 0 {
		t.Fatalf("AdminProjectSetup(project-2) = %#v, %v, want no applications", other, err)
	}
	if _, err := store.AdminProjectSetup(database.ctx, "missing-project"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("AdminProjectSetup(missing-project) error = %v, want ErrNotFound", err)
	}

	project, err := store.AdminProject(database.ctx, core.ProjectID(fixture.projectID))
	if err != nil || project.ID != core.ProjectID(fixture.projectID) || project.ApplicationCount != 2 {
		t.Fatalf("AdminProject() = %#v, %v, want the project with two applications", project, err)
	}
	if _, err := store.AdminProject(database.ctx, "missing-project"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("AdminProject(missing-project) error = %v, want ErrNotFound", err)
	}
}
