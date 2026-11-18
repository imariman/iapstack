package apple

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// maximumHistoryPages bounds transaction-history pagination when looking for upgraded predecessors.
	// Apple returns up to 20 signed transactions per page. Pages are ordered by each record's last
	// modification, not its purchase, so every page up to this bound is read.
	maximumHistoryPages = 3
	// upgradeHistoryRecency bounds how long after a product change a history lookup can still close the
	// replaced product sooner than the pre-change transaction's daily replay. Upgrade evidence (the StoreKit
	// upgrade purchase and the UPGRADE notification) arrives shortly after purchase, and after this window
	// the pre-change JWS replay has already closed the old product.
	upgradeHistoryRecency = 48 * time.Hour
	// transactionHistoryTimeout bounds the whole optional lookup, every page included, so a slow history
	// endpoint cannot hold a verification that subscription status has already answered.
	transactionHistoryTimeout = 5 * time.Second
	// historySortDescending requests Apple's most recently modified records first.
	historySortDescending = "DESCENDING"
	// historyProductTypeAutoRenewable limits history to auto-renewable subscriptions, the only product type
	// an upgrade can replace, so pages carry fewer unrelated records.
	historyProductTypeAutoRenewable = "AUTO_RENEWABLE"
	// transactionReasonRenewal marks a transaction the App Store created to renew a subscription.
	// Apple marks an upgrade PURCHASE; a renewal, including a downgrade taking effect, replaces nothing early.
	transactionReasonRenewal = "RENEWAL"
	// transactionHistoryOperation names the optional lookup in provider failures and call metrics.
	transactionHistoryOperation = "transaction_history"
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

// upgradedPredecessor pairs one upgraded lineage transaction with the transaction that replaced it.
type upgradedPredecessor struct {
	previous  transactionPayload
	successor transactionPayload
}

// needsUpgradeHistory reports whether the current transaction could be an immediate product change that
// its evidence does not show. isUpgraded is set on the predecessor, not on the current transaction, and a
// purchase later than the lineage's first purchase is true of every renewal. The trigger is therefore a
// recent transaction that is not the first in its lineage and that Apple does not mark as a renewal.
// A transaction without transactionReason keeps the lookup.
func needsUpgradeHistory(current transactionPayload, now time.Time) bool {
	if current.TransactionReason == transactionReasonRenewal {
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

// closeUpgradedPredecessors adds an expired observation for each product that a recent immediate change in
// the current lineage replaced, when the verified evidence already names the current product.
// History is enrichment only: a lookup failure is reported to the provider call observer, and the result
// Apple already confirmed through subscription status is returned unchanged.
func (adapter *Adapter) closeUpgradedPredecessors(
	ctx context.Context,
	request stores.VerificationRequest,
	configuration configuration,
	kind core.ProductKind,
	current transactionPayload,
	result stores.VerificationResult,
) stores.VerificationResult {
	if !needsUpgradeHistory(current, result.VerifiedAt) {
		return result
	}
	historyContext, cancel := transactionHistoryContext(ctx)
	defer cancel()
	predecessors, err := adapter.queryUpgradedPredecessors(historyContext, request, configuration, current, result.VerifiedAt)
	if adapter.reportProviderCall != nil {
		adapter.reportProviderCall(transactionHistoryOperation, err)
	}
	if err != nil || len(predecessors) == 0 {
		return result
	}
	closedResult := result
	closedResult.Observations = append([]core.PurchaseObservation(nil), result.Observations...)
	closedResult.NamedProducts = []core.ProviderProductID{core.ProviderProductID(current.ProductID)}
	closedResult.SupersededObservations = append([]core.ObservationID(nil), result.SupersededObservations...)
	for _, predecessor := range predecessors {
		closed, err := supersededSubscriptionObservation(
			request.Application, kind, predecessor.previous, predecessor.successor, result.VerifiedAt,
		)
		if err != nil {
			return result
		}
		closedResult.Observations = append(closedResult.Observations, closed)
		closedResult.NamedProducts = append(closedResult.NamedProducts, closed.ProductID)
		closedResult.SupersededObservations = append(closedResult.SupersededObservations, closed.ID)
	}
	return closedResult
}

// transactionHistoryContext bounds the lookup by its own budget and by half of the caller's remaining time,
// so the persistence that follows keeps at least the other half of a caller deadline.
func transactionHistoryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	budget := transactionHistoryTimeout
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(deadline)/2)
	}
	return context.WithTimeout(ctx, budget)
}

