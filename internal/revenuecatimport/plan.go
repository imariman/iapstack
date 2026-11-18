package revenuecatimport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

// Input is one local export plus the mapping and optional catalog.
type Input struct {
	ProjectID     core.ProjectID
	Mapping       Mapping
	Format        string
	Export        []byte
	Now           time.Time
	BatchID       string
	SkipAnonymous bool
	StoreAliases  bool
	Catalog       *Catalog
}

// Result is the planned canonical customers. It contains no receipt, price, or raw transaction id.
type Result struct {
	Subscribers []Subscriber
	Report      Report
}

// Plan parses an export and decides access without writing to PostgreSQL.
func Plan(input Input) (Result, error) {
	if err := input.ProjectID.Validate(); err != nil {
		return Result{}, fmt.Errorf("project: %w", ErrUsage)
	}
	if input.Now.IsZero() {
		return Result{}, fmt.Errorf("import clock is required: %w", ErrUsage)
	}
	if err := input.Mapping.Validate(input.ProjectID, input.Format); err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(input.Export)
	drafts, familyShared, trialPeriods, err := parseExport(input.Export, input.Format, input.Mapping.CanonicalIDColumn, input.Now.UTC())
	if err != nil {
		return Result{}, err
	}
	drafts = applyAliasConflicts(drafts)
	result := Result{Report: Report{
		DryRun:       true,
		ProjectID:    string(input.ProjectID),
		BatchID:      input.BatchID,
		ExportSHA256: hex.EncodeToString(sum[:]),
		Format:       input.Format,
		Failures:     []Failure{},
	}}
	result.Report.Counts.FamilyShared = familyShared
	result.Report.Counts.TrialPeriods = trialPeriods
	for _, draft := range drafts {
		subscriber := planSubscriber(input, draft)
		result.Subscribers = append(result.Subscribers, subscriber)
		result.Report.Failures = append(result.Report.Failures, subscriber.Failures...)
		if subscriber.Skipped {
			result.Report.Counts.SkippedAnonymous++
			continue
		}
		if subscriber.Anonymous && !subscriber.Fatal {
			result.Report.Counts.AnonymousCustomers++
		}
		if subscriber.Fatal {
			result.Report.Counts.Failed++
			continue
		}
		result.Report.Counts.CustomersCreated++
		result.Report.Counts.GrantsUpserted += len(subscriber.Grants)
		result.Report.Counts.IgnoredEarlierRefunds += subscriber.IgnoredEarlierRefunds
		for _, failure := range subscriber.Failures {
			if failure.Code == CodeAliasRefused {
				result.Report.Counts.AliasRefused++
			}
		}
		for _, grant := range subscriber.Grants {
			if grant.Reason == core.AccessReasonRefunded && grant.EndsAt != nil && grant.EndsAt.After(input.Now) {
				result.Report.Counts.RefundEndDisagreement++
			}
			if grantNotYetStarted(grant, input.Now) {
				result.Report.Counts.NotYetStarted++
				start := grant.StartsAt.UTC()
				if result.Report.NotYetStartedEarliestAt == nil || start.Before(result.Report.NotYetStartedEarliestAt.UTC()) {
					result.Report.NotYetStartedEarliestAt = &start
				}
			}
		}
	}
	if result.Report.Counts.NotYetStarted > 0 && result.Report.NotYetStartedEarliestAt != nil {
		result.Report.Warning = "re-import at or after " + result.Report.NotYetStartedEarliestAt.UTC().Format(time.RFC3339)
	}
	if result.Report.Failures == nil {
		result.Report.Failures = []Failure{}
	}
	return result, nil
}

// grantNotYetStarted reports a grant denied only because its period has not started.
func grantNotYetStarted(grant Grant, now time.Time) bool {
	return grant.Access == core.AccessDenied &&
		grant.Reason == core.AccessReasonProviderDecision &&
		!grant.StartsAt.IsZero() &&
		now.Before(grant.StartsAt)
}

