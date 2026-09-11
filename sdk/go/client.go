package iapstack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// sdkVersion identifies this host package in X-IAPStack-SDK.
	sdkVersion = "0.1.0-dev.1"
	// jsonContentType is the only accepted v1 request and response media type.
	jsonContentType = "application/json"
)

// Client is an application-bearer HTTP client for trusted host backends.
type Client struct {
	// baseURL is the IAPStack origin, optionally including a reverse-proxy path prefix.
	baseURL *url.URL
	// applicationID is the application scope encoded in every public API path.
	applicationID string
	// applicationToken is the durable host bearer retained only in memory.
	applicationToken string
	// timeout is the maximum duration of one HTTP attempt, including response streaming.
	timeout time.Duration
	// retryPolicy is the bounded retry policy for idempotent IAPStack operations.
	retryPolicy RetryPolicy
	// maxResponseBytes is the maximum accepted JSON response size.
	maxResponseBytes int64
	// httpClient is the transport used for abortable JSON requests.
	httpClient *http.Client
	// ownsClient reports whether Close should release idle connections.
	ownsClient bool
	// delay waits between retry attempts unless the parent context is done.
	delay func(context.Context, time.Duration) error
	// jitter returns a full-jitter value in [0, 1].
	jitter func() float64
}

type rawResponse struct {
	// StatusCode is the HTTP status returned by one attempt.
	StatusCode int
	// Header contains response headers used for request-ID correlation.
	Header http.Header
	// Body is the size-limited response payload.
	Body []byte
}

type customerSessionRequest struct {
	// ExternalCustomerID is the host-authenticated customer bound to the session.
	ExternalCustomerID string `json:"external_customer_id"`
}

type contextKey struct{}

type apiErrorEnvelope struct {
	// Error is the nested v1 failure object.
	Error apiErrorEnvelopeError `json:"error"`
}

type apiErrorEnvelopeError struct {
	// Code is the stable v1 machine-readable error code.
	Code string `json:"code"`
	// Message is a safe human-readable summary.
	Message string `json:"message"`
	// RequestID is the request ID operators can correlate with server logs.
	RequestID string `json:"request_id"`
}

var requestIDContextKey = contextKey{}

// NewClient validates host configuration and constructs an application-bearer client.
func NewClient(config Config) (*Client, error) {
	config = config.applyDefaults()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("base URL must be an origin or path prefix")
	}
	httpClient := config.HTTPClient
	ownsClient := false
	if httpClient == nil {
		httpClient = &http.Client{}
		ownsClient = true
	}
	return &Client{
		baseURL:          parsed,
		applicationID:    config.ApplicationID,
		applicationToken: config.ApplicationToken,
		timeout:          config.Timeout,
		retryPolicy:      config.RetryPolicy,
		maxResponseBytes: config.MaxResponseBytes,
		httpClient:       httpClient,
		ownsClient:       ownsClient,
		delay:            waitForRetry,
		jitter:           rand.Float64,
	}, nil
}

// WithRequestID attaches one operator-correlatable X-Request-ID value to the call.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDContextKey, strings.TrimSpace(requestID))
}

// CreateCustomerSession mints one short-lived customer bearer after the host authenticates its user.
func (client *Client) CreateCustomerSession(ctx context.Context, externalCustomerID string) (CustomerSession, error) {
	if err := validateExternalCustomerID(externalCustomerID); err != nil {
		return CustomerSession{}, err
	}
	body, err := client.applicationRequest(
		ctx,
		http.MethodPost,
		[]string{"v1", "applications", client.applicationID, "customer-sessions"},
		customerSessionRequest{ExternalCustomerID: externalCustomerID},
	)
	if err != nil {
		return CustomerSession{}, err
	}
	var session CustomerSession
	if err := decodeJSON(body, &session); err != nil {
		return CustomerSession{}, err
	}
	if err := session.validate(); err != nil {
		return CustomerSession{}, err
	}
	session.ExpiresAt = session.ExpiresAt.UTC()
	session.ExternalCustomerID = externalCustomerID
	return session, nil
}

