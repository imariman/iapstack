package verification

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// MergeCustomers merges write.SourceCustomerID into write.CanonicalCustomerID and refreshes the
// canonical projection with projector, inside the caller's transaction.
//
// The merge is refused with persistence.ErrEntitlementConflict when, at mergedAt, both customers
// can use one entitlement, or when an entitlement either customer can use would not be allowed
// after the merge. A customer can use an entitlement when its stored snapshot allows it or when a
// refresh of its own sources would; reading both keeps a snapshot ranked by an earlier projector
// from hiding access. Dual access is refused outright and never resolved by the projector, which
// is only used to find access the merge would end.
//
// Both customers and the canonical closure are locked before anything is read. The post-merge
// projections that are checked are the ones that are then written, built from the sources the
// merged closure exposes to every later refresh. A refusal comes after the merge row is written,
// so the caller must roll back the transaction on any error, as Transact does. A replayed merge
// returns the stored row and refreshes nothing.
func MergeCustomers(
	ctx context.Context,
	repository persistence.Transaction,
	projector Projector,
	write persistence.CustomerMergeWrite,
	mergedAt time.Time,
) (persistence.CustomerMergeResult, error) {
	if projector == nil {
		return persistence.CustomerMergeResult{}, fmt.Errorf("entitlement projector is not initialized")
	}
	if err := write.Validate(); err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	if write.CanonicalCustomerID == write.SourceCustomerID {
		return repository.MergeCustomers(ctx, write)
	}
	if err := lockMergedCustomers(ctx, repository, write); err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	canonical, err := planCustomerProjection(ctx, repository, projector, write.ProjectID, write.CanonicalCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	source, err := planCustomerProjection(ctx, repository, projector, write.ProjectID, write.SourceCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}

	result, err := repository.MergeCustomers(ctx, write)
	if err != nil || result.Replay {
		return result, err
	}
	merged, err := planCustomerProjection(ctx, repository, projector, write.ProjectID, write.CanonicalCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	if mergeConflicts(canonical.usableAt(mergedAt), source.usableAt(mergedAt), merged.accessAfter(mergedAt)) {
		return persistence.CustomerMergeResult{}, fmt.Errorf("merge customers: %w", persistence.ErrEntitlementConflict)
	}
	if _, err := applyCustomerProjection(
		ctx, repository, write.ProjectID, write.CanonicalCustomerID, merged, mergedAt, true,
	); err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	return result, nil
}

// lockMergedCustomers locks both customers in id order, as the repository merge does, and then
// every customer already merged into the canonical one, so the access read before the merge
// cannot change before it commits.
func lockMergedCustomers(
	ctx context.Context,
	repository persistence.Transaction,
	write persistence.CustomerMergeWrite,
) error {
	ids := []core.CustomerID{write.CanonicalCustomerID, write.SourceCustomerID}
	slices.Sort(ids)
	for _, id := range ids {
		if err := repository.LockCustomer(ctx, write.ProjectID, id); err != nil {
			return err
		}
	}
	return repository.LockCustomerClosure(ctx, write.ProjectID, write.CanonicalCustomerID)
}

// mergeConflicts reports an entitlement both customers can use, or one that either customer can
// use but the merged customer would not be allowed.
func mergeConflicts(
	canonical map[core.EntitlementID]struct{},
	source map[core.EntitlementID]struct{},
	merged map[core.EntitlementID]core.AccessStatus,
) bool {
	for entitlementID := range canonical {
		if _, dual := source[entitlementID]; dual {
			return true
		}
	}
	for _, usable := range []map[core.EntitlementID]struct{}{canonical, source} {
		for entitlementID := range usable {
			if merged[entitlementID] != core.AccessAllowed {
				return true
			}
		}
	}
	return false
}
