package apple

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// historyLineage is the original transaction of the upgraded subscription in history tests.
	historyLineage = "2000000933456789"
	// historyMonthly is the product a history test upgrades from.
	historyMonthly = "iapstack.pro.monthly"
	// historyAnnual is the product a history test upgrades to first.
	historyAnnual = "iapstack.pro.annual"
	// historyPremium is the product a history test upgrades to second.
	historyPremium = "iapstack.premium.annual"
	// historyLegacy is an older product of the same lineage that history tests modify late.
	historyLegacy = "iapstack.legacy.monthly"
)

// historyScenario scripts the subscription status and the transaction history pages Apple returns.
type historyScenario struct {
	now     time.Time
	current transactionPayload
	pages   [][]string
	endless bool
	status  int
	block   bool
	queries []url.Values
}

// TestUpgradeHistoryScansPastOlderModifiedTransactions does not stop at the first older lineage entry.
// Apple sorts history by last-modified date, so an older transaction refunded after the upgrade can
// precede the upgraded predecessor; the predecessor on a later page must still be closed.
func TestUpgradeHistoryScansPastOlderModifiedTransactions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	current := historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, 365*24*time.Hour, false)
	legacy := historyTransaction(now, "2000000933456790", historyLegacy, 400*24*time.Hour, -370*24*time.Hour, true)
	legacy.RevocationDate = now.Add(-time.Hour).UnixMilli()
	monthly := historyTransaction(now, "2000000933456794", historyMonthly, 10*24*time.Hour, 20*24*time.Hour, true)
	scenario := &historyScenario{now: now, current: current, pages: [][]string{
		{signTransactionFixture(t, fixture, current), signTransactionFixture(t, fixture, legacy)},
		{signTransactionFixture(t, fixture, monthly)},
	}}

	result, err := verifyHistoryScenario(t, fixture, scenario, context.Background())
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(scenario.queries) != 2 {
		t.Fatalf("history requests = %d, want both pages", len(scenario.queries))
	}
	if len(result.Observations) != 2 {
		t.Fatalf("Verify() observations = %#v, want current and closed monthly", result.Observations)
	}
	assertSupersededAppleProduct(t, result.Observations[1], historyMonthly, milliseconds(current.PurchaseDate))
}

// TestUpgradeHistoryClosesConsecutiveProductChanges closes every product an immediate change replaced
// inside the window, not only the latest one: monthly -> annual -> premium within 48 hours.
func TestUpgradeHistoryClosesConsecutiveProductChanges(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	premium := historyTransaction(now, "2000000933456797", historyPremium, time.Minute, 365*24*time.Hour, false)
	annual := historyTransaction(now, "2000000933456796", historyAnnual, time.Hour, 365*24*time.Hour, true)
	monthly := historyTransaction(now, "2000000933456794", historyMonthly, 10*24*time.Hour, 20*24*time.Hour, true)
	renewed := historyTransaction(now, "2000000933456793", historyMonthly, 40*24*time.Hour, -10*24*time.Hour, false)
	scenario := &historyScenario{now: now, current: premium, pages: [][]string{{
		signTransactionFixture(t, fixture, premium), signTransactionFixture(t, fixture, annual),
		signTransactionFixture(t, fixture, monthly), signTransactionFixture(t, fixture, renewed),
	}}}

	result, err := verifyHistoryScenario(t, fixture, scenario, context.Background())
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(result.Observations) != 3 {
		t.Fatalf("Verify() observations = %#v, want premium, closed annual, and closed monthly", result.Observations)
	}
	assertSupersededAppleProduct(t, result.Observations[1], historyAnnual, milliseconds(premium.PurchaseDate))
	assertSupersededAppleProduct(t, result.Observations[2], historyMonthly, milliseconds(annual.PurchaseDate))
	if err := result.ValidateForVerification(stores.VerificationRequest{
		Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
		ClaimedProducts:          result.ProductsForClaim([]core.ProviderProductID{historyPremium}),
		ExpectedCustomerBindings: []core.StoreReference{historyBinding(t)},
		Evidence:                 evidenceFixture(t, fixture, premium, core.ProductKindSubscription),
	}); err != nil {
		t.Fatalf("ValidateForVerification() error = %v", err)
	}
}

