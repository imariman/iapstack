package apple

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// ConsumptionProductConsumable is the normalized Apple consumable product type.
	ConsumptionProductConsumable = "consumable"
	// ConsumptionProductNonConsumable is the normalized Apple non-consumable product type.
	ConsumptionProductNonConsumable = "non_consumable"
	// ConsumptionProductAutoRenewable is the normalized Apple auto-renewable subscription product type.
	ConsumptionProductAutoRenewable = "auto_renewable_subscription"
	// ConsumptionProductNonRenewing is the normalized Apple non-renewing subscription product type.
	ConsumptionProductNonRenewing = "non_renewing_subscription"

	// ConsumptionOwnershipPurchased means the customer bought the transaction directly.
	ConsumptionOwnershipPurchased = "purchased"
	// ConsumptionOwnershipFamilyShared means the customer has the transaction through Family Sharing.
	ConsumptionOwnershipFamilyShared = "family_shared"
	// ConsumptionOwnershipOther means Apple reported another ownership type, such as an assigned seat.
	ConsumptionOwnershipOther = "other"

	// DeliveryStatusDelivered is Apple's deliveryStatus for a delivered In-App Purchase that works properly.
	DeliveryStatusDelivered = "DELIVERED"
	// RefundPreferenceDecline is Apple's refundPreference asking the App Store to decline the refund.
	RefundPreferenceDecline = "DECLINE"
	// RefundPreferenceGrantFull is Apple's refundPreference asking the App Store to grant the full refund.
	RefundPreferenceGrantFull = "GRANT_FULL"
	// RefundPreferenceGrantProrated is Apple's refundPreference asking the App Store to grant a prorated refund.
	RefundPreferenceGrantProrated = "GRANT_PRORATED"

	// policyRefundNone is the operator policy that omits refundPreference.
	policyRefundNone = "none"
	// policyRefundDecline is the operator policy that sends DECLINE.
	policyRefundDecline = "decline"
	// policyRefundGrantFull is the operator policy that sends GRANT_FULL.
	policyRefundGrantFull = "grant_full"
	// policyRefundGrantProrated is the operator policy that sends GRANT_PRORATED for auto-renewable subscriptions.
	policyRefundGrantProrated = "grant_prorated"

	// appleConsumableType is Apple's consumable product discriminator.
	appleConsumableType = "Consumable"
	// appleNonRenewingType is Apple's non-renewing subscription product discriminator.
	appleNonRenewingType = "Non-Renewing Subscription"
	// consumptionPath is the Send Consumption Information endpoint, version 2.
	consumptionPath = "/inApps/v2/transactions/consumption/"
	// maximumTransactionIdentifier bounds the transaction identifier placed in the request path.
	maximumTransactionIdentifier = 64
)

var (
	// ErrConsumptionConsentRequired means consumption information cannot be sent without customer consent.
	ErrConsumptionConsentRequired = errors.New("customer consent is required to send consumption information")
	// ErrConsumptionDeliveryUnknown means IAPStack has no evidence that the purchase was delivered.
	ErrConsumptionDeliveryUnknown = errors.New("delivery of the purchase is not established")
	// appleDeliveryStatuses is Apple's closed deliveryStatus vocabulary for the version 2 request.
	appleDeliveryStatuses = map[string]struct{}{
		DeliveryStatusDelivered: {}, "UNDELIVERED_QUALITY_ISSUE": {}, "UNDELIVERED_WRONG_ITEM": {},
		"UNDELIVERED_SERVER_OUTAGE": {}, "UNDELIVERED_OTHER": {},
	}
	// appleCustomerReasons maps Apple's consumptionRequestReason values to stored codes.
	appleCustomerReasons = map[string]string{
		"UNINTENDED_PURCHASE": "unintended_purchase", "FULFILLMENT_ISSUE": "fulfillment_issue",
		"UNSATISFIED_WITH_PURCHASE": "unsatisfied_with_purchase", "LEGAL": "legal", "OTHER": "other",
	}
)

// ConsumptionSignal is the part of a CONSUMPTION_REQUEST that the response job needs.
// It is stored only inside the protected inbox and consumption payloads.
type ConsumptionSignal struct {
	TransactionID      string                 `json:"transaction_id"`
	ProductType        string                 `json:"product_type"`
	ProviderProductID  core.ProviderProductID `json:"provider_product_id"`
	ExternalCustomerID string                 `json:"external_customer_id"`
	Ownership          string                 `json:"ownership"`
	CustomerReason     string                 `json:"customer_reason,omitempty"`
}

