package controlplane

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/apple"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// EntitlementConflictMessage is the stable client text for dual allowed customer merges.
	EntitlementConflictMessage = "the customers have conflicting active entitlements"
)

// Problem is the stable, safe description of one failure. Code and Message never contain request
// or stored values, so every transport can show them to any caller.
type Problem struct {
	// Status is the matching HTTP status for transports that use one.
	Status  int
	Code    string
	Message string
	// RetryAfter is a provider cooldown the caller should wait out, or zero.
	RetryAfter time.Duration
}

// Classify maps a domain, storage, authentication, or provider failure onto its stable code and message.
// Unknown failures become internal_error so no internal detail leaks.
func Classify(err error) Problem {
	problem := Problem{
		Status: http.StatusInternalServerError, Code: "internal_error", Message: "the request could not be completed",
	}
	var providerFailure *stores.Failure
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		problem = Problem{Status: http.StatusServiceUnavailable, Code: "request_timeout", Message: "the request exceeded its processing deadline"}
	case errors.Is(err, auth.ErrCapacity):
		problem = Problem{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "authentication is temporarily unavailable"}
	case errors.Is(err, googleplay.ErrNotificationUnauthorized):
		problem = Problem{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "notification authentication failed"}
	case errors.Is(err, googleplay.ErrNotificationInvalid) || errors.Is(err, apple.ErrNotificationInvalid):
		problem = Problem{Status: http.StatusBadRequest, Code: "invalid_notification", Message: "notification payload is invalid"}
	case errors.Is(err, ErrForbidden):
		problem = Problem{Status: http.StatusForbidden, Code: "forbidden", Message: "the credential may not access this resource"}
	case errors.Is(err, persistence.ErrNotFound):
		problem = Problem{Status: http.StatusNotFound, Code: "not_found", Message: "the requested resource was not found"}
	case errors.Is(err, persistence.ErrManualGrantProductNotMapped):
		problem = Problem{Status: http.StatusUnprocessableEntity, Code: "product_not_mapped", Message: persistence.ErrManualGrantProductNotMapped.Error()}
	case errors.Is(err, persistence.ErrEntitlementConflict):
		problem = Problem{Status: http.StatusConflict, Code: "entitlement_conflict", Message: EntitlementConflictMessage}
	case errors.Is(err, persistence.ErrConflict):
		problem = Problem{Status: http.StatusConflict, Code: "conflict", Message: "the request conflicts with current state"}
	case errors.Is(err, persistence.ErrUnavailable):
		problem = Problem{Status: http.StatusServiceUnavailable, Code: "storage_unavailable", Message: "durable storage is temporarily unavailable"}
	case errors.As(err, &providerFailure):
		problem = classifyProviderFailure(providerFailure)
	case errors.Is(err, validation.ErrInvalid):
		problem = Problem{Status: http.StatusBadRequest, Code: "invalid_request", Message: "request values are invalid"}
	}
	return problem
}

// classifyProviderFailure maps one normalized store adapter failure.
func classifyProviderFailure(failure *stores.Failure) Problem {
	switch failure.Kind {
	case stores.FailureInvalidEvidence:
		return Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_purchase", Message: "purchase evidence could not be verified"}
	case stores.FailureUnauthorized:
		return Problem{Status: http.StatusBadGateway, Code: "provider_unauthorized", Message: "provider credentials were rejected"}
	case stores.FailureNotFound:
		return Problem{Status: http.StatusNotFound, Code: "purchase_not_found", Message: "the provider did not find the purchase"}
	case stores.FailureRateLimited, stores.FailureTemporary:
		return Problem{
			Status: http.StatusServiceUnavailable, Code: "provider_unavailable",
			Message: "the provider is temporarily unavailable", RetryAfter: failure.RetryAfter,
		}
	default:
		return Problem{Status: http.StatusBadGateway, Code: "provider_error", Message: "the provider request failed"}
	}
}
