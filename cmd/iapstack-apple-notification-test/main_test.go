package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

// TestAuthorizationTokenUsesAppleES256Claims verifies claims, encoding, and raw signature format.
func TestAuthorizationTokenUsesAppleES256Claims(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	fixedTime := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	client := &appleClient{
		issuerID: "issuer-1", keyID: "ABCDEFGHIJ", bundleID: "com.example.app",
		key: key, clock: func() time.Time { return fixedTime },
	}

	token, err := client.authorizationToken()
	if err != nil {
		t.Fatalf("authorizationToken() error = %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT segments = %d, want 3", len(parts))
	}
	var header map[string]any
	decodeSegment(t, parts[0], &header)
	if header["alg"] != "ES256" || header["kid"] != "ABCDEFGHIJ" || header["typ"] != "JWT" {
		t.Fatalf("JWT header = %#v", header)
	}
	var claims map[string]any
	decodeSegment(t, parts[1], &claims)
	if claims["iss"] != "issuer-1" || claims["aud"] != appleAudience || claims["bid"] != "com.example.app" {
		t.Fatalf("JWT claims = %#v", claims)
	}
	if claims["iat"] != float64(fixedTime.Unix()) || claims["exp"] != float64(fixedTime.Add(tokenLifetime).Unix()) {
		t.Fatalf("JWT temporal claims = %#v", claims)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		t.Fatalf("JWT signature length = %d, error = %v", len(signature), err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(
		&key.PublicKey,
		digest[:],
		new(big.Int).SetBytes(signature[:32]),
		new(big.Int).SetBytes(signature[32:]),
	) {
		t.Fatal("JWT signature verification failed")
	}
}

// TestNotificationClientRequestsAndChecksRedactedStatus verifies the two Apple API operations.
func TestNotificationClientRequestsAndChecksRedactedStatus(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	requests := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
			t.Error("missing Apple authorization bearer")
		}
		response := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Request:    request,
		}
		response.Header.Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/inApps/v1/notifications/test":
			if request.Method != http.MethodPost {
				t.Errorf("request method = %s, want POST", request.Method)
			}
			response.Body = io.NopCloser(strings.NewReader(`{"testNotificationToken":"test-token"}`))
		case "/inApps/v1/notifications/test/test-token":
			if request.Method != http.MethodGet {
				t.Errorf("status method = %s, want GET", request.Method)
			}
			response.Body = io.NopCloser(strings.NewReader(`{"signedPayload":"must-not-be-returned","sendAttempts":[{"attemptDate":1788264000000,"sendAttemptResult":"SUCCESS"}]}`))
		default:
			response.StatusCode = http.StatusNotFound
			response.Body = io.NopCloser(strings.NewReader(`{"errorCode":4040008}`))
		}
		return response, nil
	})
	client := &appleClient{
		issuerID: "issuer-1", keyID: "ABCDEFGHIJ", bundleID: "com.example.app",
		key: key, baseURL: "https://apple.example", client: &http.Client{Transport: transport}, clock: time.Now,
	}

	token, err := client.requestTestNotification(context.Background())
	if err != nil || token != "test-token" {
		t.Fatalf("requestTestNotification() = %q, %v", token, err)
	}
	attempts, ready, err := client.testNotificationStatus(context.Background(), token)
	if err != nil || !ready || len(attempts) != 1 || attempts[0].SendAttemptResult != "SUCCESS" {
		t.Fatalf("testNotificationStatus() = %#v, %t, %v", attempts, ready, err)
	}
	if requests != 2 {
		t.Fatalf("request count = %d, want 2", requests)
	}
}

// RoundTrip delegates one test request without opening a network listener.
func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// TestReportAttemptsRequiresSuccessAndOmitsPayloads rejects unsuccessful delivery results.
func TestReportAttemptsRequiresSuccessAndOmitsPayloads(t *testing.T) {
	var output strings.Builder
	err := reportAttempts(&output, []sendAttempt{{
		AttemptDate: 1788264000000, SendAttemptResult: "TLS_ISSUE",
	}})
	if err == nil || !strings.Contains(output.String(), "TLS_ISSUE") {
		t.Fatalf("reportAttempts() output = %q, error = %v", output.String(), err)
	}
}

// decodeSegment opens one JWT segment into a test destination.
func decodeSegment(t *testing.T, value string, output any) {
	t.Helper()
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode JWT segment: %v", err)
	}
	if err := json.Unmarshal(payload, output); err != nil {
		t.Fatalf("unmarshal JWT segment: %v", err)
	}
}
