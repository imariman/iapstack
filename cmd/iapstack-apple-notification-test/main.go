// Command iapstack-apple-notification-test sends and verifies one sandbox TEST notification.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// appleSandboxURL is Apple's fixed App Store Server API sandbox origin.
	appleSandboxURL = "https://api.storekit-sandbox.apple.com"
	// appleAudience is the required App Store Server API authorization audience.
	appleAudience = "appstoreconnect-v1"
	// maximumPrivateKey bounds the PKCS#8 PEM value accepted over standard input.
	maximumPrivateKey = 64 << 10
	// maximumResponse bounds every response read from Apple.
	maximumResponse = 128 << 10
	// requestTimeout bounds each individual Apple API call.
	requestTimeout = 15 * time.Second
	// defaultPollTimeout bounds how long delivery status may remain unavailable.
	defaultPollTimeout = 45 * time.Second
	// pollInterval prevents a tight status polling loop.
	pollInterval = 2 * time.Second
	// tokenLifetime keeps authorization well below Apple's one-hour maximum.
	tokenLifetime = 5 * time.Minute
)

type options struct {
	issuerID    string
	keyID       string
	bundleID    string
	pollTimeout time.Duration
}

type appleClient struct {
	issuerID string
	keyID    string
	bundleID string
	key      *ecdsa.PrivateKey
	baseURL  string
	client   *http.Client
	clock    func() time.Time
}

type sendTestNotificationResponse struct {
	TestNotificationToken string `json:"testNotificationToken"`
}

type checkTestNotificationResponse struct {
	SendAttempts []sendAttempt `json:"sendAttempts"`
}

type sendAttempt struct {
	AttemptDate       int64  `json:"attemptDate"`
	SendAttemptResult string `json:"sendAttemptResult"`
}

// main runs the bounded sandbox notification test and emits only redacted status.
func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "apple notification test: %v\n", err)
		os.Exit(1)
	}
}

// run parses operator input, reads one private key, and completes a sandbox TEST delivery.
func run(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("iapstack-apple-notification-test", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configuration := options{}
	flags.StringVar(&configuration.issuerID, "issuer-id", "", "App Store Connect issuer ID")
	flags.StringVar(&configuration.keyID, "key-id", "", "App Store Connect In-App Purchase key ID")
	flags.StringVar(&configuration.bundleID, "bundle-id", "", "application bundle ID")
	flags.DurationVar(&configuration.pollTimeout, "poll-timeout", defaultPollTimeout, "maximum status polling time")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := configuration.validate(); err != nil {
		return err
	}
	privateKey, err := readPrivateKey(stdin)
	if err != nil {
		return err
	}
	client := &appleClient{
		issuerID: configuration.issuerID,
		keyID:    configuration.keyID,
		bundleID: configuration.bundleID,
		key:      privateKey,
		baseURL:  appleSandboxURL,
		client:   &http.Client{Timeout: requestTimeout},
		clock:    time.Now,
	}
	token, err := client.requestTestNotification(ctx)
	if err != nil {
		return fmt.Errorf("request sandbox TEST notification: %w", err)
	}
	fmt.Fprintln(stdout, "Apple accepted the sandbox TEST notification request; waiting for delivery status.")

	pollContext, cancel := context.WithTimeout(ctx, configuration.pollTimeout)
	defer cancel()
	for {
		attempts, ready, err := client.testNotificationStatus(pollContext, token)
		if err != nil {
			return fmt.Errorf("check sandbox TEST notification: %w", err)
		}
		if ready {
			return reportAttempts(stdout, attempts)
		}
		select {
		case <-pollContext.Done():
			return errors.New("delivery status did not become available before the polling deadline")
		case <-time.After(pollInterval):
		}
	}
}

// validate checks bounded App Store identity and polling inputs.
func (configuration options) validate() error {
	for name, value := range map[string]string{
		"issuer ID": configuration.issuerID,
		"key ID":    configuration.keyID,
		"bundle ID": configuration.bundleID,
	} {
		if strings.TrimSpace(value) != value || value == "" || len(value) > 255 {
			return fmt.Errorf("%s is required and must be bounded without surrounding whitespace", name)
		}
	}
	if configuration.pollTimeout <= 0 || configuration.pollTimeout > 5*time.Minute {
		return errors.New("poll timeout must be between zero and five minutes")
	}
	return nil
}

// readPrivateKey accepts exactly one bounded PKCS#8 P-256 PEM value from standard input.
func readPrivateKey(reader io.Reader) (*ecdsa.PrivateKey, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maximumPrivateKey+1))
	if err != nil {
		return nil, fmt.Errorf("read private key from stdin: %w", err)
	}
	defer zero(payload)
	if len(payload) == 0 || len(payload) > maximumPrivateKey {
		return nil, errors.New("private key from stdin is empty or too large")
	}
	block, trailing := pem.Decode(payload)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(trailing))) != 0 {
		return nil, errors.New("private key must be one PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse PKCS#8 private key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("private key must be an ECDSA P-256 key")
	}
	return key, nil
}

