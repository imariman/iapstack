package revenuecatimport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// sourceRow is one normalized export line before catalog mapping.
type sourceRow struct {
	line                   int
	canonicalID            string
	aliases                []string
	store                  string
	sandbox                bool
	productIdentifier      string
	entitlementIDs         []string
	invalidEntitlementList bool
	start                  *time.Time
	end                    *time.Time
	grace                  *time.Time
	effectiveEnd           *time.Time
	updated                *time.Time
	autoRenewable          bool
	refunded               *time.Time
	unsubscribe            *time.Time
	billing                *time.Time
	autoResume             *time.Time
	ownership              string
	trial                  bool
	lineageMaterial        string
	observedAt             time.Time
	nonSubscription        bool
	ambiguousProduct       bool
	missingPurchase        bool
}

// subscriberDraft collects every export row for one canonical app user id.
type subscriberDraft struct {
	externalID string
	line       int
	aliases    []string
	rows       []sourceRow
	anonymous  bool
}

// parseExport reads a transactions feed or subscriber JSONL into canonical subscribers.
func parseExport(payload []byte, format, canonicalColumn string, now time.Time) ([]subscriberDraft, int, int, error) {
	switch format {
	case FormatTransactionsCSV:
		return parseTransactionsCSV(payload, canonicalColumn, now)
	case FormatSubscriberJSONL:
		return parseSubscriberJSONL(payload, now)
	default:
		return nil, 0, 0, fmt.Errorf("unsupported revenuecat import format %q: %w", format, ErrUsage)
	}
}

// parseTransactionsCSV groups feed rows by the mapped canonical id column.
func parseTransactionsCSV(payload []byte, canonicalColumn string, now time.Time) ([]subscriberDraft, int, int, error) {
	reader := csv.NewReader(bytes.NewReader(bytesTrimBOM(payload)))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = false
	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, 0, 0, fmt.Errorf("revenuecat transactions export is empty: %w", ErrUsage)
		}
		return nil, 0, 0, csvReadError(err)
	}
	columns := map[string]int{}
	for index, name := range header {
		name = strings.TrimSpace(name)
		if _, duplicate := columns[name]; duplicate {
			return nil, 0, 0, fmt.Errorf("duplicate revenuecat column %q: %w", name, ErrUsage)
		}
		columns[name] = index
	}
	for _, required := range []string{canonicalColumn, "product_identifier", "entitlement_identifiers", "store", "is_auto_renewable", "is_sandbox"} {
		if _, ok := columns[required]; !ok {
			return nil, 0, 0, fmt.Errorf("revenuecat column %q is required: %w", required, ErrUsage)
		}
	}
	aliasColumn := columnLastSeenAlias
	if canonicalColumn == columnLastSeenAlias {
		aliasColumn = columnOriginalAppUserID
	}
	order := make([]string, 0)
	grouped := map[string]*subscriberDraft{}
	familyShared := 0
	trialPeriods := 0
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, 0, 0, csvReadError(readErr)
		}
		line, _ := reader.FieldPos(0)
		row, rowErr := transactionRow(record, columns, canonicalColumn, aliasColumn, line, now)
		if rowErr != nil {
			return nil, 0, 0, rowErr
		}
		if row.trial {
			trialPeriods++
		}
		if strings.EqualFold(row.ownership, "FAMILY_SHARED") {
			familyShared++
		}
		key := row.canonicalID
		if key == "" {
			key = fmt.Sprintf("\x00missing-%d", line)
		}
		draft, exists := grouped[key]
		if !exists {
			draft = &subscriberDraft{externalID: row.canonicalID, line: line, anonymous: strings.HasPrefix(row.canonicalID, anonymousPrefix)}
			grouped[key] = draft
			order = append(order, key)
		}
		draft.rows = append(draft.rows, row)
		draft.aliases = append(draft.aliases, row.aliases...)
	}
	drafts := make([]subscriberDraft, 0, len(order))
	for _, key := range order {
		drafts = append(drafts, *grouped[key])
	}
	return drafts, familyShared, trialPeriods, nil
}

