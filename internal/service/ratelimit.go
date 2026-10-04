package service

import (
	"sync"
	"time"
)

// slidingWindowLimiter allows at most max events per key within window.
// Each key keeps at most max timestamps, so memory per key is bounded;
// gc drops idle keys.
type slidingWindowLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	buckets map[string][]time.Time
	now     func() time.Time
}

func newSlidingWindowLimiter(max int, window time.Duration) *slidingWindowLimiter {
	return &slidingWindowLimiter{
		max:     max,
		window:  window,
		buckets: make(map[string][]time.Time),
		now:     time.Now,
	}
}

// allow records an event for key and reports whether it is within the limit.
func (l *slidingWindowLimiter) allow(key string) bool {
	now := l.now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	kept := pruneBefore(l.buckets[key], cutoff)
	if len(kept) >= l.max {
		l.buckets[key] = kept
		return false
	}
	l.buckets[key] = append(kept, now)
	return true
}

// gc removes keys without events inside the window.
func (l *slidingWindowLimiter) gc() {
	cutoff := l.now().Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	for k, ts := range l.buckets {
		if len(pruneBefore(ts, cutoff)) == 0 {
			delete(l.buckets, k)
		}
	}
}

func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	kept := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}
