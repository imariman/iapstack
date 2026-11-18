package jobs

import (
	"testing"
	"time"
)

// TestAppleConsumptionRetryAtDoublesCapsAndStopsAtDeadline verifies the bounded consumption backoff.
func TestAppleConsumptionRetryAtDoublesCapsAndStopsAtDeadline(t *testing.T) {
	t.Parallel()

	failedAt := time.Date(2026, time.November, 18, 9, 0, 0, 0, time.UTC)
	deadline := failedAt.Add(AppleConsumptionResponseWindow)
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: time.Minute},
		{attempt: 1, want: time.Minute},
		{attempt: 2, want: 2 * time.Minute},
		{attempt: 3, want: 4 * time.Minute},
		{attempt: 6, want: 32 * time.Minute},
		{attempt: 7, want: time.Hour},
		{attempt: 9, want: time.Hour},
		{attempt: 64, want: time.Hour},
	} {
		if got := AppleConsumptionRetryAt(test.attempt, failedAt, deadline); !got.Equal(failedAt.Add(test.want)) {
			t.Errorf("AppleConsumptionRetryAt(%d) = %s, want %s", test.attempt, got, failedAt.Add(test.want))
		}
	}
	nearDeadline := failedAt.Add(10 * time.Minute)
	if got := AppleConsumptionRetryAt(8, failedAt, nearDeadline); !got.Equal(nearDeadline) {
		t.Fatalf("AppleConsumptionRetryAt(near deadline) = %s, want %s", got, nearDeadline)
	}
	if got := AppleConsumptionRetryAt(1, failedAt, time.Time{}); !got.Equal(failedAt.Add(time.Minute)) {
		t.Fatalf("AppleConsumptionRetryAt(no deadline) = %s", got)
	}
}

// TestAppleConsumptionScheduleFitsInsideResponseWindow verifies every attempt can run before Apple's deadline.
func TestAppleConsumptionScheduleFitsInsideResponseWindow(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.November, 18, 9, 0, 0, 0, time.UTC)
	deadline := start.Add(AppleConsumptionResponseWindow)
	attemptAt := start
	for attempt := 1; attempt < AppleConsumptionMaxAttempts; attempt++ {
		attemptAt = AppleConsumptionRetryAt(attempt, attemptAt, deadline)
	}
	if !attemptAt.Before(deadline) {
		t.Fatalf("last attempt at %s is not before deadline %s", attemptAt, deadline)
	}
	if got := attemptAt.Sub(start); got > 5*time.Hour {
		t.Fatalf("retry span = %s, want at most five hours", got)
	}
	if (AppleConsumptionArgs{}).Kind() != KindAppleConsumption {
		t.Fatal("AppleConsumptionArgs kind is not stable")
	}
}