// planSubscriber maps one canonical customer and refuses the subscriber when any grant fails.
func planSubscriber(input Input, draft subscriberDraft) Subscriber {
	subscriber := Subscriber{
		ExternalID: draft.externalID,
		CustomerID: CustomerID(input.ProjectID, draft.externalID),
		Anonymous:  draft.anonymous,
		Aliases:    uniqueAliases(draft.externalID, draft.aliases),
	}
	if draft.externalID == "" {
		subscriber.Fatal = true
		subscriber.Failures = append(subscriber.Failures, Failure{Line: draft.line, Code: CodeMissingAppUserID})
		return subscriber
	}
	if err := core.ValidateExternalCustomerID(draft.externalID); err != nil {
		subscriber.Fatal = true
		subscriber.Failures = append(subscriber.Failures, Failure{Line: draft.line, Code: CodeInvalidExternalID, ExternalID: draft.externalID})
		return subscriber
	}
	if input.SkipAnonymous && subscriber.Anonymous {
		subscriber.Skipped = true
		return subscriber
	}
	if draftHasConflict(draft) {
		subscriber.Fatal = true
		subscriber.Failures = append(subscriber.Failures, Failure{Line: draft.line, Code: CodeConflictingSubscriber, ExternalID: draft.externalID})
		return subscriber
	}
	for _, alias := range subscriber.Aliases {
		if err := core.ValidateExternalCustomerID(alias); err != nil {
			subscriber.Fatal = true
			subscriber.Failures = append(subscriber.Failures, Failure{Line: draft.line, Code: CodeInvalidExternalID, ExternalID: draft.externalID})
			return subscriber
		}
		if !input.StoreAliases {
			subscriber.Failures = append(subscriber.Failures, Failure{Line: draft.line, Code: CodeAliasRefused, ExternalID: alias})
		}
	}
	type candidate struct {
		row   sourceRow
		grant Grant
	}
	grouped := map[string]candidate{}
	// refundedStarts keeps the period start of each refunded row per group, keyed
	// by row index so a row that maps twice into one group is counted once.
	refundedStarts := map[string]map[int]time.Time{}
	var failures []Failure
	for rowIndex, row := range draft.rows {
		if row.ambiguousProduct {
			failures = append(failures, Failure{Line: row.line, Code: CodeConflictingSubscriber, ExternalID: draft.externalID, EntitlementKey: firstEntitlement(row)})
			continue
		}
		if row.invalidEntitlementList {
			failures = append(failures, Failure{Line: row.line, Code: CodeInvalidEntitlementList, ExternalID: draft.externalID})
			continue
		}
		if len(row.entitlementIDs) == 0 {
			continue
		}
		for _, revenueCatEntitlementID := range row.entitlementIDs {
			grant, failure, ok := mapGrant(input, draft.externalID, row, revenueCatEntitlementID)
			if !ok {
				failures = append(failures, failure)
				continue
			}
			// The mapped key, not the catalog id, separates entitlements: a dry-run
			// without a catalog has no entitlement ids, and with one the two match 1:1.
			key := string(grant.ApplicationID) + "\x00" + string(grant.ProductID) + "\x00" + grant.EntitlementKey + "\x00" + hex.EncodeToString(grant.LineageFingerprint[:])
			if row.refunded != nil {
				if refundedStarts[key] == nil {
					refundedStarts[key] = map[int]time.Time{}
				}
				refundedStarts[key][rowIndex] = timeOrZero(row.start)
			}
			existing, exists := grouped[key]
			if !exists || row.after(existing.row) {
				grouped[key] = candidate{row: row, grant: grant}
			}
		}
	}
	if len(failures) > 0 {
		subscriber.Fatal = true
		subscriber.Failures = failures
		subscriber.Grants = nil
		return subscriber
	}
	for key, candidate := range grouped {
		subscriber.Grants = append(subscriber.Grants, candidate.grant)
		subscriber.IgnoredEarlierRefunds += ignoredEarlierRefunds(candidate.row, refundedStarts[key])
	}
	sort.Slice(subscriber.Grants, func(left, right int) bool {
		if subscriber.Grants[left].EntitlementKey != subscriber.Grants[right].EntitlementKey {
			return subscriber.Grants[left].EntitlementKey < subscriber.Grants[right].EntitlementKey
		}
		return hex.EncodeToString(subscriber.Grants[left].LineageFingerprint[:]) < hex.EncodeToString(subscriber.Grants[right].LineageFingerprint[:])
	})
	return subscriber
}