// queryUpgradedPredecessors reads bounded transaction history and returns the upgraded predecessors of the
// current transaction. History holds signed transactions for the whole customer, so each lineage entry is
// verified through the same JWS path as submitted evidence and bound to the current transaction's
// application, environment, lineage, and customer before it can close a product.
func (adapter *Adapter) queryUpgradedPredecessors(
	ctx context.Context,
	request stores.VerificationRequest,
	configuration configuration,
	current transactionPayload,
	now time.Time,
) ([]upgradedPredecessor, error) {
	path := "/inApps/v2/history/" + url.PathEscape(current.OriginalTransactionID)
	var lineage []transactionPayload
	revision := ""
	for page := 0; page < maximumHistoryPages; page++ {
		query := url.Values{
			"sort":        []string{historySortDescending},
			"productType": []string{historyProductTypeAutoRenewable},
		}
		if revision != "" {
			query.Set("revision", revision)
		}
		body, err := adapter.get(ctx, request.Application, configuration, transactionHistoryOperation, path, query)
		if err != nil {
			return nil, err
		}
		var response transactionHistoryResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, invalid(transactionHistoryOperation, errors.New("apple transaction history response is invalid"))
		}
		if err := validateResponseScope(
			request.Application, configuration, "transaction history",
			response.Environment, response.BundleID, response.AppAppleID,
		); err != nil {
			return nil, invalid(transactionHistoryOperation, err)
		}
		for _, signed := range response.SignedTransactions {
			if unverifiedOriginalTransactionID(signed) != current.OriginalTransactionID {
				continue
			}
			candidate, err := verifyTransactionJWS(signed, configuration.roots)
			if err != nil || !isLineagePredecessor(request, configuration, current, candidate) {
				continue
			}
			lineage = append(lineage, candidate)
		}
		if !response.HasMore || response.Revision == "" || response.Revision == revision {
			break
		}
		revision = response.Revision
	}
	return upgradedPredecessors(current, lineage, now), nil
}

// unverifiedOriginalTransactionID reads the lineage of a signed transaction without verifying it.
// It only lets history skip other lineages before paying for certificate chain and signature
// verification. Nothing read here is trusted: an entry it keeps is verified before use.
func unverifiedOriginalTransactionID(signed string) string {
	parts := strings.Split(signed, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		OriginalTransactionID string `json:"originalTransactionId"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.OriginalTransactionID
}

// isLineagePredecessor binds one verified history entry to the current lineage and customer.
// It applies the application, environment, product type, and customer binding checks of submitted
// evidence, and the entry must share the current lineage and appAccountToken and not be purchased later.
func isLineagePredecessor(
	request stores.VerificationRequest,
	configuration configuration,
	current transactionPayload,
	candidate transactionPayload,
) bool {
	return validateTransactionScope(request, configuration.bundleID, core.ProductKindSubscription, candidate, false) == nil &&
		candidate.TransactionID != current.TransactionID &&
		candidate.OriginalTransactionID == current.OriginalTransactionID &&
		candidate.AppAccountToken == current.AppAccountToken &&
		candidate.PurchaseDate > 0 && candidate.PurchaseDate <= current.PurchaseDate
}

// upgradedPredecessors walks back from the current transaction through its lineage by purchase date and
// returns each consecutive predecessor Apple marks upgraded, paired with the transaction that replaced it.
// The walk stops at the first predecessor that was not upgraded, such as a renewal or a lapse, and at a
// change older than upgradeHistoryRecency, whose pre-change transaction has already had its daily replay.
// A predecessor of the current product, or of a product already closed, adds no observation.
func upgradedPredecessors(
	current transactionPayload,
	lineage []transactionPayload,
	now time.Time,
) []upgradedPredecessor {
	slices.SortFunc(lineage, func(left, right transactionPayload) int {
		return cmp.Or(
			cmp.Compare(right.PurchaseDate, left.PurchaseDate),
			cmp.Compare(right.ExpiresDate, left.ExpiresDate),
			strings.Compare(right.TransactionID, left.TransactionID),
		)
	})
	closedProducts := []string{current.ProductID}
	successor := current
	var predecessors []upgradedPredecessor
	for index, candidate := range lineage {
		if index > 0 && candidate.TransactionID == lineage[index-1].TransactionID {
			continue
		}
		if !candidate.IsUpgraded || now.Sub(milliseconds(successor.PurchaseDate)) > upgradeHistoryRecency {
			break
		}
		if !slices.Contains(closedProducts, candidate.ProductID) {
			closedProducts = append(closedProducts, candidate.ProductID)
			predecessors = append(predecessors, upgradedPredecessor{previous: candidate, successor: successor})
		}
		successor = candidate
	}
	return predecessors
}
