package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// RawSession is the low-level session row, shared across auth adapters.
type RawSession struct {
	ID        string
	UserID    string
	Username  string
	Payload   []byte
	RefreshID string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// NewSessionID returns a fresh opaque session identifier.
func NewSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GetRawSession retrieves a session by ID, mapping from the service-layer Session struct.
func (s *Service) GetRawSession(ctx context.Context, id string) (*RawSession, error) {
	row, err := s.store.Sessions().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &RawSession{
		ID:        row.ID,
		UserID:    row.UserID,
		Username:  row.Username,
		Payload:   row.Payload,
		RefreshID: row.RefreshID,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}, nil
}

// PutRawSession creates or replaces a session row. If the session already
// exists it is deleted first, then re-created (SQLite has no generic UPSERT
// for this schema without altering the storage layer).
func (s *Service) PutRawSession(ctx context.Context, rs *RawSession) error {
	if rs.CreatedAt.IsZero() {
		rs.CreatedAt = time.Now()
	}
	sess := &Session{
		ID:        rs.ID,
		UserID:    rs.UserID,
		Username:  rs.Username,
		Payload:   rs.Payload,
		RefreshID: rs.RefreshID,
		ExpiresAt: rs.ExpiresAt,
		CreatedAt: rs.CreatedAt,
	}
	// Try create; if conflict, delete+create.
	err := s.store.Sessions().Create(ctx, sess)
	if err == nil {
		return nil
	}
	_ = s.store.Sessions().Delete(ctx, rs.ID)
	return s.store.Sessions().Create(ctx, sess)
}

// DeleteRawSession removes a session by ID.
func (s *Service) DeleteRawSession(ctx context.Context, id string) error {
	return s.store.Sessions().Delete(ctx, id)
}
