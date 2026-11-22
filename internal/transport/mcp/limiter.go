package mcp

import (
	"sync"
)

// keyLimiter bounds in-flight requests per API key. It keeps an entry only while a key has a request in
// flight, so its memory is bounded by concurrent requests rather than by the number of keys ever seen.
type keyLimiter struct {
	mu       sync.Mutex
	limit    int
	inFlight map[string]int
}

// newKeyLimiter constructs a limiter that admits at most limit concurrent requests per key.
func newKeyLimiter(limit int) *keyLimiter {
	return &keyLimiter{limit: limit, inFlight: make(map[string]int)}
}

// acquire admits one request for key, or reports false when the key is at its limit.
func (limiter *keyLimiter) acquire(key string) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.inFlight[key] >= limiter.limit {
		return false
	}
	limiter.inFlight[key]++
	return true
}

// release ends one admitted request for key and drops the entry when none remain.
func (limiter *keyLimiter) release(key string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.inFlight[key] <= 1 {
		delete(limiter.inFlight, key)
		return
	}
	limiter.inFlight[key]--
}

// size reports how many keys currently have a request in flight.
func (limiter *keyLimiter) size() int {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return len(limiter.inFlight)
}
