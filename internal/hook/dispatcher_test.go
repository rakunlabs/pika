package hook

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSink struct {
	mu     sync.Mutex
	sent   int
	delay  time.Duration
	closed atomic.Bool
}

func (f *fakeSink) Send(ctx context.Context, _ []byte, _ string) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	f.sent++
	f.mu.Unlock()
	return nil
}

func (f *fakeSink) Close() error {
	f.closed.Store(true)
	return nil
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent
}

func dispatcherWithSinks(t *testing.T, sinks ...*fakeSink) *Dispatcher {
	t.Helper()
	d := NewDispatcher(16)
	hi := hookInstance{hook: Hook{Name: "test", Enabled: true, Events: []EventType{EventAll}}}
	for _, s := range sinks {
		hi.sinks = append(hi.sinks, newSinkEntry(s, "fake", "test"))
		hi.bodyTemplates = append(hi.bodyTemplates, nil)
		hi.keyTemplates = append(hi.keyTemplates, nil)
	}
	d.hooks = []hookInstance{hi}
	d.SetEventLogEnabled(false)
	d.Start(t.Context())
	return d
}

func TestDispatcherSlowSinkDoesNotBlockOthers(t *testing.T) {
	slow := &fakeSink{delay: time.Second}
	fast := &fakeSink{}
	d := dispatcherWithSinks(t, slow, fast)

	for range 3 {
		d.Emit(Event{Type: EventConfigUpdated, Path: "a"})
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for fast.count() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := fast.count(); got != 3 {
		t.Fatalf("fast sink got %d events while slow sink was busy, want 3", got)
	}
}

func TestDispatcherStopClosesSinksAndIgnoresLateEmit(t *testing.T) {
	s := &fakeSink{}
	d := dispatcherWithSinks(t, s)

	d.Emit(Event{Type: EventConfigUpdated, Path: "a"})
	d.Stop()

	if !s.closed.Load() {
		t.Fatal("sink not closed on Stop")
	}
	if s.count() != 1 {
		t.Fatalf("queued event not delivered before close: sent=%d", s.count())
	}

	// Must not panic on a closed channel.
	d.Emit(Event{Type: EventConfigUpdated, Path: "b"})
	d.Stop()
}

func TestDispatcherStopWithoutStart(t *testing.T) {
	d := NewDispatcher(1)
	d.Stop()
}