// ConsumptionRequest is Apple's ConsumptionRequest body for Send Consumption Information.
// consumptionPercentage is never sent: IAPStack does not know how much of a purchase was used.
type ConsumptionRequest struct {
	CustomerConsented     bool   `json:"customerConsented"`
	DeliveryStatus        string `json:"deliveryStatus"`
	RefundPreference      string `json:"refundPreference,omitempty"`
	SampleContentProvided bool   `json:"sampleContentProvided"`
}

// ConsumptionInput is what the server knows about one request before it builds the Apple body.
type ConsumptionInput struct {
	ProductType           string
	CustomerConsented     bool
	Delivered             bool
	RefundPreference      string
	SampleContentProvided bool
}

// ConsumptionResponse is the non-sensitive result of one Send Consumption Information call.
type ConsumptionResponse struct {
	HTTPStatus     int
	AppleErrorCode int64
}

// appleErrorResponse is Apple's JSON error body.
type appleErrorResponse struct {
	ErrorCode int64 `json:"errorCode"`
}

// BuildConsumptionRequest builds the Apple body from established facts and the operator's policy.
// It refuses to build a body without consent or delivery evidence, because the API has no undeclared values
// for those fields. GRANT_PRORATED is sent only for auto-renewable subscriptions. For other product types
// Apple needs a consumptionPercentage that IAPStack cannot know, so the preference is left out.
func BuildConsumptionRequest(input ConsumptionInput) (ConsumptionRequest, error) {
	if !input.CustomerConsented {
		return ConsumptionRequest{}, ErrConsumptionConsentRequired
	}
	if !input.Delivered {
		return ConsumptionRequest{}, ErrConsumptionDeliveryUnknown
	}
	if !validConsumptionProductType(input.ProductType) {
		return ConsumptionRequest{}, fmt.Errorf("unsupported consumption product type %q", input.ProductType)
	}
	request := ConsumptionRequest{
		CustomerConsented: true, DeliveryStatus: DeliveryStatusDelivered,
		SampleContentProvided: input.SampleContentProvided,
	}
	switch input.RefundPreference {
	case policyRefundNone:
	case policyRefundDecline:
		request.RefundPreference = RefundPreferenceDecline
	case policyRefundGrantFull:
		request.RefundPreference = RefundPreferenceGrantFull
	case policyRefundGrantProrated:
		if input.ProductType == ConsumptionProductAutoRenewable {
			request.RefundPreference = RefundPreferenceGrantProrated
		}
	default:
		return ConsumptionRequest{}, fmt.Errorf("unsupported refund preference policy %q", input.RefundPreference)
	}
	return request, request.Validate()
}

// Validate checks Apple's documented constraints for one consumption body.
func (request ConsumptionRequest) Validate() error {
	if !request.CustomerConsented {
		return ErrConsumptionConsentRequired
	}
	if _, ok := appleDeliveryStatuses[request.DeliveryStatus]; !ok {
		return fmt.Errorf("unsupported Apple delivery status %q", request.DeliveryStatus)
	}
	switch request.RefundPreference {
	case "", RefundPreferenceDecline, RefundPreferenceGrantFull, RefundPreferenceGrantProrated:
		return nil
	default:
		return fmt.Errorf("unsupported Apple refund preference %q", request.RefundPreference)
	}
}

// Validate checks the identifiers and closed vocabularies of one consumption signal.
func (signal ConsumptionSignal) Validate() error {
	if !validNotificationIdentifier(signal.TransactionID) || len(signal.TransactionID) > maximumTransactionIdentifier {
		return errors.New("app store consumption transaction identifier is invalid")
	}
	if !validConsumptionProductType(signal.ProductType) {
		return errors.New("app store consumption product type is invalid")
	}
	if !validAppAccountToken(signal.ExternalCustomerID) {
		return errors.New("app store consumption customer binding is invalid")
	}
	switch signal.Ownership {
	case ConsumptionOwnershipPurchased, ConsumptionOwnershipFamilyShared, ConsumptionOwnershipOther:
	default:
		return errors.New("app store consumption ownership is invalid")
	}
	if signal.CustomerReason != "" {
		known := false
		for _, reason := range appleCustomerReasons {
			known = known || reason == signal.CustomerReason
		}
		if !known {
			return errors.New("app store consumption reason is invalid")
		}
	}
	return signal.ProviderProductID.Validate()
}

