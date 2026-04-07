// Package hook provides an event system for file and config operations.
// It dispatches events to configurable sinks (HTTP webhooks, Kafka, etc.)
// when files are created, updated, deleted, or otherwise modified.
package hook

import "time"

// EventType identifies the kind of operation that triggered the event.
type EventType string

const (
	// Raw filesystem events.
	EventFileCreated EventType = "file.created"
	EventFileUpdated EventType = "file.updated"
	EventFileDeleted EventType = "file.deleted"
	EventDirCreated  EventType = "dir.created"
	EventFileRenamed EventType = "file.renamed"
	EventFileCopied  EventType = "file.copied"

	// Config/data events.
	EventConfigCreated EventType = "config.created"
	EventConfigDeleted EventType = "config.deleted"
	EventConfigUpdated EventType = "config.updated"

	// Wildcard — matches all event types in a hook filter.
	EventAll EventType = "*"
)

// Event is the payload dispatched to sinks when an operation occurs.
type Event struct {
	// Type is the event type (e.g. "file.created").
	Type EventType `json:"type"`
	// Timestamp is when the event was generated.
	Timestamp time.Time `json:"timestamp"`
	// Hook is the name of the hook that matched this event.
	Hook string `json:"hook"`
	// Mount is the raw mount prefix (e.g. "uploads").
	Mount string `json:"mount,omitempty"`
	// Path is the file path within the mount.
	Path string `json:"path,omitempty"`
	// Size is the file size in bytes (if applicable).
	Size int64 `json:"size,omitempty"`
	// Protocol identifies how the operation was performed (http, ftp, sftp, tftp).
	Protocol string `json:"protocol,omitempty"`
	// User is the username that performed the operation (if available).
	User string `json:"user,omitempty"`

	// OldPath is set for rename events to indicate the original path.
	OldPath string `json:"old_path,omitempty"`
	// DstMount is set for cross-mount copy/rename events.
	DstMount string `json:"dst_mount,omitempty"`
	// DstPath is set for copy/rename events to indicate the destination path.
	DstPath string `json:"dst_path,omitempty"`

	// ConfigKey is the config file key (for config.* events).
	ConfigKey string `json:"config_key,omitempty"`
	// ConfigVersion is the config version number (for config.* events).
	ConfigVersion int64 `json:"config_version,omitempty"`
	// Variant is the variant name (for config.* events involving variants).
	Variant string `json:"variant,omitempty"`
}
