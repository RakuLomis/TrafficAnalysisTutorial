package transport

import (
	"sync"
	"time"
)

type tokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newTokenBucket(rate, burst int, now time.Time) *tokenBucket {
	if rate < 1 {
		rate = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &tokenBucket{
		rate:   float64(rate),
		burst:  float64(burst),
		tokens: float64(burst),
		last:   now,
	}
}

func (b *tokenBucket) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Before(b.last) {
		now = b.last
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
