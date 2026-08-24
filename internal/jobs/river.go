// Package jobs defines durable payloads and identifiers shared by enqueueing and workers.
package jobs

const (
	// QueueInbox isolates provider notification processing capacity.
	QueueInbox = "iapstack_inbox"
	// QueueOutbox isolates outbound webhook delivery capacity.
	QueueOutbox = "iapstack_outbox"
	// QueueReconciliation isolates scheduled provider refresh capacity.
	QueueReconciliation = "iapstack_reconciliation"

	// KindInbox identifies one protected provider notification job.
	KindInbox = "iapstack_inbox"
	// KindOutbox identifies one immutable webhook delivery job.
	KindOutbox = "iapstack_outbox"
	// KindReconciliation identifies one protected lifecycle refresh job.
	KindReconciliation = "iapstack_reconciliation"
)

// InboxArgs identifies one protected inbox record without copying its payload into River.
type InboxArgs struct {
	MessageID string `json:"message_id"`
}

// OutboxArgs identifies one immutable outbox record without copying its payload into River.
type OutboxArgs struct {
	EventID string `json:"event_id"`
}

// ReconciliationArgs identifies one protected scheduled reconciliation record.
type ReconciliationArgs struct {
	JobID string `json:"job_id"`
}

// Kind returns the stable River job kind for inbox processing.
func (InboxArgs) Kind() string {
	return KindInbox
}

// Kind returns the stable River job kind for webhook delivery.
func (OutboxArgs) Kind() string {
	return KindOutbox
}

// Kind returns the stable River job kind for reconciliation processing.
func (ReconciliationArgs) Kind() string {
	return KindReconciliation
}
