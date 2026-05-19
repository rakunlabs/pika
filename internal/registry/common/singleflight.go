package common

import (
	"sync"
)

// Singleflight coalesces concurrent calls that compute the same
// expensive value, so only one goroutine actually does the work and
// the rest wait for the result.
//
// Used by every metadata cache (NPM packument, Go @v/list, Docker
// referrers index) to keep rebuild storms from happening when a hot
// package is requested by N clients in parallel right after a
// publish-time invalidation.
//
// Why not golang.org/x/sync/singleflight
//
// The semantics we need are slightly narrower: keys are strings, the
// result is bytes-or-error, and we want to be able to forget a key
// after it's done so a stale cache miss doesn't block its successor.
// Re-implementing in 40 lines is cheaper than pulling in another
// dep and writing wrappers.
type Singleflight struct {
	mu      sync.Mutex
	pending map[string]*sfCall
}

type sfCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// NewSingleflight constructs an empty coalescer.
func NewSingleflight() *Singleflight {
	return &Singleflight{pending: make(map[string]*sfCall)}
}

// Do calls fn at most once per key while previous invocations are
// in flight. The result (val, err) is shared with every caller that
// arrived during the in-flight window. Once fn returns the key is
// removed from the pending table so the next request starts fresh.
//
// The returned `shared` flag is true when this call observed an in-
// flight execution started by another goroutine. Useful for metrics.
func (s *Singleflight) Do(key string, fn func() (any, error)) (val any, err error, shared bool) {
	s.mu.Lock()
	if existing, ok := s.pending[key]; ok {
		s.mu.Unlock()
		existing.wg.Wait()
		return existing.val, existing.err, true
	}
	call := &sfCall{}
	call.wg.Add(1)
	s.pending[key] = call
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
		call.wg.Done()
	}()

	call.val, call.err = fn()
	return call.val, call.err, false
}