// TestUpgradeHistoryClosesOnlyBoundUpgradedPredecessors separates the closing gates.
// A different-product predecessor that Apple did not mark upgraded (a resubscribe or a downgrade),
// and upgraded entries from another lineage, another customer, or with an invalid signature, close nothing.
func TestUpgradeHistoryClosesOnlyBoundUpgradedPredecessors(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		entry func(signingFixture, transactionPayload) string
	}{
		{name: "different product not upgraded", entry: func(fixture signingFixture, monthly transactionPayload) string {
			monthly.IsUpgraded = false
			return signTransactionFixture(t, fixture, monthly)
		}},
		{name: "same product upgraded", entry: func(fixture signingFixture, monthly transactionPayload) string {
			monthly.ProductID = historyAnnual
			return signTransactionFixture(t, fixture, monthly)
		}},
		{name: "other lineage", entry: func(fixture signingFixture, monthly transactionPayload) string {
			monthly.OriginalTransactionID = "2000000933400000"
			return signTransactionFixture(t, fixture, monthly)
		}},
		{name: "other customer", entry: func(fixture signingFixture, monthly transactionPayload) string {
			monthly.AppAccountToken = "8c4fdb36-8c9b-4a3d-a0a2-0f3f62c0f001"
			return signTransactionFixture(t, fixture, monthly)
		}},
		{name: "invalid signature", entry: func(fixture signingFixture, monthly transactionPayload) string {
			signed := signTransactionFixture(t, fixture, monthly)
			return signed[:len(signed)-4] + "AAAA"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newSigningFixture(t, now)
			current := historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, 365*24*time.Hour, false)
			monthly := historyTransaction(now, "2000000933456794", historyMonthly, 10*24*time.Hour, 20*24*time.Hour, true)
			scenario := &historyScenario{now: now, current: current, pages: [][]string{{
				signTransactionFixture(t, fixture, current), test.entry(fixture, monthly),
			}}}
			result, err := verifyHistoryScenario(t, fixture, scenario, context.Background())
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if len(scenario.queries) != 1 || len(result.Observations) != 1 {
				t.Fatalf("history requests = %d, observations = %#v, want one lookup and no closed product",
					len(scenario.queries), result.Observations)
			}
		})
	}
}

// TestUpgradeHistoryLookupIsBounded makes no lookup for old or first transactions and caps pagination.
func TestUpgradeHistoryLookupIsBounded(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)

	old := historyTransaction(now, "2000000933456795", historyAnnual, 3*24*time.Hour, 365*24*time.Hour, false)
	scenario := &historyScenario{now: now, current: old}
	if result, err := verifyHistoryScenario(t, fixture, scenario, context.Background()); err != nil ||
		len(scenario.queries) != 0 || len(result.Observations) != 1 {
		t.Fatalf("Verify(older than window) = (%d observations, %v), history requests = %d, want no lookup",
			len(result.Observations), err, len(scenario.queries))
	}

	first := historyTransaction(now, historyLineage, historyAnnual, time.Minute, 365*24*time.Hour, false)
	scenario = &historyScenario{now: now, current: first}
	if _, err := verifyHistoryScenario(t, fixture, scenario, context.Background()); err != nil || len(scenario.queries) != 0 {
		t.Fatalf("Verify(first transaction) error = %v, history requests = %d, want no lookup", err, len(scenario.queries))
	}

	current := historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, 365*24*time.Hour, false)
	scenario = &historyScenario{now: now, current: current, endless: true}
	result, err := verifyHistoryScenario(t, fixture, scenario, context.Background())
	if err != nil {
		t.Fatalf("Verify(endless history) error = %v", err)
	}
	if len(scenario.queries) != maximumHistoryPages || len(result.Observations) != 1 {
		t.Fatalf("history requests = %d, observations = %d, want %d pages and the current product only",
			len(scenario.queries), len(result.Observations), maximumHistoryPages)
	}
}

// TestUpgradeHistorySkipsRenewals uses transactionReason to tell a renewal from an upgrade.
// Apple marks an upgrade PURCHASE and every renewal RENEWAL; without the field the lookup still runs.
func TestUpgradeHistorySkipsRenewals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	for _, test := range []struct {
		reason     string
		wantLookup bool
	}{
		{reason: transactionReasonRenewal},
		{reason: "PURCHASE", wantLookup: true},
		{reason: "", wantLookup: true},
	} {
		current := historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, 365*24*time.Hour, false)
		current.TransactionReason = test.reason
		scenario := &historyScenario{now: now, current: current}
		result, err := verifyHistoryScenario(t, fixture, scenario, context.Background())
		if err != nil {
			t.Fatalf("Verify(reason %q) error = %v", test.reason, err)
		}
		if lookedUp := len(scenario.queries) > 0; lookedUp != test.wantLookup || len(result.Observations) != 1 {
			t.Fatalf("Verify(reason %q) history requests = %d, want lookup %t", test.reason, len(scenario.queries), test.wantLookup)
		}
	}
}

