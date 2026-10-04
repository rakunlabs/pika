package service

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerStop(t *testing.T) {
	w := newWorker()
	var ticks atomic.Int32
	w.every(time.Millisecond, func() { ticks.Add(1) })

	deadline := time.Now().Add(time.Second)
	for ticks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	w.stop()
	after := ticks.Load()
	time.Sleep(10 * time.Millisecond)
	if ticks.Load() != after {
		t.Fatal("worker kept ticking after stop")
	}
	w.stop() // idempotent

	var nilWorker *worker
	nilWorker.stop()
}

func TestSlidingWindowLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newSlidingWindowLimiter(2, time.Minute)
	l.now = func() time.Time { return now }

	for i := range 2 {
		if !l.allow("a") {
			t.Fatalf("attempt %d should pass", i+1)
		}
	}
	if l.allow("a") {
		t.Fatal("third attempt should be limited")
	}
	if !l.allow("b") {
		t.Fatal("keys are independent")
	}

	now = now.Add(61 * time.Second)
	if !l.allow("a") {
		t.Fatal("window should have slid")
	}

	now = now.Add(2 * time.Minute)
	l.gc()
	if len(l.buckets) != 0 {
		t.Fatalf("gc left %d buckets", len(l.buckets))
	}
}

func TestSetVaultServiceStopsPrevious(t *testing.T) {
	s := New(nil)
	first := NewVaultService(s)
	s.SetVaultService(first)
	s.SetVaultService(NewVaultService(s))

	select {
	case <-first.worker.ctx.Done():
	default:
		t.Fatal("replaced vault service worker not stopped")
	}

	s.Close()
	if s.VaultCoord() != nil {
		t.Fatal("Close should detach coordinators")
	}
}
