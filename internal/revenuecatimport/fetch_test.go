package revenuecatimport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFetchSubscribersWritesJSONLWithoutPersistingTheKey verifies explicit-id fetch against a local server.
func TestFetchSubscribersWritesJSONLWithoutPersistingTheKey(t *testing.T) {
	t.Parallel()

	const apiKey = "revenuecat-test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+apiKey {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.URL.Path != "/v1/subscribers/user-1456" {
			t.Errorf("path = %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"request_date":"2026-10-26T00:00:00Z","subscriber":{"original_app_user_id":"user-1456"}}`))
	}))
	t.Cleanup(server.Close)

	var output strings.Builder
	err := FetchSubscribers(t.Context(), FetchConfig{
		APIKey:     apiKey,
		IDs:        []string{"user-1456"},
		BaseURL:    server.URL + "/v1/subscribers",
		HTTPClient: server.Client(),
		Sleep:      func(time.Duration) {},
		Output:     &output,
	})
	if err != nil {
		t.Fatalf("FetchSubscribers() error = %v", err)
	}
	if strings.Contains(output.String(), apiKey) || !strings.Contains(output.String(), `"app_user_id":"user-1456"`) {
		t.Fatalf("output = %s", output.String())
	}
}

// TestFetchSubscribersStopsOnUnauthorized verifies a 401 does not continue or echo the key.
func TestFetchSubscribersStopsOnUnauthorized(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	var output strings.Builder
	err := FetchSubscribers(t.Context(), FetchConfig{
		APIKey:     "secret-key",
		IDs:        []string{"one", "two"},
		BaseURL:    server.URL + "/v1/subscribers",
		HTTPClient: server.Client(),
		Sleep:      func(time.Duration) {},
		Output:     &output,
	})
	if err == nil || strings.Contains(err.Error(), "secret-key") || output.Len() != 0 || calls != 1 {
		t.Fatalf("error = %v calls = %d output = %s", err, calls, output.String())
	}
}
