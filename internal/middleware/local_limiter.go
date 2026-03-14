package middleware

import (
	"sync"
	"time"
)

type keyedLocalLimiter struct {
	mu      sync.Mutex
	buckets map[string]*localTokenBucket
}

type localTokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newKeyedLocalLimiter() *keyedLocalLimiter {
	return &keyedLocalLimiter{
		buckets: make(map[string]*localTokenBucket),
	}
}

func newLocalTokenBucket(rate float64, burst int) *localTokenBucket {
	now := time.Now()
	return &localTokenBucket{
		rate:   rate,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   now,
	}
}

func (b *localTokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now

	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}

	if b.tokens < 1 {
		return false
	}

	b.tokens -= 1
	return true
}

func (l *keyedLocalLimiter) Allow(key string, rate float64, burst int) bool {
	l.mu.Lock()
	bucket, ok := l.buckets[key]
	if !ok {
		bucket = newLocalTokenBucket(rate, burst)
		l.buckets[key] = bucket
	}
	l.mu.Unlock()

	return bucket.Allow()
}
