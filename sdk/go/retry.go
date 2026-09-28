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
	// MaxRetryAfter is the client budget for a server-requested Retry-After
	// cooldown. A longer cooldown is cut to this value, so the retry may still
	// land inside it; the budget never reduces the configured jitter. Every
	// IAPStack SDK uses 30 seconds.
	MaxRetryAfter = 30 * time.Second
)

// RetryPolicy is a bounded full-jitter exponential backoff policy.
//
// In Config, each zero field takes its DefaultRetryPolicy value, like Timeout,
// so RetryPolicy{MaxAttempts: 5} keeps the default backoff. This matches the
// other IAPStack SDKs, which default every omitted retry field. Go cannot tell
// an omitted field from an explicit zero, so Config cannot disable backoff;
// set MaxAttempts to 1 to disable retries instead.
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

// withDefaults fills each zero field from DefaultRetryPolicy.
func (policy RetryPolicy) withDefaults() RetryPolicy {
	defaults := DefaultRetryPolicy()
	if policy.MaxAttempts == 0 {
		policy.MaxAttempts = defaults.MaxAttempts
	}
	if policy.BaseDelay == 0 {
		policy.BaseDelay = defaults.BaseDelay
	}
	if policy.MaxDelay == 0 {
		policy.MaxDelay = defaults.MaxDelay
	}
	return policy
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

// DelayAfterResponse returns the delay after a failed one-based attempt whose
// response may have carried a Retry-After cooldown.
//
// A zero retryAfter behaves like DelayAfter. Otherwise the result is
// max(jitter, min(retryAfter, MaxRetryAfter)): the cooldown raises the wait up
// to the client budget and never shortens the jitter delay.
func (policy RetryPolicy) DelayAfterResponse(attempt int, randomValue float64, retryAfter time.Duration) (time.Duration, error) {
	delay, err := policy.DelayAfter(attempt, randomValue)
	if err != nil {
		return 0, err
	}
	if retryAfter <= 0 {
		return delay, nil
	}
	if retryAfter > MaxRetryAfter {
		retryAfter = MaxRetryAfter
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	return delay, nil
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