// TestUpgradeHistoryReportsLookupOutcome reports every lookup, so a failing history call that leaves the
// verification result unchanged is still visible in provider call metrics.
func TestUpgradeHistoryReportsLookupOutcome(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	current := historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, 365*24*time.Hour, false)
	for _, test := range []struct {
		status   int
		wantKind stores.FailureKind
	}{
		{status: http.StatusOK},
		{status: http.StatusForbidden, wantKind: stores.FailureUnauthorized},
		{status: http.StatusTooManyRequests, wantKind: stores.FailureRateLimited},
	} {
		scenario := &historyScenario{now: now, current: current}
		if test.status != http.StatusOK {
			scenario.status = test.status
		}
		adapter := newHistoryAdapter(t, fixture, scenario, time.Second)
		var operations []string
		var outcomes []error
		adapter.ReportProviderCalls(func(operation string, err error) {
			operations = append(operations, operation)
			outcomes = append(outcomes, err)
		})
		result, err := adapter.Verify(context.Background(), stores.VerificationRequest{
			Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
			ClaimedProducts:          []core.ProviderProductID{historyAnnual},
			ExpectedCustomerBindings: []core.StoreReference{historyBinding(t)},
			Evidence:                 evidenceFixture(t, fixture, current, core.ProductKindSubscription),
		})
		if err != nil || len(result.Observations) != 1 {
			t.Fatalf("Verify(history %d) = (%d observations, %v), want the current product", test.status, len(result.Observations), err)
		}
		if len(operations) != 1 || operations[0] != transactionHistoryOperation {
			t.Fatalf("reported operations = %v, want one %q", operations, transactionHistoryOperation)
		}
		if test.wantKind == "" && outcomes[0] != nil {
			t.Fatalf("reported outcome = %v, want success", outcomes[0])
		}
		if test.wantKind != "" && !isFailureKind(outcomes[0], test.wantKind) {
			t.Fatalf("reported outcome = %v, want %q", outcomes[0], test.wantKind)
		}
	}
}

// TestAdapterClosesPreviousProductFromUpgradeNotification follows the inbox path of #250: the worker turns a
// DID_CHANGE_RENEWAL_PREF/UPGRADE notification into its signed transaction as evidence and claims the
// notification product, so the same verify that records the new product also closes the old one.
func TestAdapterClosesPreviousProductFromUpgradeNotification(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	current := historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, 365*24*time.Hour, false)
	current.TransactionReason = "PURCHASE"
	monthly := historyTransaction(now, "2000000933456794", historyMonthly, 10*24*time.Hour, 20*24*time.Hour, true)
	scenario := &historyScenario{now: now, current: current, pages: [][]string{{
		signTransactionFixture(t, fixture, current), signTransactionFixture(t, fixture, monthly),
	}}}
	adapter := newHistoryAdapter(t, fixture, scenario, time.Second)
	renewal := renewalPayload{
		AppAccountToken: current.AppAccountToken, AutoRenewProductID: current.ProductID,
		AutoRenewStatus: autoRenewEnabled, Environment: current.Environment,
		OriginalTransactionID: current.OriginalTransactionID, ProductID: current.ProductID,
		RenewalDate: current.ExpiresDate, SignedDate: now.UnixMilli(),
	}
	notification, err := adapter.ValidateNotification(
		context.Background(), appleApplication(core.EnvironmentSandbox),
		notificationRequestFixture(t, fixture, notificationPayload{
			NotificationType: "DID_CHANGE_RENEWAL_PREF", Subtype: "UPGRADE", NotificationUUID: fixtureNotificationUUID,
			Version: notificationVersion, SignedDate: now.UnixMilli(),
			Data: &notificationData{
				BundleID: fixtureBundleID, Environment: "Sandbox", Status: subscriptionStatusActive,
				SignedTransactionInfo: signTransactionFixture(t, fixture, current),
				SignedRenewalInfo:     signApplePayloadFixture(t, fixture, renewal),
			},
		}),
	)
	if err != nil {
		t.Fatalf("ValidateNotification() error = %v", err)
	}
	evidence, process, err := notification.VerificationEvidence()
	if err != nil || !process {
		t.Fatalf("VerificationEvidence() = (%t, %v)", process, err)
	}
	wrapped, err := stores.NewEvidence(EvidenceContentType, evidence)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	binding, err := core.NewStoreReference(core.ReferenceCustomerBinding, "app_account_token", notification.ExternalCustomerID)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	result, err := adapter.Verify(context.Background(), stores.VerificationRequest{
		Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
		ClaimedProducts:          []core.ProviderProductID{notification.ProviderProductID},
		ExpectedCustomerBindings: []core.StoreReference{binding},
		Evidence:                 wrapped,
	})
	if err != nil {
		t.Fatalf("Verify(notification evidence) error = %v", err)
	}
	if len(result.Observations) != 2 || result.Observations[0].ProductID != historyAnnual ||
		result.Observations[0].Access != core.AccessAllowed {
		t.Fatalf("Verify(notification evidence) observations = %#v, want allowed annual and closed monthly", result.Observations)
	}
	assertSupersededAppleProduct(t, result.Observations[1], historyMonthly, milliseconds(current.PurchaseDate))
}

