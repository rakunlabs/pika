package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/rakunlabs/query"
)

// AuditEntry is one persisted audit record.
type AuditEntry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Action    string    `json:"action"`               // e.g. "PUT /api/v1/settings", "login.failed", "config.created"
	Actor     string    `json:"actor,omitempty"`      // user name or token name
	ActorType string    `json:"actor_type,omitempty"` // "user", "token", "anonymous"
	Target    string    `json:"target,omitempty"`     // path / resource the action touched
	Status    int       `json:"status,omitempty"`     // HTTP status for request-derived entries
	IP        string    `json:"ip,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

// AuditStorage persists audit entries.
type AuditStorage interface {
	Append(ctx context.Context, entries []AuditEntry) error
	List(ctx context.Context, q *query.Query) ([]AuditEntry, int64, error)
	DeleteBefore(ctx context.Context, t time.Time) (int, error)
}

// DefaultAuditRetention is how long audit entries are kept when the
// operator doesn't configure a value.
const DefaultAuditRetention = 90 * 24 * time.Hour

// minAuditRetention guards against a typo pruning the whole log.
const minAuditRetention = time.Hour

// AuditSettings is the runtime override for the audit log, stored in the
// settings row. Retention is a Go duration string ("2160h"); "0" keeps
// entries forever and an empty value falls back to audit.retention from
// the config file.
type AuditSettings struct {
	Retention string `json:"retention,omitempty"`
}

func (a *AuditSettings) Validate() error {
	if a == nil || a.Retention == "" {
		return nil
	}
	d, err := time.ParseDuration(a.Retention)
	if err != nil {
		return fmt.Errorf("audit.retention: %w: %w", err, ErrBadRequest)
	}
	if d < 0 || (d > 0 && d < minAuditRetention) {
		return fmt.Errorf("audit.retention: must be 0 or at least %s: %w", minAuditRetention, ErrBadRequest)
	}
	return nil
}

// AuditRetentionInfo describes the retention currently in effect.
type AuditRetentionInfo struct {
	// Retention is the effective window as a Go duration string.
	Retention string `json:"retention"`
	// Source is "settings" when overridden from the UI, else "config".
	Source string `json:"source"`
	// ConfigRetention is the value from the config file / env.
	ConfigRetention string `json:"config_retention"`
}

const (
	auditFlushInterval = 2 * time.Second
	auditBufferSize    = 4096
	auditPruneInterval = time.Hour
)

// auditLog buffers entries and writes them in batches so recording never
// blocks a request on storage.
type auditLog struct {
	mu        sync.Mutex
	buf       []AuditEntry
	dropped   int
	retention time.Duration
}

// newAuditID returns a sortable id: zero-padded unix nanos + random suffix,
// so the primary-key order is chronological.
func newAuditID(t time.Time) string {
	var r [4]byte
	_, _ = rand.Read(r[:])
	return padNanos(t.UnixNano()) + "-" + hex.EncodeToString(r[:])
}

func padNanos(n int64) string {
	s := strconv.FormatInt(n, 10)
	for len(s) < 20 {
		s = "0" + s
	}
	return s
}

// Audit records an entry. It never blocks; when the buffer is full the
// entry is dropped and counted.
func (s *Service) Audit(e AuditEntry) {
	if s.auditLog == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.ID = newAuditID(e.Time)

	s.auditLog.mu.Lock()
	if len(s.auditLog.buf) >= auditBufferSize {
		s.auditLog.dropped++
		s.auditLog.mu.Unlock()
		return
	}
	s.auditLog.buf = append(s.auditLog.buf, e)
	s.auditLog.mu.Unlock()
}

// SetAuditRetention sets the config-level retention used when settings
// don't override it; <= 0 keeps entries forever.
func (s *Service) SetAuditRetention(d time.Duration) {
	if s.auditLog == nil {
		return
	}
	s.auditLog.mu.Lock()
	s.auditLog.retention = d
	s.auditLog.mu.Unlock()
}

// AuditRetention returns the effective retention: the settings override
// when present, otherwise the config value.
func (s *Service) AuditRetention(ctx context.Context) AuditRetentionInfo {
	cfg := s.configAuditRetention()
	d, source := s.effectiveAuditRetention(ctx)
	return AuditRetentionInfo{
		Retention:       d.String(),
		Source:          source,
		ConfigRetention: cfg.String(),
	}
}

func (s *Service) configAuditRetention() time.Duration {
	if s.auditLog == nil {
		return 0
	}
	s.auditLog.mu.Lock()
	defer s.auditLog.mu.Unlock()
	return s.auditLog.retention
}

// effectiveAuditRetention reads the override from settings on every call
// so a change saved on any cluster node applies without a reload.
func (s *Service) effectiveAuditRetention(ctx context.Context) (time.Duration, string) {
	cfg := s.configAuditRetention()
	settings, err := s.Settings(ctx)
	if err != nil || settings == nil || settings.Audit == nil || settings.Audit.Retention == "" {
		return cfg, "config"
	}
	d, err := time.ParseDuration(settings.Audit.Retention)
	if err != nil {
		slog.Warn("audit: ignoring invalid retention in settings", "retention", settings.Audit.Retention, "error", err)
		return cfg, "config"
	}
	return d, "settings"
}

// FlushAudit writes buffered entries to storage.
func (s *Service) FlushAudit(ctx context.Context) {
	if s.auditLog == nil || s.store == nil {
		return
	}
	if s.canWriteBackground != nil && !s.canWriteBackground() {
		// Followers keep their buffer bounded by dropping the oldest
		// half; request-derived entries are recorded on the leader,
		// which executes every forwarded write.
		s.auditLog.mu.Lock()
		if len(s.auditLog.buf) > auditBufferSize/2 {
			s.auditLog.buf = append([]AuditEntry(nil), s.auditLog.buf[len(s.auditLog.buf)/2:]...)
		}
		s.auditLog.mu.Unlock()
		return
	}

	s.auditLog.mu.Lock()
	batch := s.auditLog.buf
	dropped := s.auditLog.dropped
	s.auditLog.buf = nil
	s.auditLog.dropped = 0
	s.auditLog.mu.Unlock()

	if dropped > 0 {
		slog.Warn("audit: buffer full, entries dropped", "dropped", dropped)
	}
	if len(batch) == 0 {
		return
	}
	if err := s.store.Audit().Append(ctx, batch); err != nil {
		slog.Error("audit: persisting entries failed", "count", len(batch), "error", err)
	}
}

// PruneAudit deletes entries older than the retention window.
func (s *Service) PruneAudit(ctx context.Context) {
	if s.auditLog == nil || s.store == nil {
		return
	}
	if s.canWriteBackground != nil && !s.canWriteBackground() {
		return
	}
	retention, _ := s.effectiveAuditRetention(ctx)
	if retention <= 0 {
		return
	}
	n, err := s.store.Audit().DeleteBefore(ctx, time.Now().Add(-retention))
	if err != nil {
		slog.Warn("audit: pruning failed", "error", err)
		return
	}
	if n > 0 {
		slog.Debug("audit: pruned entries", "removed", n)
	}
}

// ListAudit returns audit entries, newest first unless q sorts otherwise.
func (s *Service) ListAudit(ctx context.Context, q *query.Query) ([]AuditEntry, int64, error) {
	// Include what's still buffered so a just-made change is visible.
	s.FlushAudit(ctx)
	return s.store.Audit().List(ctx, q)
}

func (s *Service) startAuditWorker() {
	s.bgWorker.goLoop(func(ctx context.Context) {
		flush := time.NewTicker(auditFlushInterval)
		defer flush.Stop()
		prune := time.NewTicker(auditPruneInterval)
		defer prune.Stop()

		run := func(fn func(context.Context)) {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			fn(c)
		}

		for {
			select {
			case <-ctx.Done():
				run(s.FlushAudit)
				return
			case <-flush.C:
				run(s.FlushAudit)
			case <-prune.C:
				run(s.PruneAudit)
			}
		}
	})
}