// mapGrant resolves one entitlement cell to an existing catalog product and an access decision.
func mapGrant(input Input, externalID string, row sourceRow, revenueCatEntitlementID string) (Grant, Failure, bool) {
	failure := Failure{Line: row.line, ExternalID: externalID, EntitlementKey: revenueCatEntitlementID}
	if row.missingPurchase {
		failure.Code = CodeUnknownProduct
		return Grant{}, failure, false
	}
	key, mapped := input.Mapping.Entitlements[revenueCatEntitlementID]
	if !mapped {
		failure.Code = CodeUnknownEntitlement
		return Grant{}, failure, false
	}
	failure.EntitlementKey = key
	environment := string(core.EnvironmentProduction)
	if row.sandbox {
		environment = string(core.EnvironmentSandbox)
	}
	if row.store == "" || !input.Mapping.storeMapped(row.store) {
		failure.Code = CodeUnsupportedStore
		return Grant{}, failure, false
	}
	productMapping, found := input.Mapping.productMapping(row.store, environment, row.productIdentifier)
	if !found {
		if input.Mapping.productMappedOnOtherEnvironment(row.store, environment, row.productIdentifier) {
			failure.Code = CodeEnvironmentMismatch
		} else {
			failure.Code = CodeUnknownProduct
		}
		return Grant{}, failure, false
	}
	var entitlementID core.EntitlementID
	var productKind core.ProductKind
	kindKnown := false
	if input.Catalog != nil {
		application, applicationOK := input.Catalog.applications[core.ApplicationID(productMapping.ApplicationID)]
		product, productOK := input.Catalog.products[core.ProductID(productMapping.ProductID)]
		entitlement, entitlementOK := input.Catalog.entitlements[key]
		if !applicationOK || !productOK {
			failure.Code = CodeUnknownProduct
			return Grant{}, failure, false
		}
		if application.Store.Environment != core.Environment(productMapping.Environment) || string(application.Store.Environment) != environment {
			failure.Code = CodeEnvironmentMismatch
			return Grant{}, failure, false
		}
		if !entitlementOK {
			failure.Code = CodeUnknownEntitlement
			failure.EntitlementKey = key
			return Grant{}, failure, false
		}
		if !productGrants(product, entitlement.ID) {
			failure.Code = CodeEntitlementNotGranted
			return Grant{}, failure, false
		}
		if requiresStoreProduct(row.store) {
			if _, storeOK := input.Catalog.storeProducts[storeProductKey(application.ID, row.productIdentifier)]; !storeOK {
				failure.Code = CodeUnknownProduct
				return Grant{}, failure, false
			}
		}
		entitlementID = entitlement.ID
		productKind = product.Kind
		kindKnown = true
	}
	decision := decideAccess(row, productKind, kindKnown, input.Now)
	if decision.failure != "" {
		failure.Code = decision.failure
		return Grant{}, failure, false
	}
	lineage := sha256.Sum256([]byte(row.lineageMaterial))
	grant := Grant{
		ID:                 GrantID(string(input.ProjectID), string(CustomerID(input.ProjectID, externalID)), productMapping.ApplicationID, productMapping.ProductID, string(entitlementID), lineage),
		Line:               row.line,
		EntitlementKey:     key,
		EntitlementID:      entitlementID,
		ApplicationID:      core.ApplicationID(productMapping.ApplicationID),
		ProductID:          core.ProductID(productMapping.ProductID),
		OriginStore:        row.store,
		Environment:        core.Environment(environment),
		Access:             decision.access,
		Reason:             decision.reason,
		StartsAt:           decision.start,
		EndsAt:             decision.end,
		ObservedAt:         row.observedAt,
		LineageFingerprint: lineage,
	}
	failure.LineageFingerprint = hex.EncodeToString(lineage[:])
	return grant, Failure{}, true
}