// GetEntitlements loads the current projection using a customer session bearer.
//
// The public v1 contract authenticates GET
// /v1/applications/{application_id}/customers/{external_customer_id}/entitlements
// with customerSessionBearer, not the durable application bearer. Mint a session
// with CreateCustomerSession, then either return that token to a mobile client
// or use it here. Hosts that only need push updates should verify signed
// webhooks instead of polling.
func (client *Client) GetEntitlements(ctx context.Context, session CustomerSession) (EntitlementSnapshot, error) {
	if err := validateExternalCustomerID(session.ExternalCustomerID); err != nil {
		return EntitlementSnapshot{}, err
	}
	if err := validateBearer("customer token", session.Token); err != nil {
		return EntitlementSnapshot{}, err
	}
	body, err := client.customerRequest(
		ctx,
		session.Token,
		http.MethodGet,
		[]string{"v1", "applications", client.applicationID, "customers", session.ExternalCustomerID, "entitlements"},
		nil,
	)
	if err != nil {
		return EntitlementSnapshot{}, err
	}
	var snapshot EntitlementSnapshot
	if err := decodeJSON(body, &snapshot); err != nil {
		return EntitlementSnapshot{}, err
	}
	if err := snapshot.validate(); err != nil {
		return EntitlementSnapshot{}, err
	}
	snapshot.normalize()
	return snapshot, nil
}

// Close releases idle connections for a client-owned HTTP transport.
func (client *Client) Close() {
	if client == nil || !client.ownsClient || client.httpClient == nil {
		return
	}
	client.httpClient.CloseIdleConnections()
}

// applicationRequest sends one JSON round trip authenticated with the durable host bearer.
func (client *Client) applicationRequest(ctx context.Context, method string, path []string, body any) (json.RawMessage, error) {
	return client.request(ctx, method, client.applicationToken, path, body)
}

// customerRequest sends one JSON round trip authenticated with a customer session bearer.
func (client *Client) customerRequest(ctx context.Context, customerToken, method string, path []string, body any) (json.RawMessage, error) {
	return client.request(ctx, method, customerToken, path, body)
}

// request performs one abortable, retrying JSON round trip without logging credentials.
func (client *Client) request(
	ctx context.Context,
	method string,
	bearer string,
	path []string,
	body any,
) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestID := requestIDFrom(ctx)
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, &ProtocolError{Message: "IAPStack request could not be encoded", Cause: err}
		}
		payload = encoded
	}
	target := resolveURL(client.baseURL, path...)
	var last error
	for attempt := 1; attempt <= client.retryPolicy.MaxAttempts; attempt++ {
		response, err := client.attempt(ctx, method, bearer, target, payload, requestID)
		if err != nil {
			last = err
			if !client.shouldRetry(ctx, err, attempt) {
				return nil, err
			}
			if waitErr := client.wait(ctx, attempt); waitErr != nil {
				return nil, last
			}
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return json.RawMessage(response.Body), nil
		}
		apiErr := apiErrorFromResponse(response)
		last = apiErr
		if !client.shouldRetry(ctx, apiErr, attempt) {
			return nil, apiErr
		}
		if waitErr := client.wait(ctx, attempt); waitErr != nil {
			return nil, last
		}
	}
	if last != nil {
		return nil, last
	}
	return nil, &TransportError{Message: "IAPStack request exhausted its retry policy"}
}

// attempt sends one bounded HTTP request and reads a size-limited body.
func (client *Client) attempt(
	ctx context.Context,
	method string,
	bearer string,
	target *url.URL,
	payload []byte,
	requestID string,
) (rawResponse, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(attemptCtx, method, target.String(), reader)
	if err != nil {
		return rawResponse{}, &TransportError{Message: "IAPStack request failed before a response was received", Cause: err}
	}
	request.Header.Set("Accept", jsonContentType)
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("X-IAPStack-SDK", "go-host/"+sdkVersion)
	if requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}
	if payload != nil {
		request.Header.Set("Content-Type", jsonContentType)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return rawResponse{}, classifyAttemptError(ctx, attemptCtx, err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, client.maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return rawResponse{}, classifyAttemptError(ctx, attemptCtx, err)
	}
	if int64(len(body)) > client.maxResponseBytes {
		return rawResponse{}, &ProtocolError{Message: "IAPStack response exceeded the configured size limit"}
	}
	return rawResponse{StatusCode: response.StatusCode, Header: response.Header, Body: body}, nil
}