// TestUpgradeHistoryHasItsOwnDeadline keeps a hanging history endpoint from consuming the caller's deadline.
func TestUpgradeHistoryHasItsOwnDeadline(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	current := historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, 365*24*time.Hour, false)
	scenario := &historyScenario{now: now, current: current, block: true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := time.Now()
	result, err := verifyHistoryScenario(t, fixture, scenario, ctx)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 1500*time.Millisecond || ctx.Err() != nil {
		t.Fatalf("Verify() took %s with caller context error %v, want the history budget to leave time to persist",
			elapsed, ctx.Err())
	}
	if len(result.Observations) != 1 {
		t.Fatalf("Verify() observations = %#v, want the current product only", result.Observations)
	}
}

// TestReconcileDoesNotAddHistoryObservations keeps reconciliation results inside its query-reference contract.
// Every observation must match the reconciliation query reference, which a history-discovered predecessor
// (its own transaction ID) cannot; verify paths (client, notification, daily replay) close the predecessor.
func TestReconcileDoesNotAddHistoryObservations(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	current := historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, 365*24*time.Hour, false)
	monthly := historyTransaction(now, "2000000933456794", historyMonthly, 10*24*time.Hour, 20*24*time.Hour, true)
	scenario := &historyScenario{now: now, current: current, pages: [][]string{{
		signTransactionFixture(t, fixture, current), signTransactionFixture(t, fixture, monthly),
	}}}
	adapter := newHistoryAdapter(t, fixture, scenario, time.Second)
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "transaction_id", current.TransactionID)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	request := stores.ReconciliationRequest{
		Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
		ExpectedProducts:         []core.ProviderProductID{historyAnnual},
		ExpectedProductKind:      core.ProductKindSubscription,
		ExpectedCustomerBindings: []core.StoreReference{historyBinding(t)},
		QueryReferences:          []core.StoreReference{queryReference},
	}
	result, err := adapter.Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	request.ExpectedProducts = result.ProductsForClaim(request.ExpectedProducts)
	if err := result.ValidateForReconciliation(request); err != nil {
		t.Fatalf("ValidateForReconciliation() error = %v", err)
	}
	if len(scenario.queries) != 0 || len(result.Observations) != 1 {
		t.Fatalf("history requests = %d, observations = %#v, want no lookup", len(scenario.queries), result.Observations)
	}
}

// TestUnverifiedOriginalTransactionIDOnlyReadsTheLineage pre-filters history entries before verification.
// It reads the lineage claim of a compact JWS and returns nothing for any malformed value.
func TestUnverifiedOriginalTransactionIDOnlyReadsTheLineage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	signed := signTransactionFixture(t, fixture, historyTransaction(now, "2000000933456795", historyAnnual, time.Minute, time.Hour, false))
	if got := unverifiedOriginalTransactionID(signed); got != historyLineage {
		t.Fatalf("unverifiedOriginalTransactionID() = %q, want %q", got, historyLineage)
	}
	for _, malformed := range []string{"", "a.b", "a.%%%.c", "a." + "bm90LWpzb24" + ".c"} {
		if got := unverifiedOriginalTransactionID(malformed); got != "" {
			t.Fatalf("unverifiedOriginalTransactionID(%q) = %q, want empty", malformed, got)
		}
	}
}

// historyTransaction builds one lineage transaction purchased before now that expires relative to now.
func historyTransaction(
	now time.Time,
	transactionID string,
	productID string,
	purchasedAgo time.Duration,
	expiresIn time.Duration,
	upgraded bool,
) transactionPayload {
	return transactionPayload{
		OriginalTransactionID: historyLineage, TransactionID: transactionID,
		BundleID: fixtureBundleID, ProductID: productID,
		PurchaseDate: now.Add(-purchasedAgo).UnixMilli(), ExpiresDate: now.Add(expiresIn).UnixMilli(),
		Quantity: 1, Type: appleSubscriptionType, AppAccountToken: fixtureAccountToken,
		OwnershipType: applePurchasedOwnership, SignedDate: now.UnixMilli(),
		Environment: "Sandbox", IsUpgraded: upgraded,
	}
}

