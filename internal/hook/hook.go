package hook

import "path/filepath"

// ServiceName and Version identify this service in HTTP User-Agent headers
// and Kafka client software name. Set from the config package at startup.
var (
	ServiceName = "pika"
	Version     = "v0.0.0"
)

// UserAgent returns the service identifier string (e.g. "pika/v0.1.0").
func UserAgent() string {
	return ServiceName + "/" + Version
}

// Hook defines an event hook with filters and one or more targets.
// Hooks are stored in the database via Settings and managed through the UI.
type Hook struct {
	// Name is the unique identifier for this hook.
	Name string `json:"name"`
	// Enabled toggles the hook on or off.
	Enabled bool `json:"enabled"`
	// Events lists the event types this hook listens for.
	// Use "*" to match all event types.
	Events []EventType `json:"events"`
	// Filter restricts which operations trigger this hook.
	Filter HookFilter `json:"filter,omitempty"`
	// Targets lists the destinations where events are pushed.
	Targets []Target `json:"targets"`
}

// HookFilter restricts which events a hook receives.
type HookFilter struct {
	// Mounts restricts to specific mount prefixes. Empty means all mounts.
	Mounts []string `json:"mounts,omitempty"`
	// PathPattern is a glob pattern for matching file paths (e.g. "**/*.pdf").
	// Empty means all paths.
	PathPattern string `json:"path_pattern,omitempty"`
}

// Target defines a single push destination for hook events.
// Exactly one of HTTP or Kafka should be set.
type Target struct {
	// Type identifies the target kind: "http" or "kafka".
	Type string `json:"type"`
	// HTTP holds configuration for HTTP webhook targets.
	HTTP *HTTPTarget `json:"http,omitempty"`
	// Kafka holds configuration for Kafka targets.
	Kafka *KafkaTarget `json:"kafka,omitempty"`
	// BodyTemplate is an optional Go text/template string for customizing the
	// event payload. When empty, the default JSON payload is used.
	BodyTemplate string `json:"body_template,omitempty"`
}

// HTTPTarget configures an HTTP webhook target.
type HTTPTarget struct {
	// URL is the endpoint to send events to.
	URL string `json:"url"`
	// Method is the HTTP method (default: "POST").
	Method string `json:"method,omitempty"`
	// Headers are extra HTTP headers to include in the request.
	Headers map[string]string `json:"headers,omitempty"`
	// Timeout is the request timeout (e.g. "10s"). Default: "30s".
	Timeout string `json:"timeout,omitempty"`
}

// KafkaTarget configures a Kafka producer target.
type KafkaTarget struct {
	// Brokers is the list of Kafka broker addresses.
	Brokers []string `json:"brokers"`
	// Topic is the Kafka topic to produce messages to.
	Topic string `json:"topic"`
	// KeyTemplate is an optional Go template for the Kafka message key.
	// Default: "{{.Mount}}/{{.Path}}".
	KeyTemplate string `json:"key_template,omitempty"`
	// AutoTopicCreation enables automatic topic creation via the broker.
	// Defaults to true. Set to false to disable.
	// Requires the broker to have auto.create.topics.enable=true.
	AutoTopicCreation *bool `json:"auto_topic_creation,omitempty"`
	// Security holds TLS and SASL authentication settings.
	Security KafkaSecurity `json:"security,omitempty"`
}

// KafkaSecurity contains TLS and SASL configuration for Kafka connections.
type KafkaSecurity struct {
	// TLS configures TLS encryption for the Kafka connection.
	TLS KafkaTLS `json:"tls,omitempty"`
	// SASL configures SASL authentication mechanisms.
	// Multiple mechanisms can be specified; they are tried in order.
	SASL []KafkaSASL `json:"sasl,omitempty"`
}

// KafkaTLS configures TLS for the Kafka connection.
//
// Each certificate field supports three modes:
//   - File path: e.g. "/etc/ssl/ca.pem"
//   - PEM content: paste the PEM text directly into the _pem field
//   - Reference: "raw://mount/path" reads from a raw mount,
//     "config://file/path" reads from the config store.
//
// When both a file path and PEM content are provided, PEM content takes precedence.
type KafkaTLS struct {
	// Enabled activates TLS for the connection.
	Enabled bool `json:"enabled,omitempty"`
	// CertFile is the path to the client TLS certificate file.
	CertFile string `json:"cert_file,omitempty"`
	// CertPEM is the client TLS certificate PEM content (inline or raw://... or config://...).
	CertPEM string `json:"cert_pem,omitempty"`
	// KeyFile is the path to the client TLS private key file.
	KeyFile string `json:"key_file,omitempty"`
	// KeyPEM is the client TLS private key PEM content (inline or raw://... or config://...).
	KeyPEM string `json:"key_pem,omitempty"`
	// CAFile is the path to the CA certificate file.
	CAFile string `json:"ca_file,omitempty"`
	// CAPEM is the CA certificate PEM content (inline or raw://... or config://...).
	CAPEM string `json:"ca_pem,omitempty"`
}

// KafkaSASL configures a single SASL authentication mechanism.
// Exactly one of Plain or SCRAM should be enabled.
type KafkaSASL struct {
	// Plain configures SASL/PLAIN authentication.
	Plain *KafkaSASLPlain `json:"plain,omitempty"`
	// SCRAM configures SASL/SCRAM authentication.
	SCRAM *KafkaSASLSCRAM `json:"scram,omitempty"`
}

// KafkaSASLPlain configures SASL/PLAIN authentication.
type KafkaSASLPlain struct {
	// Enabled activates this mechanism.
	Enabled bool `json:"enabled,omitempty"`
	// User is the SASL username.
	User string `json:"user,omitempty"`
	// Pass is the SASL password.
	Pass string `json:"pass,omitempty"`
}

// KafkaSASLSCRAM configures SASL/SCRAM authentication.
type KafkaSASLSCRAM struct {
	// Enabled activates this mechanism.
	Enabled bool `json:"enabled,omitempty"`
	// Algorithm must be "SCRAM-SHA-256" or "SCRAM-SHA-512".
	Algorithm string `json:"algorithm,omitempty"`
	// User is the SASL username.
	User string `json:"user,omitempty"`
	// Pass is the SASL password.
	Pass string `json:"pass,omitempty"`
	// IsToken indicates the user/pass are from a delegation token.
	IsToken bool `json:"is_token,omitempty"`
}

// Matches reports whether the hook should fire for the given event type, mount, and path.
func (h *Hook) Matches(eventType EventType, mount, path string) bool {
	if !h.Enabled {
		return false
	}

	// Check event type
	if !h.matchesEvent(eventType) {
		return false
	}

	// Check mount filter
	if len(h.Filter.Mounts) > 0 {
		found := false
		for _, m := range h.Filter.Mounts {
			if m == mount {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check path pattern
	if h.Filter.PathPattern != "" {
		matched, err := filepath.Match(h.Filter.PathPattern, path)
		if err != nil || !matched {
			// Also try matching with doublestar-like behavior: match against the base name
			matched2, err2 := filepath.Match(h.Filter.PathPattern, filepath.Base(path))
			if err2 != nil || !matched2 {
				return false
			}
		}
	}

	return true
}

// matchesEvent checks if the hook listens for the given event type.
func (h *Hook) matchesEvent(eventType EventType) bool {
	for _, e := range h.Events {
		if e == EventAll || e == eventType {
			return true
		}
	}
	return false
}
