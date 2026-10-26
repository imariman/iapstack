package revenuecatimport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// revenueCatSubscriberURL is the v1 customer endpoint. GET creates a customer when the id is new.
	revenueCatSubscriberURL = "https://api.revenuecat.com/v1/subscribers/"
	// fetchAttempts bounds retries for 429 and 5xx responses.
	fetchAttempts = 3
)

// FetchConfig reads explicit subscriber ids and writes subscriber JSONL.
type FetchConfig struct {
	APIKey     string
	IDs        []string
	BaseURL    string
	HTTPClient *http.Client
	Sleep      func(time.Duration)
	Output     io.Writer
}

// FetchSubscribers calls the v1 subscriber endpoint for ids the operator already listed.
// The API key is read by the caller from the environment and is not written to Output.
func FetchSubscribers(ctx context.Context, config FetchConfig) error {
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("REVENUECAT_SECRET_API_KEY is required: %w", ErrUsage)
	}
	if len(config.IDs) == 0 {
		return fmt.Errorf("subscriber ids are required: %w", ErrUsage)
	}
	if config.Output == nil {
		return fmt.Errorf("fetch output is required: %w", ErrUsage)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.Sleep == nil {
		config.Sleep = time.Sleep
	}
	base := strings.TrimRight(config.BaseURL, "/")
	if base == "" {
		base = strings.TrimRight(revenueCatSubscriberURL, "/")
	}
	var buffer bytes.Buffer
	for _, id := range config.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		body, err := fetchSubscriber(ctx, config, base, id)
		if err != nil {
			return err
		}
		var document struct {
			RequestDate string          `json:"request_date"`
			Subscriber  json.RawMessage `json:"subscriber"`
		}
		if err := json.Unmarshal(body, &document); err != nil || len(document.Subscriber) == 0 {
			return fmt.Errorf("revenuecat subscriber %q response was not customer info", id)
		}
		encoded, err := json.Marshal(struct {
			AppUserID   string          `json:"app_user_id"`
			RequestDate string          `json:"request_date,omitempty"`
			Subscriber  json.RawMessage `json:"subscriber"`
		}{AppUserID: id, RequestDate: document.RequestDate, Subscriber: document.Subscriber})
		if err != nil {
			return fmt.Errorf("encode subscriber %q: %w", id, err)
		}
		buffer.Write(encoded)
		buffer.WriteByte('\n')
	}
	if buffer.Len() == 0 {
		return fmt.Errorf("subscriber ids are required: %w", ErrUsage)
	}
	_, err := config.Output.Write(buffer.Bytes())
	return err
}

// fetchSubscriber performs one GET and retries bounded 429 and 5xx responses.
func fetchSubscriber(ctx context.Context, config FetchConfig, base, id string) ([]byte, error) {
	var lastStatus int
	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		if attempt > 1 {
			config.Sleep(fetchJitter(attempt))
		}
		endpoint, err := url.JoinPath(base, id)
		if err != nil {
			return nil, fmt.Errorf("revenuecat subscriber url: %w", ErrUsage)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("revenuecat subscriber request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
		request.Header.Set("Accept", "application/json")
		response, err := config.HTTPClient.Do(request)
		if err != nil {
			lastStatus = 0
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if readErr != nil {
			lastStatus = response.StatusCode
			continue
		}
		if response.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("revenuecat subscriber request was unauthorized")
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			lastStatus = response.StatusCode
			continue
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("revenuecat subscriber %q returned status %d", id, response.StatusCode)
		}
		return body, nil
	}
	if lastStatus == 0 {
		return nil, fmt.Errorf("revenuecat subscriber %q request failed", id)
	}
	return nil, fmt.Errorf("revenuecat subscriber %q returned status %d", id, lastStatus)
}

// fetchJitter returns a bounded delay that grows with the attempt.
func fetchJitter(attempt int) time.Duration {
	ceiling := int64(50 * attempt)
	value, err := rand.Int(rand.Reader, big.NewInt(ceiling))
	if err != nil {
		return time.Duration(ceiling) * time.Millisecond
	}
	return time.Duration(value.Int64()+1) * time.Millisecond
}

// ReadSubscriberIDs returns one trimmed id per non-empty line.
func ReadSubscriberIDs(payload []byte) []string {
	lines := strings.Split(string(payload), "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		id := strings.TrimSpace(line)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
