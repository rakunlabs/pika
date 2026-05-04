package usersync

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

// Scheduler manages one goroutine per enabled SyncSource with mode=interval.
// Manual sources (or disabled ones) are tracked in `last` for status reporting
// but consume no goroutine. Reload diffs the new settings against running
// jobs and starts/stops as needed.
//
// All public methods are safe to call from any goroutine.
type Scheduler struct {
	syncer *Syncer

	mu      sync.Mutex
	jobs    map[string]*job   // source ID -> running job
	last    map[string]Report // source ID -> most recent run summary (any source, including manual)
	running map[string]bool   // source ID -> a Run is currently in flight (prevents overlap)
	appCtx  context.Context
}

type job struct {
	cancel context.CancelFunc
	source service.SyncSource
}

// NewScheduler returns a Scheduler bound to the given service. Call Start
// once at boot, then Reload after every settings change.
func NewScheduler(svc *service.Service) *Scheduler {
	return &Scheduler{
		syncer:  New(svc),
		jobs:    make(map[string]*job),
		last:    make(map[string]Report),
		running: make(map[string]bool),
	}
}

// Start records the parent context and applies the initial settings.
// Safe to call before settings are loaded — Reload(nil) is a no-op.
func (s *Scheduler) Start(ctx context.Context, settings *service.UserSyncSettings) {
	s.mu.Lock()
	s.appCtx = ctx
	s.mu.Unlock()
	s.Reload(settings)
}

// Reload diffs the running jobs against the desired set in `settings`:
//   - sources that disappeared or are now disabled: stop their goroutine
//   - sources that arrived or had their schedule changed: (re)start
//   - sources that are unchanged: leave alone
//
// Manual sources (Schedule.Mode != "interval") never spawn a goroutine
// regardless of Enabled — they're triggered exclusively by RunNow.
func (s *Scheduler) Reload(settings *service.UserSyncSettings) {
	desired := map[string]service.SyncSource{}
	if settings != nil {
		for _, src := range settings.Sources {
			if !src.Enabled {
				continue
			}
			if src.Schedule.Mode != "interval" {
				continue
			}
			if src.Schedule.IntervalMinutes < 1 {
				continue
			}
			desired[src.ID] = src
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop jobs that are no longer desired or whose config changed.
	for id, j := range s.jobs {
		want, ok := desired[id]
		if !ok || !sameSchedule(j.source, want) {
			j.cancel()
			delete(s.jobs, id)
		}
	}

	// Start jobs that are desired but not running.
	for id, src := range desired {
		if _, exists := s.jobs[id]; exists {
			continue
		}
		ctx, cancel := context.WithCancel(s.appCtx)
		s.jobs[id] = &job{cancel: cancel, source: src}
		go s.runLoop(ctx, src)
	}
}

// Stop cancels every running job and waits no goroutines behind. Used at
// shutdown — the appCtx cancellation already terminates the loops, but
// calling Stop explicitly clears the bookkeeping so subsequent Reloads
// from a re-Start would see a clean slate.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, j := range s.jobs {
		j.cancel()
		delete(s.jobs, id)
	}
}

// runLoop ticks at the source's configured cadence. Each tick triggers
// one Run; overlap is prevented (a long-running sync that overflows the
// next tick is skipped, not stacked).
func (s *Scheduler) runLoop(ctx context.Context, src service.SyncSource) {
	interval := time.Duration(src.Schedule.IntervalMinutes) * time.Minute
	if interval < time.Minute {
		interval = time.Minute
	}

	slog.Info("usersync: scheduler started", "source", src.ID, "interval", interval)

	// Fire once on startup so a freshly-configured source doesn't have to
	// wait a full interval for its first run.
	s.tick(ctx, src)

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("usersync: scheduler stopped", "source", src.ID)
			return
		case <-t.C:
			s.tick(ctx, src)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context, src service.SyncSource) {
	if !s.tryAcquire(src.ID) {
		slog.Warn("usersync: skipping tick — previous run still in progress", "source", src.ID)
		return
	}
	defer s.release(src.ID)

	report := s.syncer.Run(ctx, src)
	s.recordLast(src.ID, report)
	if len(report.Errors) > 0 {
		slog.Error("usersync: run completed with errors",
			"source", src.ID,
			"found", report.Found, "created", report.Created, "updated", report.Updated, "disabled", report.Disabled,
			"error_count", len(report.Errors))
	} else {
		slog.Info("usersync: run completed",
			"source", src.ID,
			"found", report.Found, "created", report.Created, "updated", report.Updated, "disabled", report.Disabled)
	}
}

// RunNow triggers an immediate, synchronous sync. Used by the "Sync now"
// API endpoint. Returns the Report (caller renders it for the UI).
//
// If a periodic run is already in flight for the same source, RunNow
// waits for it: returning immediately would lie about the current state.
// We use a short retry loop instead of blocking forever to keep the
// HTTP request bounded.
func (s *Scheduler) RunNow(ctx context.Context, src service.SyncSource) Report {
	for i := 0; i < 30; i++ {
		if s.tryAcquire(src.ID) {
			defer s.release(src.ID)
			report := s.syncer.Run(ctx, src)
			s.recordLast(src.ID, report)
			return report
		}
		select {
		case <-ctx.Done():
			return Report{SourceID: src.ID, Errors: []string{"cancelled while waiting for in-progress run"}}
		case <-time.After(time.Second):
		}
	}
	return Report{SourceID: src.ID, Errors: []string{"timed out waiting for in-progress run"}}
}

// LastReport returns the most recent run summary for a source, plus
// whether any record exists. Used by the status endpoint.
func (s *Scheduler) LastReport(sourceID string) (Report, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.last[sourceID]
	return r, ok
}

// AllReports returns a snapshot of every recorded last-run summary.
func (s *Scheduler) AllReports() map[string]Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Report, len(s.last))
	for k, v := range s.last {
		out[k] = v
	}
	return out
}

func (s *Scheduler) tryAcquire(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *Scheduler) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, id)
}

func (s *Scheduler) recordLast(id string, r Report) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last[id] = r
}

// sameSchedule reports whether two sources would produce the same ticker
// behavior. We restart on any other config change too, since LDAP/attr
// changes need a fresh search anyway — and the Reload contract is
// "convergent within one Reload call", not "minimal restart".
func sameSchedule(a, b service.SyncSource) bool {
	return a.Schedule.Mode == b.Schedule.Mode &&
		a.Schedule.IntervalMinutes == b.Schedule.IntervalMinutes
}
