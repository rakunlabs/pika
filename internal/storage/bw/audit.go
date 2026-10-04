package bw

import (
	"context"
	"time"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/query"

	"github.com/rakunlabs/pika/internal/service"
)

// auditRow stores one audit entry. The id is time-ordered (see
// service.newAuditID), so key order is chronological.
type auditRow struct {
	ID        string    `bw:"id,pk"`
	Time      time.Time `bw:"time,index"`
	Action    string    `bw:"action,index"`
	Actor     string    `bw:"actor,index"`
	ActorType string    `bw:"actor_type"`
	Target    string    `bw:"target"`
	Status    int       `bw:"status"`
	IP        string    `bw:"ip"`
	RequestID string    `bw:"request_id"`
	Detail    string    `bw:"detail"`
}

func (r *auditRow) toService() service.AuditEntry {
	return service.AuditEntry{
		ID:        r.ID,
		Time:      r.Time,
		Action:    r.Action,
		Actor:     r.Actor,
		ActorType: r.ActorType,
		Target:    r.Target,
		Status:    r.Status,
		IP:        r.IP,
		RequestID: r.RequestID,
		Detail:    r.Detail,
	}
}

func auditRowFromService(e *service.AuditEntry) *auditRow {
	return &auditRow{
		ID:        e.ID,
		Time:      e.Time,
		Action:    e.Action,
		Actor:     e.Actor,
		ActorType: e.ActorType,
		Target:    e.Target,
		Status:    e.Status,
		IP:        e.IP,
		RequestID: e.RequestID,
		Detail:    e.Detail,
	}
}

type auditStorage struct {
	store  *Storage
	bucket *bw.Bucket[auditRow]
	scope  scope
}

func (s *Storage) Audit() service.AuditStorage {
	return &auditStorage{store: s, bucket: s.audit, scope: s.dbScope()}
}

func (t *txStorage) Audit() service.AuditStorage {
	return &auditStorage{store: t.base, bucket: t.base.audit, scope: t.scope}
}

// Append writes a batch in a single transaction.
func (s *auditStorage) Append(ctx context.Context, entries []service.AuditEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if s.scope.tx != nil {
		for i := range entries {
			if err := bucketInsert(ctx, s.scope, s.bucket, auditRowFromService(&entries[i])); err != nil {
				return err
			}
		}
		return nil
	}
	return translateErr(s.store.db.Update(func(tx *bw.Tx) error {
		for i := range entries {
			if err := s.bucket.InsertTx(tx, auditRowFromService(&entries[i])); err != nil {
				return err
			}
		}
		return nil
	}))
}

func (s *auditStorage) List(ctx context.Context, q *query.Query) ([]service.AuditEntry, int64, error) {
	q = auditSortByID(q)
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, 0, err
	}
	out := make([]service.AuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toService())
	}

	var countQ *query.Query
	if q != nil {
		c := *q
		c.Offset = nil
		c.Limit = nil
		c.Sort = nil
		countQ = &c
	}
	total, err := bucketCount(ctx, s.scope, s.bucket, countQ)
	if err != nil {
		return nil, 0, err
	}
	return out, int64(total), nil
}

// DeleteBefore removes entries older than t.
func (s *auditStorage) DeleteBefore(ctx context.Context, t time.Time) (int, error) {
	q, err := query.Parse("time[lt]=" + t.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	err = s.store.db.Update(func(tx *bw.Tx) error {
		for _, r := range rows {
			if err := s.bucket.DeleteTx(tx, r.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return len(rows), translateErr(err)
}

// auditSortByID rewrites sorts on "time" to "id": bw can't order by a
// time.Time field, and ids are zero-padded unix nanos, so they sort in
// the same order. Without an explicit sort, newest entries come first.
func auditSortByID(q *query.Query) *query.Query {
	if q == nil {
		q = &query.Query{}
	}
	c := *q
	if len(c.Sort) == 0 {
		c.Sort = []query.ExpressionSort{{Field: "id", Desc: true}}
		return &c
	}
	c.Sort = append([]query.ExpressionSort(nil), q.Sort...)
	for i := range c.Sort {
		if c.Sort[i].Field == "time" {
			c.Sort[i].Field = "id"
		}
	}
	return &c
}