// transactionRow copies only the allowlisted transactions columns from one record.
func transactionRow(record []string, columns map[string]int, canonicalColumn, aliasColumn string, line int, now time.Time) (sourceRow, error) {
	cell := func(name string) string {
		index, ok := columns[name]
		if !ok || index >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[index])
	}
	sandbox, err := parseBool(cell("is_sandbox"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	autoRenewable, err := parseBool(cell("is_auto_renewable"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	start, _, err := parseExportTime(cell("start_time"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	end, endSet, err := parseExportTime(cell("end_time"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	grace, graceSet, err := parseExportTime(cell("grace_period_end_time"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	effectiveEnd, effectiveSet, err := parseExportTime(cell("effective_end_time"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	updated, updatedSet, err := parseExportTime(cell("updated_at"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	refunded, refundedSet, err := parseExportTime(cell("refunded_at"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	unsubscribe, unsubscribeSet, err := parseExportTime(cell("unsubscribe_detected_at"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	billing, billingSet, err := parseExportTime(cell("billing_issues_detected_at"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	autoResume, autoResumeSet, err := parseExportTime(cell("auto_resume_time"))
	if err != nil {
		return sourceRow{}, fmt.Errorf("line %d: %w", line, err)
	}
	entitlements, entitlementsOK := parseEntitlementList(cell("entitlement_identifiers"))
	originalTransaction := cell("original_store_transaction_id")
	storeTransaction := cell("store_transaction_id")
	material := originalTransaction
	if material == "" {
		material = storeTransaction
	}
	if material == "" {
		material = cell("product_identifier") + "\x00" + cell("start_time")
	}
	observed := now.UTC()
	if updatedSet {
		observed = updated
	}
	row := sourceRow{
		line:                   line,
		canonicalID:            cell(canonicalColumn),
		store:                  cell("store"),
		sandbox:                sandbox,
		productIdentifier:      cell("product_identifier"),
		entitlementIDs:         entitlements,
		invalidEntitlementList: !entitlementsOK,
		autoRenewable:          autoRenewable,
		ownership:              cell("ownership_type"),
		trial:                  false,
		lineageMaterial:        material,
		observedAt:             observed,
	}
	if _, ok := columns["is_trial_period"]; ok && cell("is_trial_period") != "" {
		trial, trialErr := parseBool(cell("is_trial_period"))
		if trialErr != nil {
			return sourceRow{}, fmt.Errorf("line %d: %w", line, trialErr)
		}
		row.trial = trial
	}
	if startSet := cell("start_time") != ""; startSet {
		row.start = &start
	}
	if endSet {
		row.end = &end
	}
	if graceSet {
		row.grace = &grace
	}
	if effectiveSet {
		row.effectiveEnd = &effectiveEnd
	}
	if updatedSet {
		row.updated = &updated
	}
	if refundedSet {
		row.refunded = &refunded
	}
	if unsubscribeSet {
		row.unsubscribe = &unsubscribe
	}
	if billingSet {
		row.billing = &billing
	}
	if autoResumeSet {
		row.autoResume = &autoResume
	}
	alias := cell(aliasColumn)
	if alias != "" && alias != row.canonicalID {
		row.aliases = []string{alias}
	}
	return row, nil
}

// parseEntitlementList accepts only a JSON string array. Empty and null unlock nothing.
func parseEntitlementList(cell string) ([]string, bool) {
	cell = strings.TrimSpace(cell)
	if cell == "" || cell == "null" {
		return nil, true
	}
	var identifiers []string
	if err := json.Unmarshal([]byte(cell), &identifiers); err != nil {
		return nil, false
	}
	for _, identifier := range identifiers {
		if identifier == "" || strings.TrimSpace(identifier) != identifier {
			return nil, false
		}
	}
	return identifiers, true
}

// csvReadError classifies an unfinished record separately from a bad mapping.
func csvReadError(err error) error {
	var parseError *csv.ParseError
	if errors.As(err, &parseError) && (errors.Is(parseError.Err, io.ErrUnexpectedEOF) || errors.Is(parseError.Err, io.EOF) || strings.Contains(parseError.Err.Error(), "extraneous or missing")) {
		return fmt.Errorf("revenuecat transactions record is unfinished: %w", ErrTruncated)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("revenuecat transactions record is unfinished: %w", ErrTruncated)
	}
	return fmt.Errorf("read revenuecat transactions export: %w", ErrUsage)
}

// parseSubscriberJSONL reads one wrapped Customer Info object per non-empty line.
func parseSubscriberJSONL(payload []byte, now time.Time) ([]subscriberDraft, int, int, error) {
	lines := bytes.Split(bytesTrimBOM(payload), []byte("\n"))
	drafts := make([]subscriberDraft, 0, len(lines))
	familyShared := 0
	for number, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		if bytes.HasSuffix(payload, []byte("\n")) && number == len(lines)-1 && len(trimmed) == 0 {
			continue
		}
		draft, shared, err := decodeSubscriberLine(trimmed, number+1, now)
		if err != nil {
			return nil, 0, 0, err
		}
		familyShared += shared
		drafts = append(drafts, draft)
	}
	if len(drafts) == 0 {
		return nil, 0, 0, fmt.Errorf("revenuecat subscriber export is empty: %w", ErrUsage)
	}
	return drafts, familyShared, 0, nil
}

// decodeSubscriberLine decodes one envelope and keeps only the documented Customer Info fields.
func decodeSubscriberLine(line []byte, number int, now time.Time) (subscriberDraft, int, error) {
	var envelope struct {
		AppUserID   string          `json:"app_user_id"`
		RequestDate string          `json:"request_date"`
		Subscriber  json.RawMessage `json:"subscriber"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return subscriberDraft{}, 0, fmt.Errorf("line %d is not subscriber json: %w", number, ErrUsage)
	}
	var subscriber struct {
		RequestDate       string                     `json:"request_date"`
		OriginalAppUserID string                     `json:"original_app_user_id"`
		Aliases           []string                   `json:"aliases"`
		Entitlements      map[string]json.RawMessage `json:"entitlements"`
		Subscriptions     map[string]json.RawMessage `json:"subscriptions"`
		NonSubscriptions  map[string]json.RawMessage `json:"non_subscriptions"`
	}
	if len(envelope.Subscriber) > 0 {
		if err := json.Unmarshal(envelope.Subscriber, &subscriber); err != nil {
			return subscriberDraft{}, 0, fmt.Errorf("line %d subscriber is invalid: %w", number, ErrUsage)
		}
	}
	requestDate := envelope.RequestDate
	if requestDate == "" {
		requestDate = subscriber.RequestDate
	}
	observed := now.UTC()
	if requestDate != "" {
		parsed, _, err := parseExportTime(requestDate)
		if err != nil {
			return subscriberDraft{}, 0, fmt.Errorf("line %d: %w", number, err)
		}
		observed = parsed
	}
	draft := subscriberDraft{
		externalID: strings.TrimSpace(envelope.AppUserID),
		line:       number,
		anonymous:  strings.HasPrefix(strings.TrimSpace(envelope.AppUserID), anonymousPrefix),
	}
	if subscriber.OriginalAppUserID != "" && subscriber.OriginalAppUserID != draft.externalID {
		draft.aliases = append(draft.aliases, subscriber.OriginalAppUserID)
	}
	draft.aliases = append(draft.aliases, subscriber.Aliases...)
	familyShared := 0
	for entitlementID, raw := range subscriber.Entitlements {
		var entitlement struct {
			ExpiresDate            string `json:"expires_date"`
			GracePeriodExpiresDate string `json:"grace_period_expires_date"`
			ProductIdentifier      string `json:"product_identifier"`
			PurchaseDate           string `json:"purchase_date"`
		}
		if err := json.Unmarshal(raw, &entitlement); err != nil {
			return subscriberDraft{}, 0, fmt.Errorf("line %d entitlement is invalid: %w", number, ErrUsage)
		}
		row, shared, err := subscriberGrantRow(entitlementID, entitlement.ProductIdentifier, entitlement.ExpiresDate, entitlement.GracePeriodExpiresDate, entitlement.PurchaseDate, subscriber.Subscriptions, subscriber.NonSubscriptions, number, observed)
		if err != nil {
			return subscriberDraft{}, 0, err
		}
		familyShared += shared
		row.canonicalID = draft.externalID
		draft.rows = append(draft.rows, row)
	}
	return draft, familyShared, nil
}

// subscriberGrantRow joins one entitlement to its subscription or non-subscription purchase.
func subscriberGrantRow(
	entitlementID, productIdentifier, expiresDate, graceDate, purchaseDate string,
	subscriptions, nonSubscriptions map[string]json.RawMessage,
	line int,
	observed time.Time,
) (sourceRow, int, error) {
	_, subscriptionPresent := subscriptions[productIdentifier]
	_, nonSubscriptionPresent := nonSubscriptions[productIdentifier]
	row := sourceRow{
		line:              line,
		productIdentifier: productIdentifier,
		entitlementIDs:    []string{entitlementID},
		observedAt:        observed,
	}
	if subscriptionPresent && nonSubscriptionPresent {
		row.ambiguousProduct = true
		return row, 0, nil
	}
	if !subscriptionPresent && !nonSubscriptionPresent {
		row.missingPurchase = true
		return row, 0, nil
	}
	if subscriptionPresent {
		var subscription struct {
			Store                   string `json:"store"`
			IsSandbox               bool   `json:"is_sandbox"`
			ExpiresDate             string `json:"expires_date"`
			GracePeriodExpiresDate  string `json:"grace_period_expires_date"`
			PurchaseDate            string `json:"purchase_date"`
			RefundedAt              string `json:"refunded_at"`
			UnsubscribeDetectedAt   string `json:"unsubscribe_detected_at"`
			BillingIssuesDetectedAt string `json:"billing_issues_detected_at"`
			OwnershipType           string `json:"ownership_type"`
			StoreTransactionID      string `json:"store_transaction_id"`
			AutoResumeDate          string `json:"auto_resume_date"`
		}
		if err := json.Unmarshal(subscriptions[productIdentifier], &subscription); err != nil {
			return sourceRow{}, 0, fmt.Errorf("line %d subscription is invalid: %w", line, ErrUsage)
		}
		filled, err := fillSubscriberPurchase(&row, subscription.Store, subscription.IsSandbox, subscription.PurchaseDate, subscription.ExpiresDate, subscription.GracePeriodExpiresDate, subscription.RefundedAt, subscription.UnsubscribeDetectedAt, subscription.BillingIssuesDetectedAt, subscription.AutoResumeDate, true)
		if err != nil {
			return sourceRow{}, 0, fmt.Errorf("line %d: %w", line, err)
		}
		row = filled
		row.ownership = subscription.OwnershipType
		row.autoRenewable = true
		material := subscription.StoreTransactionID
		if material == "" {
			material = productIdentifier + "\x00" + subscription.PurchaseDate
		}
		row.lineageMaterial = material
		shared := 0
		if strings.EqualFold(subscription.OwnershipType, "FAMILY_SHARED") {
			shared = 1
		}
		return row, shared, nil
	}
	var purchases []struct {
		ID           string `json:"id"`
		IsSandbox    bool   `json:"is_sandbox"`
		PurchaseDate string `json:"purchase_date"`
		Store        string `json:"store"`
	}
	if err := json.Unmarshal(nonSubscriptions[productIdentifier], &purchases); err != nil || len(purchases) == 0 {
		row.missingPurchase = true
		return row, 0, nil
	}
	selected := purchases[0]
	for _, purchase := range purchases[1:] {
		if purchase.PurchaseDate > selected.PurchaseDate {
			selected = purchase
		}
	}
	filled, err := fillSubscriberPurchase(&row, selected.Store, selected.IsSandbox, selected.PurchaseDate, expiresDate, graceDate, "", "", "", "", false)
	if err != nil {
		return sourceRow{}, 0, fmt.Errorf("line %d: %w", line, err)
	}
	row = filled
	if row.start == nil && purchaseDate != "" {
		parsed, set, parseErr := parseExportTime(purchaseDate)
		if parseErr != nil {
			return sourceRow{}, 0, fmt.Errorf("line %d: %w", line, parseErr)
		}
		if set {
			row.start = &parsed
		}
	}
	material := selected.ID
	if material == "" {
		material = productIdentifier + "\x00" + selected.PurchaseDate
	}
	row.lineageMaterial = material
	row.nonSubscription = true
	row.autoRenewable = false
	return row, 0, nil
}

// fillSubscriberPurchase copies purchase dates onto one source row.
func fillSubscriberPurchase(row *sourceRow, store string, sandbox bool, purchaseDate, expiresDate, graceDate, refunded, unsubscribe, billing, autoResume string, subscription bool) (sourceRow, error) {
	copied := *row
	copied.store = store
	copied.sandbox = sandbox
	if purchase, set, err := parseExportTime(purchaseDate); err != nil {
		return sourceRow{}, err
	} else if set {
		copied.start = &purchase
	}
	boundary, boundarySet, err := subscriberBoundary(expiresDate, graceDate)
	if err != nil {
		return sourceRow{}, err
	}
	if boundarySet {
		copied.effectiveEnd = &boundary
		if parsed, set, parseErr := parseExportTime(expiresDate); parseErr != nil {
			return sourceRow{}, parseErr
		} else if set {
			copied.end = &parsed
		}
	}
	if parsed, set, parseErr := parseExportTime(graceDate); parseErr != nil {
		return sourceRow{}, parseErr
	} else if set {
		copied.grace = &parsed
	}
	if parsed, set, parseErr := parseExportTime(refunded); parseErr != nil {
		return sourceRow{}, parseErr
	} else if set {
		copied.refunded = &parsed
	}
	if parsed, set, parseErr := parseExportTime(unsubscribe); parseErr != nil {
		return sourceRow{}, parseErr
	} else if set {
		copied.unsubscribe = &parsed
	}
	if parsed, set, parseErr := parseExportTime(billing); parseErr != nil {
		return sourceRow{}, parseErr
	} else if set {
		copied.billing = &parsed
	}
	if parsed, set, parseErr := parseExportTime(autoResume); parseErr != nil {
		return sourceRow{}, parseErr
	} else if set {
		copied.autoResume = &parsed
	}
	if !subscription && copied.effectiveEnd == nil {
		copied.nonSubscription = true
	}
	return copied, nil
}

// subscriberBoundary uses grace when it is strictly after expiry.
func subscriberBoundary(expiresDate, graceDate string) (time.Time, bool, error) {
	expires, expiresSet, err := parseExportTime(expiresDate)
	if err != nil {
		return time.Time{}, false, err
	}
	grace, graceSet, err := parseExportTime(graceDate)
	if err != nil {
		return time.Time{}, false, err
	}
	if graceSet && expiresSet && grace.After(expires) {
		return grace, true, nil
	}
	if expiresSet {
		return expires, true, nil
	}
	if graceSet && !expiresSet {
		return grace, true, nil
	}
	return time.Time{}, false, nil
}
