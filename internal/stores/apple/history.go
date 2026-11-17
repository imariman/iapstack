package apple

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// maximumHistoryPages bounds transaction-history pagination when looking for an upgraded predecessor.
	// Apple returns up to 20 signed transactions per page, sorted descending, so the transaction
	// immediately before the current one is on an early page; three pages is a generous bound.
	maximumHistoryPages = 3
	// upgradeHistoryRecency bounds how long after a transaction's purchase a history lookup can
	// still close a superseded product sooner than the pre-change transaction's daily replay.
	// Upgrade evidence (the StoreKit upgrade purchase, an UPGRADE notification, and the first
	// reconciliation that discovers the newer status transaction) arrives shortly after purchase,
	// and after this window the pre-change JWS replay has already closed the old product.
	upgradeHistoryRecency = 48 * time.Hour
	// historySortDescending requests Apple's newest-first transaction history order.
	historySortDescending = "DESCENDING"
)

// transactionHistoryResponse is Apple's Get Transaction History response contract.
type transactionHistoryResponse struct {
	Revision           string   `json:"revision"`
	HasMore            bool     `json:"hasMore"`
	BundleID           string   `json:"bundleId"`
	AppAppleID         uint64   `json:"appAppleId,omitempty"`
	Environment        string   `json:"environment"`
	SignedTransactions []string `json:"signedTransactions"`
}

// needsUpgradeHistory reports whether a same-product result could still hide an upgrade.
// No field on the current transaction, its status row, or its renewal info names the product
// it replaced: isUpgraded is set on the predecessor, not the current transaction, and a
// purchase later than the lineage's first purchase is true of every renewal. The narrowest
// adapter-local trigger is therefore a recent, non-first transaction in its lineage whose
// submitted or stored evidence already names the current product.
func needsUpgradeHistory(previous transactionPayload, current transactionPayload, now time.Time) bool {
	if previous.ProductID == "" || previous.ProductID != current.ProductID {
		return false
	}
	if current.TransactionID == "" || current.OriginalTransactionID == "" ||
		current.TransactionID == current.OriginalTransactionID || current.PurchaseDate <= 0 {
		return false
	}
	purchasedAt := milliseconds(current.PurchaseDate)
	if purchasedAt.After(now.Add(maximumNotificationClockSkew)) {
		return false
	}
	return now.Sub(purchasedAt) <= upgradeHistoryRecency
}

// queryUpgradedPredecessor finds the transaction immediately before the current one in its
// lineage when Apple marks that predecessor upgraded into a different product.
// History returns signed transactions for the whole customer, so every entry is verified
// through the same JWS path as submitted evidence and then bound to the current
// transaction's application, environment, lineage, and customer before it can close a product.
func (adapter *Adapter) queryUpgradedPredecessor(
	ctx context.Context,
	application core.Application,
	configuration configuration,
	current transactionPayload,
) (transactionPayload, bool, error) {
	token, err := adapter.authorizationToken(configuration)
	if err != nil {
		return transactionPayload{}, false,
			stores.NewFailure(adapter.Provider(), "token", stores.FailurePermanent, 0, err)
	}
	baseURL := adapter.productionURL
	if application.Store.Environment == core.EnvironmentSandbox {
		baseURL = adapter.sandboxURL
	}
	var predecessor *transactionPayload
	revision := ""
	for page := 0; page < maximumHistoryPages; page++ {
		endpoint := strings.TrimRight(baseURL, "/") + "/inApps/v2/history/" +
			url.PathEscape(current.OriginalTransactionID)
		query := url.Values{"sort": []string{historySortDescending}}
		if revision != "" {
			query.Set("revision", revision)
		}
		endpoint += "?" + query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return transactionPayload{}, false,
				stores.NewFailure(adapter.Provider(), "transaction_history", stores.FailurePermanent, 0, err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/json")
		body, status, responseHeader, err := adapter.do(request)
		if err != nil {
			return transactionPayload{}, false, err
		}
		if err := adapter.classifyProviderStatus("transaction_history", status, responseHeader); err != nil {
			return transactionPayload{}, false, err
		}
		var response transactionHistoryResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return transactionPayload{}, false,
				invalid("transaction_history", errors.New("apple transaction history response is invalid"))
		}
		if err := validateHistoryResponseScope(application, configuration, response); err != nil {
			return transactionPayload{}, false, invalid("transaction_history", err)
		}
		foundOlder := false
		for _, signed := range response.SignedTransactions {
			candidate, verifyErr := verifyTransactionJWS(signed, configuration.roots)
			if verifyErr != nil || !isLineagePredecessorCandidate(application, configuration, current, candidate) {
				continue
			}
			foundOlder = true
			if predecessor == nil || candidate.PurchaseDate > predecessor.PurchaseDate ||
				(candidate.PurchaseDate == predecessor.PurchaseDate && candidate.ExpiresDate > predecessor.ExpiresDate) {
				selected := candidate
				predecessor = &selected
			}
		}
		// Descending pages are contiguous from the newest transaction, so the first page
		// containing an older lineage transaction also contains the immediate predecessor.
		if foundOlder || !response.HasMore || response.Revision == "" || response.Revision == revision {
			break
		}
		revision = response.Revision
	}
	if predecessor == nil || !predecessor.IsUpgraded ||
		predecessor.ProductID == "" || predecessor.ProductID == current.ProductID {
		return transactionPayload{}, false, nil
	}
	return *predecessor, true, nil
}

// validateHistoryResponseScope checks App Store application and environment history fields.
func validateHistoryResponseScope(
	application core.Application,
	configuration configuration,
	response transactionHistoryResponse,
) error {
	environment, err := appleEnvironment(application.Store.Environment)
	if err != nil || response.Environment != environment || response.BundleID != configuration.bundleID {
		return errors.New("apple transaction history application scope mismatch")
	}
	if application.Store.Environment == core.EnvironmentProduction && response.AppAppleID != configuration.appAppleID {
		return errors.New("apple transaction history App Apple ID mismatch")
	}
	return nil
}

// isLineagePredecessorCandidate binds one verified history entry to the current lineage and customer.
func isLineagePredecessorCandidate(
	application core.Application,
	configuration configuration,
	current transactionPayload,
	candidate transactionPayload,
) bool {
	environment, err := appleEnvironment(application.Store.Environment)
	if err != nil {
		return false
	}
	return candidate.TransactionID != "" && candidate.TransactionID != current.TransactionID &&
		candidate.OriginalTransactionID == current.OriginalTransactionID &&
		candidate.BundleID == configuration.bundleID && candidate.BundleID == string(application.Store.ID) &&
		candidate.Environment == environment && candidate.Type == appleSubscriptionType &&
		candidate.AppAccountToken == current.AppAccountToken && candidate.AppAccountToken != "" &&
		candidate.PurchaseDate > 0 && candidate.PurchaseDate <= current.PurchaseDate
}
