package service

import (
	"context"
	"errors"
	"time"

	"github.com/rakunlabs/query"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrBadRequest       = errors.New("bad request")
	ErrNoStorageBackend = errors.New("no storage backend configured")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrForbidden        = errors.New("forbidden")
	ErrConflict         = errors.New("conflict")
)

// Storage defines the top-level storage interface.
// Each entity has its own typed repository.
type Storage interface {
	Users() UserStorage
	Tokens() TokenStorage
	Sessions() SessionStorage
	Folders() FolderStorage
	Files() FileStorage
	FileVersions() FileVersionStorage
	Settings() SettingsStorage

	// Tx executes a function within a transaction.
	// If the function returns an error, the transaction is rolled back.
	Tx(ctx context.Context, fn func(ctx context.Context, tx Storage) error) error
}

// UserStorage manages user records.
type UserStorage interface {
	Create(ctx context.Context, user *User) error
	Get(ctx context.Context, id string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	List(ctx context.Context, q *query.Query) ([]User, int64, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int64, error)
}

// Session represents an active user session stored in the database.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionStorage manages user sessions.
type SessionStorage interface {
	Create(ctx context.Context, session *Session) error
	Get(ctx context.Context, id string) (*Session, error)
	Delete(ctx context.Context, id string) error
	DeleteByUserID(ctx context.Context, userID string) error
	DeleteExpired(ctx context.Context) error
	CountByUserID(ctx context.Context, userID string) (int64, error)
}

// TokenStorage manages access tokens.
type TokenStorage interface {
	Create(ctx context.Context, token *Token) error
	Get(ctx context.Context, id string) (*Token, error)
	FindByHash(ctx context.Context, hashedKey string) (*Token, error)
	List(ctx context.Context, q *query.Query) ([]Token, int64, error)
	Update(ctx context.Context, token *Token) error
	Delete(ctx context.Context, id string) error
}

// FolderEntry represents a single folder record for backup/export.
type FolderEntry struct {
	Path   string  `json:"path"`
	Folder *Folder `json:"folder"`
}

// FolderStorage manages folder records.
type FolderStorage interface {
	Get(ctx context.Context, path string) (*Folder, error)
	Set(ctx context.Context, path string, folder *Folder) error
	Delete(ctx context.Context, path string) error
	DeletePrefix(ctx context.Context, prefix string) error
	List(ctx context.Context) ([]FolderEntry, error)
	DeleteAll(ctx context.Context) error
}

// FileEntry represents a single file record (path + version) for backup/export.
type FileEntry struct {
	Path    string `json:"path"`
	Version int64  `json:"version"`
	File    *File  `json:"file"`
}

// FileStorage manages file content at specific versions.
type FileStorage interface {
	Get(ctx context.Context, path string, version int64) (*File, error)
	Set(ctx context.Context, path string, version int64, file *File) error
	Delete(ctx context.Context, path string, version int64) error
	DeleteAllVersions(ctx context.Context, path string) error
	DeletePrefix(ctx context.Context, prefix string) error
	List(ctx context.Context) ([]FileEntry, error)
	DeleteAll(ctx context.Context) error
}

// FileVersionEntry represents a single file version record for backup/export.
type FileVersionEntry struct {
	Path     string       `json:"path"`
	Versions FileVersions `json:"versions"`
}

// FileVersionStorage manages file version metadata.
type FileVersionStorage interface {
	Get(ctx context.Context, path string) (FileVersions, error)
	Set(ctx context.Context, path string, versions FileVersions) error
	Delete(ctx context.Context, path string) error
	DeletePrefix(ctx context.Context, prefix string) error
	List(ctx context.Context) ([]FileVersionEntry, error)
	DeleteAll(ctx context.Context) error
}

// SettingsStorage manages application settings (singleton).
type SettingsStorage interface {
	Get(ctx context.Context) (*Settings, error)
	Set(ctx context.Context, settings *Settings) error
}

// Folder represents a directory containing folders and files.
type Folder struct {
	Folders  []string            `json:"folders"`
	Files    []string            `json:"files"`
	Variants map[string][]string `json:"variants,omitempty"` // file name -> variant keys
}

// SearchResult represents a single search match.
type SearchResult struct {
	Path    string `json:"path"`              // config path
	Type    string `json:"type"`              // "name" or "content"
	Line    int    `json:"line,omitempty"`    // line number (content match)
	Snippet string `json:"snippet,omitempty"` // matching line text (content match)
}

// DataResult holds the resolved configuration data returned by GetData.
type DataResult struct {
	Data   []byte `json:"data"`
	Format string `json:"format"`
	Error  string `json:"error,omitempty"` // parse/conversion error message
}

// RenderResult holds the rendered configuration for preview.
type RenderResult struct {
	Data  string `json:"data"`            // base64 encoded
	Error string `json:"error,omitempty"` // parse/conversion error message for the UI
}
