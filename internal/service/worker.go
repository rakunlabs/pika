package service

import (
	"context"
	"sync"
	"time"
)

// worker runs a background goroutine that can be stopped. Coordinators
// embed it so replacing one (auth reload) or shutting down doesn't leak
// its loops.
type worker struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newWorker() *worker {
	ctx, cancel := context.WithCancel(context.Background())
	return &worker{ctx: ctx, cancel: cancel}
}

// goLoop starts fn in a goroutine; fn must return once ctx is done.
func (w *worker) goLoop(fn func(ctx context.Context)) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		fn(w.ctx)
	}()
}

// every calls fn on each tick of interval until stopped.
func (w *worker) every(interval time.Duration, fn func()) {
	w.goLoop(func(ctx context.Context) {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fn()
			}
		}
	})
}

// stop cancels the worker and waits for its goroutines. Safe to call
// more than once and on a nil receiver.
func (w *worker) stop() {
	if w == nil {
		return
	}
	w.cancel()
	w.wg.Wait()
}
