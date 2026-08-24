package worker

import (
	"context"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/stores"
)

// TestRetryClassificationUsesProviderContract verifies transient and permanent durable outcomes.
func TestRetryClassificationUsesProviderContract(t *testing.T) {
	temporary := stores.NewFailure("huawei_appgallery", "verify", stores.FailureTemporary, 0, context.DeadlineExceeded)
	if !isRetryable(temporary) {
		t.Fatal("temporary provider failure was not retryable")
	}
	invalid := stores.NewFailure("huawei_appgallery", "verify", stores.FailureInvalidEvidence, 0, nil)
	if isRetryable(invalid) {
		t.Fatal("invalid evidence failure was retryable")
	}
	if got := errorCode(temporary); got != "provider_temporary" {
		t.Fatalf("errorCode() = %q, want provider_temporary", got)
	}
}

// TestBackoffIsBounded verifies full jitter remains inside the exponential operational ceiling.
func TestBackoffIsBounded(t *testing.T) {
	for attempt := 1; attempt <= 32; attempt++ {
		delay := backoff(attempt)
		if delay < 0 || delay > maximumBackoff {
			t.Fatalf("backoff(%d) = %v, want [0,%v]", attempt, delay, maximumBackoff)
		}
	}
	if delay := backoff(1); delay > time.Second {
		t.Fatalf("backoff(1) = %v, want at most 1s", delay)
	}
}
