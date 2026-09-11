package iapstack

import (
	"fmt"
	"time"
)

const (
	// defaultMaxAttempts includes the initial HTTP request.
	defaultMaxAttempts = 3
	// defaultBaseDelay is the full-jitter upper bound before the second attempt.
	defaultBaseDelay = 250 * time.Millisecond
	// defaultMaxDelay caps exponential backoff.
	defaultMaxDelay = 2 * time.Second
	// maxBackoffExponent prevents duration overflow in the retry multiplier.
	maxBackoffExponent = 20
)

// RetryPolicy is a bounded full-jitter exponential backoff policy.
type RetryPolicy struct {
	// MaxAttempts is the total attempts, including the initial request.
	MaxAttempts int
	// BaseDelay is the upper delay bound before the second attempt.
	BaseDelay time.Duration
	// MaxDelay is the upper delay bound for later attempts.
	MaxDelay time.Duration
}

// DefaultRetryPolicy returns the Flutter-aligned host retry defaults.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: defaultMaxAttempts,
		BaseDelay:   defaultBaseDelay,
		MaxDelay:    defaultMaxDelay,
	}
}

// DelayAfter returns a full-jitter delay after a failed one-based attempt.
func (policy RetryPolicy) DelayAfter(attempt int, randomValue float64) (time.Duration, error) {
	if randomValue < 0 || randomValue > 1 {
		return 0, fmt.Errorf("randomValue must be between 0 and 1")
	}
	if policy.BaseDelay == 0 {
		return 0, nil
	}
	exponent := attempt - 1
	if exponent < 0 {
		exponent = 0
	}
	if exponent > maxBackoffExponent {
		exponent = maxBackoffExponent
	}
	delay := policy.BaseDelay * time.Duration(1<<uint(exponent))
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	return time.Duration(float64(delay) * randomValue), nil
}

// Validate reports whether retry bounds are safe.
func (policy RetryPolicy) Validate() error {
	if policy.MaxAttempts < 1 || policy.MaxAttempts > 5 {
		return fmt.Errorf("retry max attempts must be between 1 and 5")
	}
	if policy.BaseDelay < 0 || policy.MaxDelay < 0 || policy.BaseDelay > policy.MaxDelay {
		return fmt.Errorf("retry delays must be non-negative and ordered")
	}
	return nil
}