// productGrants reports whether a product includes one entitlement.
func productGrants(product core.Product, entitlementID core.EntitlementID) bool {
	for _, candidate := range product.EntitlementIDs {
		if candidate == entitlementID {
			return true
		}
	}
	return false
}

// applyAliasConflicts refuses subscribers that claim one alias for two canonical ids, or an alias that is another canonical id.
func applyAliasConflicts(drafts []subscriberDraft) []subscriberDraft {
	canonicals := map[string]struct{}{}
	owners := map[string]map[string]struct{}{}
	for _, draft := range drafts {
		if draft.externalID == "" {
			continue
		}
		canonicals[draft.externalID] = struct{}{}
		for _, alias := range uniqueAliases(draft.externalID, draft.aliases) {
			if owners[alias] == nil {
				owners[alias] = map[string]struct{}{}
			}
			owners[alias][draft.externalID] = struct{}{}
		}
	}
	conflicts := map[string]struct{}{}
	for alias, canonicalIDs := range owners {
		if len(canonicalIDs) > 1 {
			for canonicalID := range canonicalIDs {
				conflicts[canonicalID] = struct{}{}
			}
		}
		if _, isCanonical := canonicals[alias]; isCanonical {
			for canonicalID := range canonicalIDs {
				if canonicalID != alias {
					conflicts[canonicalID] = struct{}{}
				}
			}
		}
	}
	for index := range drafts {
		if _, conflict := conflicts[drafts[index].externalID]; conflict {
			drafts[index].rows = append(drafts[index].rows, sourceRow{line: drafts[index].line, ambiguousProduct: true, canonicalID: drafts[index].externalID})
		}
	}
	return drafts
}

// draftHasConflict reports the marker added when two canonical ids claim one alias.
func draftHasConflict(draft subscriberDraft) bool {
	for _, row := range draft.rows {
		if row.ambiguousProduct && row.productIdentifier == "" && len(row.entitlementIDs) == 0 {
			return true
		}
	}
	return false
}

// uniqueAliases returns alias ids that are not the canonical id.
func uniqueAliases(canonicalID string, aliases []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == canonicalID {
			continue
		}
		if _, exists := seen[alias]; exists {
			continue
		}
		seen[alias] = struct{}{}
		result = append(result, alias)
	}
	return result
}

// firstEntitlement returns a display key for a row that failed before mapping.
func firstEntitlement(row sourceRow) string {
	if len(row.entitlementIDs) == 0 {
		return ""
	}
	return row.entitlementIDs[0]
}

// after reports whether row is the current lineage record over other. Keys are
// compared in one fixed order, so the winner does not depend on export row
// order. The latest period start wins, so a refund recorded later against an
// earlier period cannot replace a still-valid renewal. Within one period the
// latest export update wins, so a later refunded snapshot replaces a stale one
// whether or not the export carries a store transaction id. Then the later
// effective end wins, and on a full tie a refunded snapshot wins.
func (row sourceRow) after(other sourceRow) bool {
	leftStart, rightStart := timeOrZero(row.start), timeOrZero(other.start)
	if !leftStart.Equal(rightStart) {
		return leftStart.After(rightStart)
	}
	leftUpdated, rightUpdated := timeOrZero(row.updated), timeOrZero(other.updated)
	if !leftUpdated.Equal(rightUpdated) {
		return leftUpdated.After(rightUpdated)
	}
	leftHas, rightHas := row.effectiveEnd != nil, other.effectiveEnd != nil
	if leftHas != rightHas {
		return leftHas
	}
	if leftHas && !row.effectiveEnd.Equal(*other.effectiveEnd) {
		return row.effectiveEnd.After(*other.effectiveEnd)
	}
	return row.refunded != nil && other.refunded == nil
}

// ignoredEarlierRefunds counts refunded rows from an earlier period that the
// winning unrefunded row superseded within one lineage.
func ignoredEarlierRefunds(winner sourceRow, refundedStarts map[int]time.Time) int {
	if winner.refunded != nil {
		return 0
	}
	winnerStart := timeOrZero(winner.start)
	count := 0
	for _, start := range refundedStarts {
		if start.Before(winnerStart) {
			count++
		}
	}
	return count
}

// timeOrZero dereferences an optional export timestamp.
func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
