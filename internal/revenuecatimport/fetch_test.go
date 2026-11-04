package revenuecatimport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

// TestFetchSubscribersSendsEachIDAsOnePathSegment verifies an id with separators cannot fetch another customer.
func TestFetchSubscribersSendsEachIDAsOnePathSegment(t *testing.T) {
	t.Parallel()

	const prefix = "/v1/subscribers/"
	var mutex sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		escapedPath := request.URL.EscapedPath()
		mutex.Lock()
		paths = append(paths, escapedPath)
		mutex.Unlock()
		// Answer as RevenueCat would: the customer is the one named by the path below the prefix.
		segment, err := url.PathUnescape(strings.TrimPrefix(escapedPath, prefix))
		if !strings.HasPrefix(escapedPath, prefix) || err != nil {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := json.Marshal(map[string]any{
			"request_date": "2026-11-02T00:00:00Z",
			"subscriber":   map[string]string{"original_app_user_id": segment},
		})
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(body)
	}))
	t.Cleanup(server.Close)

	ids := []string{"bob/../alice", "team/alice", "a%2Fb", "ali ce"}
	var output strings.Builder
	err := FetchSubscribers(t.Context(), FetchConfig{
		APIKey:     "revenuecat-test-secret",
		IDs:        ids,
		BaseURL:    server.URL + "/v1/subscribers",
		HTTPClient: server.Client(),
		Sleep:      func(time.Duration) {},
		Output:     &output,
	})
	if err != nil {
		t.Fatalf("FetchSubscribers() error = %v", err)
	}
	wantPaths := []string{
		prefix + "bob%2F..%2Falice",
		prefix + "team%2Falice",
		prefix + "a%252Fb",
		prefix + "ali%20ce",
	}
	mutex.Lock()
	gotPaths := append([]string(nil), paths...)
	mutex.Unlock()
	if strings.Join(gotPaths, "\n") != strings.Join(wantPaths, "\n") {
		t.Errorf("request paths = %q, want %q", gotPaths, wantPaths)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != len(ids) {
		t.Fatalf("output lines = %d, want %d: %s", len(lines), len(ids), output.String())
	}
	for index, line := range lines {
		var record struct {
			AppUserID  string `json:"app_user_id"`
			Subscriber struct {
				OriginalAppUserID string `json:"original_app_user_id"`
			} `json:"subscriber"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode output line %d: %v", index, err)
		}
		if record.AppUserID != ids[index] || record.Subscriber.OriginalAppUserID != ids[index] {
			t.Errorf("line %d labels %q with customer %q, want %q for both", index, record.AppUserID, record.Subscriber.OriginalAppUserID, ids[index])
		}
	}
}

// TestFetchSubscribersRejectsDotSegmentsBeforeAnyRequest verifies dot-segment ids cannot walk up the endpoint path.
func TestFetchSubscribersRejectsDotSegmentsBeforeAnyRequest(t *testing.T) {
	t.Parallel()

	for _, ids := range [][]string{{".."}, {"."}, {"user-1", ".."}, {" . "}} {
		var mutex sync.Mutex
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			mutex.Lock()
			calls++
			mutex.Unlock()
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"request_date":"2026-11-02T00:00:00Z","subscriber":{"original_app_user_id":"someone-else"}}`))
		}))
		var output strings.Builder
		err := FetchSubscribers(t.Context(), FetchConfig{
			APIKey:     "revenuecat-test-secret",
			IDs:        ids,
			BaseURL:    server.URL + "/v1/subscribers",
			HTTPClient: server.Client(),
			Sleep:      func(time.Duration) {},
			Output:     &output,
		})
		server.Close()
		mutex.Lock()
		gotCalls := calls
		mutex.Unlock()
		if !errors.Is(err, ErrUsage) || gotCalls != 0 || output.Len() != 0 {
			t.Errorf("ids %q: error = %v calls = %d output = %q, want ErrUsage, no request, no output", ids, err, gotCalls, output.String())
		}
	}
}
