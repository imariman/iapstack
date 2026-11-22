package postgres_test

import (
	"bytes"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// TestAdminReadsReportMissingWebhookAsSkipped verifies an event closed for a missing webhook is
// counted and shown as skipped, never as a failed delivery.
func TestAdminReadsReportMissingWebhookAsSkipped(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)

	insert := func(id string, fingerprint byte, version int, outcome string) {
		t.Helper()
		mustExec(t, database, `
			INSERT INTO outbox_events (
				id, project_id, application_id, event_type, aggregate_type, aggregate_id,
				payload, payload_fingerprint, occurred_at, available_at,
				delivered_at, failed_at, last_error_code
			) VALUES (
				$1, $2, $3, 'entitlement.changed', 'customer', $4,
				jsonb_build_object('entitlement_id', $5::text, 'entitlement_key', 'premium',
					'access', 'allowed', 'access_reason', 'purchase_valid', 'version', $6::int),
				$7, now(), now(),
				CASE WHEN $8 = 'delivered' THEN now() END,
				CASE WHEN $8 <> 'delivered' THEN now() END,
				CASE $8 WHEN 'failed' THEN 'webhook_rejected' WHEN 'skipped' THEN $9 END
			)
		`, id, fixture.projectID, fixture.applicationID, fixture.customerID, fixture.entitlementID, version,
			bytes.Repeat([]byte{fingerprint}, 32), outcome, persistence.WebhookNotConfiguredCode)
	}
	insert("outbox-delivered", 1, 1, "delivered")
	insert("outbox-failed", 2, 2, "failed")
	insert("outbox-skipped", 3, 3, "skipped")

	overview, err := store.AdminProjectOverview(database.ctx, projectID, persistence.AdminRevenueQuery{})
	if err != nil {
		t.Fatalf("AdminProjectOverview() error = %v", err)
	}
	var outbox persistence.AdminQueue
	for _, queue := range overview.Queues {
		if queue.Name == persistence.QueueOutbox {
			outbox = queue
		} else if queue.Skipped != 0 {
			t.Fatalf("queue %s skipped = %d, want 0 outside the outbox", queue.Name, queue.Skipped)
		}
	}
	if outbox.Pending != 0 || outbox.Completed != 1 || outbox.Failed != 1 || outbox.Skipped != 1 {
		t.Fatalf("outbox queue = %#v, want one delivered, one failed, one skipped", outbox)
	}
	statuses := map[string]string{}
	for _, event := range overview.RecentWebhookEvents {
		statuses[event.ID] = event.Status
	}
	if statuses["outbox-delivered"] != "delivered" || statuses["outbox-failed"] != "failed" ||
		statuses["outbox-skipped"] != persistence.AdminDeliverySkipped {
		t.Fatalf("webhook event statuses = %#v", statuses)
	}

	detail, err := store.AdminCustomerDetail(database.ctx, projectID, core.CustomerID(fixture.customerID))
	if err != nil {
		t.Fatalf("AdminCustomerDetail() error = %v", err)
	}
	changes := map[int64]string{}
	failedDeliveries := map[string]bool{}
	for _, event := range detail.Timeline {
		switch event.Kind {
		case persistence.AdminTimelineEntitlementChanged:
			changes[event.Version] = event.Status
		case persistence.AdminTimelineWebhookFailed:
			failedDeliveries[event.SourceID] = true
		}
	}
	if changes[1] != "delivered" || changes[2] != "failed" || changes[3] != persistence.AdminDeliverySkipped {
		t.Fatalf("timeline entitlement change statuses = %#v", changes)
	}
	if !failedDeliveries["outbox-failed"] || failedDeliveries["outbox-skipped"] {
		t.Fatalf("timeline failed deliveries = %#v, want only the rejected one", failedDeliveries)
	}
}