// shouldRetry reports whether another attempt is allowed for a retryable failure.
func (client *Client) shouldRetry(ctx context.Context, err error, attempt int) bool {
	if attempt >= client.retryPolicy.MaxAttempts || ctx.Err() != nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable
	}
	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) {
		return true
	}
	var transportErr *TransportError
	return errors.As(err, &transportErr)
}

// wait sleeps the full-jitter delay unless the parent context is already done.
func (client *Client) wait(ctx context.Context, attempt int) error {
	delay, err := client.retryPolicy.DelayAfter(attempt, client.jitter())
	if err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	return client.delay(ctx, delay)
}

// classifyAttemptError maps transport failures onto timeout or redacted transport errors.
func classifyAttemptError(parent context.Context, attempt context.Context, err error) error {
	if parent.Err() != nil {
		if errors.Is(parent.Err(), context.DeadlineExceeded) {
			return &TimeoutError{Message: "IAPStack request timed out", Cause: err}
		}
		return &TransportError{Message: "IAPStack request failed before a response was received", Cause: err}
	}
	if attempt.Err() != nil && errors.Is(attempt.Err(), context.DeadlineExceeded) {
		return &TimeoutError{Message: "IAPStack request timed out", Cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{Message: "IAPStack request timed out", Cause: err}
	}
	return &TransportError{Message: "IAPStack request failed before a response was received", Cause: err}
}

// apiErrorFromResponse extracts the v1 error envelope without retaining extra payload fields.
func apiErrorFromResponse(response rawResponse) *APIError {
	code := "http_error"
	message := "IAPStack returned an unsuccessful response"
	requestID := strings.TrimSpace(response.Header.Get("X-Request-ID"))
	var envelope apiErrorEnvelope
	if err := json.Unmarshal(response.Body, &envelope); err == nil {
		if envelope.Error.Code != "" {
			code = envelope.Error.Code
		}
		if envelope.Error.Message != "" {
			message = envelope.Error.Message
		}
		if envelope.Error.RequestID != "" {
			requestID = envelope.Error.RequestID
		}
	}
	return &APIError{
		StatusCode: response.StatusCode,
		Code:       code,
		Message:    message,
		RequestID:  requestID,
		Retryable:  response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500,
	}
}

// decodeJSON unmarshals one success body into a contract struct.
func decodeJSON(body json.RawMessage, value any) error {
	if err := json.Unmarshal(body, value); err != nil {
		return &ProtocolError{Message: "IAPStack returned an invalid JSON object", Cause: err}
	}
	return nil
}

// resolveURL appends escaped path segments to an origin or reverse-proxy prefix.
func resolveURL(base *url.URL, segments ...string) *url.URL {
	resolved := *base
	escaped := strings.TrimSuffix(resolved.EscapedPath(), "/")
	for _, segment := range segments {
		escaped += "/" + url.PathEscape(segment)
	}
	if escaped == "" {
		escaped = "/"
	}
	unescaped, err := url.PathUnescape(escaped)
	if err != nil {
		unescaped = escaped
	}
	resolved.Path = unescaped
	resolved.RawPath = escaped
	resolved.RawQuery = ""
	resolved.Fragment = ""
	return &resolved
}

// waitForRetry sleeps unless the parent context is cancelled first.
func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// requestIDFrom returns the optional X-Request-ID attached to the call context.
func requestIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey).(string)
	return strings.TrimSpace(value)
}

// utcTime copies a timestamp pointer into UTC without sharing the input.
func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

// validateExternalCustomerID requires one non-empty identity without surrounding whitespace.
func validateExternalCustomerID(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("external customer ID must not be empty")
	}
	return nil
}
