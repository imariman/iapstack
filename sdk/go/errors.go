package iapstack

import "fmt"

// APIError is a stable non-success v1 envelope.
type APIError struct {
	// StatusCode is the HTTP response status.
	StatusCode int
	// Code is the stable v1 machine-readable error code.
	Code string
	// Message is a safe human-readable summary without payloads or credentials.
	Message string
	// RequestID is the request ID operators can correlate with server logs.
	RequestID string
	// Retryable reports whether a later idempotent retry may succeed.
	Retryable bool
}

// TransportError is a network failure before a complete HTTP response.
type TransportError struct {
	// Message is a redacted transport failure summary.
	Message string
	// Cause is the optional underlying non-secret error.
	Cause error
}

// TimeoutError is a bounded HTTP attempt that exceeded its deadline.
type TimeoutError struct {
	// Message is a redacted timeout failure summary.
	Message string
	// Cause is the optional underlying non-secret error.
	Cause error
}

// ProtocolError is a response that did not match the versioned JSON contract.
type ProtocolError struct {
	// Message is a redacted protocol failure summary.
	Message string
	// Cause is the optional underlying decode error.
	Cause error
}

// WebhookError is a fail-closed webhook authentication or replay failure.
type WebhookError struct {
	// Code is the stable webhook failure code.
	Code string
}

// Error returns a safe API failure without response payloads or credentials.
func (err *APIError) Error() string {
	if err == nil {
		return "IAPStack API error"
	}
	if err.RequestID == "" {
		return fmt.Sprintf("IAPStack API error (status: %d, code: %s, message: %s)", err.StatusCode, err.Code, err.Message)
	}
	return fmt.Sprintf(
		"IAPStack API error (status: %d, code: %s, request_id: %s, message: %s)",
		err.StatusCode, err.Code, err.RequestID, err.Message,
	)
}

// Error returns a redacted transport failure.
func (err *TransportError) Error() string {
	if err == nil || err.Message == "" {
		return "IAPStack request failed before a response was received"
	}
	return err.Message
}

// Unwrap returns the underlying transport cause without exposing it in Error.
func (err *TransportError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// Error returns a redacted timeout failure.
func (err *TimeoutError) Error() string {
	if err == nil || err.Message == "" {
		return "IAPStack request timed out"
	}
	return err.Message
}

// Unwrap returns the underlying timeout cause without exposing it in Error.
func (err *TimeoutError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// Error returns a redacted protocol failure.
func (err *ProtocolError) Error() string {
	if err == nil || err.Message == "" {
		return "IAPStack response did not match the v1 contract"
	}
	return err.Message
}

// Unwrap returns the underlying decode cause without exposing it in Error.
func (err *ProtocolError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// Error returns the stable webhook failure code.
func (err *WebhookError) Error() string {
	if err == nil || err.Code == "" {
		return "webhook verification failed"
	}
	return err.Code
}
