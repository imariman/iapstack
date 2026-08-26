package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/app"
)

// TestLogProcessFailureDoesNotSerializeError verifies raw failure details stay out of logs.
func TestLogProcessFailureDoesNotSerializeError(t *testing.T) {
	const secret = "postgres://iapstack:do-not-log@postgres:5432/iapstack"
	var output bytes.Buffer

	logProcessFailure(&output, errors.New("connect using "+secret))

	logged := output.String()
	if strings.Contains(logged, secret) || strings.Contains(logged, "do-not-log") {
		t.Fatalf("logProcessFailure() exposed secret material: %s", logged)
	}
	if !strings.Contains(logged, `"error_code":"process_failed"`) {
		t.Fatalf("logProcessFailure() output = %s, want process_failed code", logged)
	}
}

// TestLogProcessFailureClassifiesUsageErrors verifies invalid invocations retain a useful code.
func TestLogProcessFailureClassifiesUsageErrors(t *testing.T) {
	var output bytes.Buffer

	logProcessFailure(&output, app.ErrUsage)

	if logged := output.String(); !strings.Contains(logged, `"error_code":"invalid_usage"`) {
		t.Fatalf("logProcessFailure() output = %s, want invalid_usage code", logged)
	}
}
