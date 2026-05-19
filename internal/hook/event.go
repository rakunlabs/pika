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

	// Personal vault events. Item-level mutations emit the matching
	// vault.item.* event so operators can pipe them to an audit log
	// or trigger a backup. The user.* events cover lifecycle on the
	// vault account row itself.
	//
	// EncryptedPayload bodies are NEVER included in the event — only
	// the item id, type and the calling user. Operators can correlate
	// to the live vault via the id; the encrypted payload stays
	// inside the database.
	EventVaultItemCreated  EventType = "vault.item.created"
	EventVaultItemUpdated  EventType = "vault.item.updated"
	EventVaultItemDeleted  EventType = "vault.item.deleted"
	EventVaultUnlockFailed EventType = "vault.unlock.failed"

	// Artifact registry events. Mirror the file.* shape: semantic
	// fields (namespace, repository name, subject — module/package/
	// image:tag) carry the operation context; numeric fields carry
	// counts / bytes where meaningful. Body payloads (manifests,
	// tarballs, blobs) are NEVER included — the event tells you
	// what changed; the artifact is downloadable through the
	// regular /registries/* endpoints.
	//
	// Field mapping for registry events:
	//   Mount    — namespace
	//   Path     — "{repository}/{subject}"  (e.g. "mirror/lib/foo:1.2")
	//   Protocol — "registry-go" | "registry-npm" | "registry-docker"
	//   User     — actor (session user or token id)
	//   Size     — artifact size in bytes (publish events; 0 for delete)
	//
	// EventRegistryPublished fires when a local push completes
	// (Go upload, NPM publish, Docker manifest+tag PUT).
	// EventRegistryDeleted fires on tag/manifest/version delete.
	// EventRegistryCachePurged fires from the operator-triggered
	// purge endpoint. EventRegistryGCCompleted fires after Docker
	// mark-and-sweep finishes. The namespace/repo lifecycle
	// events report admin-side CRUD on the settings tree.
	EventRegistryPublished       EventType = "registry.published"
	EventRegistryDeleted         EventType = "registry.deleted"
	EventRegistryCachePurged     EventType = "registry.cache_purged"
	EventRegistryGCCompleted     EventType = "registry.gc_completed"
	EventRegistryNamespaceCreated EventType = "registry.namespace_created"
	EventRegistryNamespaceDeleted EventType = "registry.namespace_deleted"
	EventRegistryRepositoryCreated EventType = "registry.repository_created"
	EventRegistryRepositoryUpdated EventType = "registry.repository_updated"
	EventRegistryRepositoryDeleted EventType = "registry.repository_deleted"

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