// SendConsumptionInformation calls PUT /inApps/v2/transactions/consumption/{transactionId}.
// The response always carries the HTTP status when Apple answered, including on failure.
func (adapter *Adapter) SendConsumptionInformation(
	ctx context.Context,
	application core.Application,
	transactionID string,
	request ConsumptionRequest,
) (ConsumptionResponse, error) {
	if err := request.Validate(); err != nil {
		return ConsumptionResponse{}, stores.NewFailure(adapter.Provider(), "consumption", stores.FailurePermanent, 0, err)
	}
	if !validNotificationIdentifier(transactionID) || len(transactionID) > maximumTransactionIdentifier {
		return ConsumptionResponse{}, stores.NewFailure(adapter.Provider(), "consumption", stores.FailurePermanent, 0, nil)
	}
	if err := application.Validate(); err != nil || application.Store.Provider != core.ProviderAppleAppStore {
		return ConsumptionResponse{}, stores.NewFailure(adapter.Provider(), "consumption", stores.FailurePermanent, 0, err)
	}
	configuration, err := adapter.configuration(ctx, application)
	if err != nil {
		return ConsumptionResponse{}, err
	}
	token, err := adapter.authorizationToken(configuration)
	if err != nil {
		return ConsumptionResponse{}, stores.NewFailure(adapter.Provider(), "token", stores.FailurePermanent, 0, err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return ConsumptionResponse{}, stores.NewFailure(adapter.Provider(), "consumption", stores.FailurePermanent, 0, err)
	}
	baseURL := adapter.productionURL
	if application.Store.Environment == core.EnvironmentSandbox {
		baseURL = adapter.sandboxURL
	}
	endpoint := strings.TrimRight(baseURL, "/") + consumptionPath + url.PathEscape(transactionID)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return ConsumptionResponse{}, stores.NewFailure(adapter.Provider(), "consumption", stores.FailurePermanent, 0, err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	responseBody, status, responseHeader, err := adapter.do(httpRequest)
	if err != nil {
		return ConsumptionResponse{HTTPStatus: status}, err
	}
	response := ConsumptionResponse{HTTPStatus: status, AppleErrorCode: appleErrorCode(responseBody)}
	switch {
	case status >= 200 && status < 300:
		return ConsumptionResponse{HTTPStatus: status}, nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return response, stores.NewFailure(adapter.Provider(), "consumption", stores.FailureUnauthorized, 0, nil)
	case status == http.StatusNotFound:
		return response, stores.NewFailure(adapter.Provider(), "consumption", stores.FailureNotFound, 0, nil)
	case status == http.StatusTooManyRequests:
		return response, stores.NewFailure(
			adapter.Provider(), "consumption", stores.FailureRateLimited, retryAfter(responseHeader, adapter.clock()), nil,
		)
	case status >= 500:
		return response, stores.NewFailure(adapter.Provider(), "consumption", stores.FailureTemporary, 0, nil)
	default:
		return response, stores.NewFailure(adapter.Provider(), "consumption", stores.FailurePermanent, 0, nil)
	}
}

// consumptionSignal extracts the consumption fields from one verified notification transaction.
func consumptionSignal(transaction transactionPayload, reason string) (ConsumptionSignal, error) {
	productType, err := consumptionProductType(transaction.Type)
	if err != nil {
		return ConsumptionSignal{}, err
	}
	ownership := ConsumptionOwnershipOther
	switch transaction.OwnershipType {
	case applePurchasedOwnership, "":
		ownership = ConsumptionOwnershipPurchased
	case appleFamilySharedOwnership:
		ownership = ConsumptionOwnershipFamilyShared
	}
	signal := ConsumptionSignal{
		TransactionID: transaction.TransactionID, ProductType: productType,
		ProviderProductID:  core.ProviderProductID(transaction.ProductID),
		ExternalCustomerID: transaction.AppAccountToken, Ownership: ownership,
		CustomerReason: appleCustomerReasons[reason],
	}
	return signal, signal.Validate()
}

// consumptionProductType maps Apple's product type string to the stored consumption vocabulary.
func consumptionProductType(value string) (string, error) {
	switch value {
	case appleConsumableType:
		return ConsumptionProductConsumable, nil
	case appleNonConsumableType:
		return ConsumptionProductNonConsumable, nil
	case appleSubscriptionType:
		return ConsumptionProductAutoRenewable, nil
	case appleNonRenewingType:
		return ConsumptionProductNonRenewing, nil
	default:
		return "", fmt.Errorf("unsupported Apple product type %q", value)
	}
}

// validConsumptionProductType reports whether a value is one stored consumption product type.
func validConsumptionProductType(value string) bool {
	switch value {
	case ConsumptionProductConsumable, ConsumptionProductNonConsumable,
		ConsumptionProductAutoRenewable, ConsumptionProductNonRenewing:
		return true
	default:
		return false
	}
}

// appleErrorCode reads Apple's numeric error code from a bounded response body, or zero.
func appleErrorCode(body []byte) int64 {
	var response appleErrorResponse
	if len(body) == 0 || json.Unmarshal(body, &response) != nil || response.ErrorCode < 0 {
		return 0
	}
	return response.ErrorCode
}