// historyBinding returns the customer binding every history test transaction carries.
func historyBinding(t *testing.T) core.StoreReference {
	t.Helper()
	binding, err := core.NewStoreReference(core.ReferenceCustomerBinding, "app_account_token", fixtureAccountToken)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	return binding
}

// verifyHistoryScenario verifies the scenario's current transaction as submitted client evidence.
func verifyHistoryScenario(
	t *testing.T,
	fixture signingFixture,
	scenario *historyScenario,
	ctx context.Context,
) (stores.VerificationResult, error) {
	t.Helper()
	adapter := newHistoryAdapter(t, fixture, scenario, 10*time.Second)
	return adapter.Verify(ctx, stores.VerificationRequest{
		Application: appleApplication(core.EnvironmentSandbox), CustomerID: "customer-1",
		ClaimedProducts:          []core.ProviderProductID{core.ProviderProductID(scenario.current.ProductID)},
		ExpectedCustomerBindings: []core.StoreReference{historyBinding(t)},
		Evidence:                 evidenceFixture(t, fixture, scenario.current, core.ProductKindSubscription),
	})
}

// newHistoryAdapter serves the scenario's status, transaction, and history endpoints in memory.
func newHistoryAdapter(
	t *testing.T,
	fixture signingFixture,
	scenario *historyScenario,
	providerTimeout time.Duration,
) *Adapter {
	t.Helper()
	adapter, err := New(
		fakeCredentialSource{credential: credentialFixture(t, fixture, core.EnvironmentSandbox)},
		providerTimeout,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return scenario.now }
	current := scenario.current
	renewal := renewalPayload{
		AppAccountToken: current.AppAccountToken, AutoRenewProductID: current.ProductID,
		AutoRenewStatus: autoRenewEnabled, Environment: current.Environment,
		OriginalTransactionID: current.OriginalTransactionID, ProductID: current.ProductID,
		RenewalDate: current.ExpiresDate, SignedDate: scenario.now.UnixMilli(),
	}
	adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/inApps/v1/transactions/" + current.TransactionID:
			return jsonResponse(t, http.StatusOK, transactionInfoResponse{
				SignedTransactionInfo: signTransactionFixture(t, fixture, current),
			}, nil), nil
		case "/inApps/v1/subscriptions/" + current.OriginalTransactionID:
			return jsonResponse(t, http.StatusOK, statusResponse{
				Environment: current.Environment, BundleID: current.BundleID,
				Data: []subscriptionGroupIdentifierItem{{
					SubscriptionGroupIdentifier: "subscription-group-1",
					LastTransactions: []lastTransactionsItem{{
						OriginalTransactionID: current.OriginalTransactionID, Status: subscriptionStatusActive,
						SignedTransactionInfo: signTransactionFixture(t, fixture, current),
						SignedRenewalInfo:     signApplePayloadFixture(t, fixture, renewal),
					}},
				}},
			}, nil), nil
		case "/inApps/v2/history/" + current.OriginalTransactionID:
			return scenario.historyResponse(t, request)
		default:
			t.Errorf("unexpected provider request path %q", request.URL.Path)
			return jsonResponse(t, http.StatusNotFound, map[string]string{}, nil), nil
		}
	})
	return adapter
}

// historyResponse returns the scripted page named by the request's revision token.
func (scenario *historyScenario) historyResponse(t *testing.T, request *http.Request) (*http.Response, error) {
	t.Helper()
	query := request.URL.Query()
	scenario.queries = append(scenario.queries, query)
	if scenario.block {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}
	if scenario.status != 0 {
		return jsonResponse(t, scenario.status, map[string]string{}, nil), nil
	}
	page := 0
	if revision := query.Get("revision"); revision != "" {
		parsed, err := strconv.Atoi(strings.TrimPrefix(revision, "rev-"))
		if err != nil {
			t.Errorf("history revision = %q", revision)
		}
		page = parsed
	}
	var signed []string
	if page < len(scenario.pages) {
		signed = scenario.pages[page]
	}
	return jsonResponse(t, http.StatusOK, transactionHistoryResponse{
		Revision: "rev-" + strconv.Itoa(page+1), HasMore: scenario.endless || page+1 < len(scenario.pages),
		Environment: scenario.current.Environment, BundleID: scenario.current.BundleID,
		SignedTransactions: signed,
	}, nil), nil
}
