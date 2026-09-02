package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

// TestNewEmitsStructuredLogsAtTheConfiguredLevel verifies JSON shape and level filtering.
func TestNewEmitsStructuredLogsAtTheConfiguredLevel(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := New(&output, slog.LevelWarn)
	logger.InfoContext(context.Background(), "hidden", "request_id", "ignored")
	logger.WarnContext(context.Background(), "visible", "request_id", "request-1")

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode structured log: %v; output=%s", err, output.String())
	}
	if record["level"] != "WARN" || record["msg"] != "visible" || record["request_id"] != "request-1" {
		t.Fatalf("structured record = %#v", record)
	}
	if bytes.Contains(output.Bytes(), []byte("hidden")) || bytes.Contains(output.Bytes(), []byte("ignored")) {
		t.Fatalf("output contains a record below the configured level: %s", output.String())
	}
}
