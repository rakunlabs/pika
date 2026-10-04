package service_test

import (
	"testing"
	"time"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/pika/internal/service"
)

func TestAuditRecordListPrune(t *testing.T) {
	svc := newServiceOn(t, newTokenTestStore(t))
	ctx := t.Context()

	old := time.Now().Add(-48 * time.Hour).UTC()
	svc.Audit(service.AuditEntry{Time: old, Action: "config.deleted", Actor: "bob", Target: "/a"})
	svc.Audit(service.AuditEntry{Action: "POST /api/v1/file/*", Actor: "alice", Target: "/api/v1/file/b", Status: 200})

	q, _ := query.Parse("_sort=-time")
	entries, total, err := svc.ListAudit(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || entries[0].Actor != "alice" {
		t.Fatalf("entries=%+v total=%d", entries, total)
	}

	q, _ = query.Parse("actor=bob")
	if _, total, _ := svc.ListAudit(ctx, q); total != 1 {
		t.Fatalf("filter by actor: total=%d", total)
	}

	svc.SetAuditRetention(24 * time.Hour)
	svc.PruneAudit(ctx)
	if _, total, _ := svc.ListAudit(ctx, nil); total != 1 {
		t.Fatalf("after prune total=%d, want 1", total)
	}

	svc.SetAuditRetention(0)
	svc.PruneAudit(ctx) // no-op, must not delete
	if _, total, _ := svc.ListAudit(ctx, nil); total != 1 {
		t.Fatalf("retention 0 deleted entries: total=%d", total)
	}
}

func TestAuditGateKeepsBufferBounded(t *testing.T) {
	svc := newServiceOn(t, newTokenTestStore(t))
	svc.SetBackgroundWriteGate(func() bool { return false })
	svc.Audit(service.AuditEntry{Action: "x"})
	svc.FlushAudit(t.Context())

	svc.SetBackgroundWriteGate(nil)
	if _, total, _ := svc.ListAudit(t.Context(), nil); total != 1 {
		t.Fatalf("entry recorded while gated should flush once allowed: total=%d", total)
	}
}