// requestTestNotification asks Apple to deliver one TEST notification to the sandbox URL.
func (client *appleClient) requestTestNotification(ctx context.Context) (string, error) {
	var response sendTestNotificationResponse
	status, err := client.request(ctx, http.MethodPost, "/inApps/v1/notifications/test", &response)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("Apple returned HTTP %d", status)
	}
	if response.TestNotificationToken == "" || len(response.TestNotificationToken) > 4096 {
		return "", errors.New("Apple returned an invalid test notification token")
	}
	return response.TestNotificationToken, nil
}

// testNotificationStatus returns Apple's redacted send attempts when they become available.
func (client *appleClient) testNotificationStatus(
	ctx context.Context,
	testToken string,
) ([]sendAttempt, bool, error) {
	var response checkTestNotificationResponse
	path := "/inApps/v1/notifications/test/" + url.PathEscape(testToken)
	status, err := client.request(ctx, http.MethodGet, path, &response)
	if err != nil {
		return nil, false, err
	}
	if status == http.StatusNotFound {
		return nil, false, nil
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("Apple returned HTTP %d", status)
	}
	if len(response.SendAttempts) == 0 {
		return nil, false, nil
	}
	return response.SendAttempts, true, nil
}

// request sends one authorized bounded App Store Server API request.
func (client *appleClient) request(ctx context.Context, method, path string, output any) (int, error) {
	token, err := client.authorizationToken()
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, nil)
	if err != nil {
		return 0, fmt.Errorf("create Apple request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("send Apple request: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumResponse+1))
	if err != nil {
		return 0, fmt.Errorf("read Apple response: %w", err)
	}
	if len(payload) > maximumResponse {
		return 0, errors.New("Apple response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, nil
	}
	if err := json.Unmarshal(payload, output); err != nil {
		return 0, errors.New("decode Apple response")
	}
	return response.StatusCode, nil
}

// authorizationToken creates a fresh five-minute ES256 App Store Server API JWT.
func (client *appleClient) authorizationToken() (string, error) {
	now := client.clock().UTC()
	header := struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}{Algorithm: "ES256", KeyID: client.keyID, Type: "JWT"}
	claims := struct {
		IssuerID string `json:"iss"`
		IssuedAt int64  `json:"iat"`
		Expires  int64  `json:"exp"`
		Audience string `json:"aud"`
		BundleID string `json:"bid"`
	}{
		IssuerID: client.issuerID,
		IssuedAt: now.Unix(),
		Expires:  now.Add(tokenLifetime).Unix(),
		Audience: appleAudience,
		BundleID: client.bundleID,
	}
	encodedHeader, err := encodeJSON(header)
	if err != nil {
		return "", err
	}
	encodedClaims, err := encodeJSON(claims)
	if err != nil {
		return "", err
	}
	signingInput := encodedHeader + "." + encodedClaims
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, client.key, digest[:])
	if err != nil {
		return "", errors.New("sign Apple authorization token")
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// encodeJSON serializes and base64url-encodes one JWT segment.
func encodeJSON(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("encode Apple authorization token")
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// reportAttempts prints non-secret delivery outcomes and requires at least one success.
func reportAttempts(writer io.Writer, attempts []sendAttempt) error {
	succeeded := false
	for index, attempt := range attempts {
		if attempt.SendAttemptResult == "" {
			return errors.New("Apple returned an attempt without a result")
		}
		attemptedAt := time.UnixMilli(attempt.AttemptDate).UTC().Format(time.RFC3339)
		fmt.Fprintf(writer, "Attempt %d: %s at %s\n", index+1, attempt.SendAttemptResult, attemptedAt)
		if attempt.SendAttemptResult == "SUCCESS" {
			succeeded = true
		}
	}
	if !succeeded {
		return errors.New("Apple did not receive HTTP 200 from the configured sandbox notification URL")
	}
	fmt.Fprintln(writer, "App Store Server Notifications V2 sandbox TEST delivery passed.")
	return nil
}

// zero overwrites temporary plaintext private-key bytes.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
