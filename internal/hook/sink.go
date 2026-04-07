package hook

import "context"

// Sink is the interface for event delivery backends.
// Implementations send events to external systems (HTTP endpoints, Kafka topics, etc.).
type Sink interface {
	// Send delivers the rendered payload to the external system.
	// The payload is the event body after Go-template rendering.
	// key is an optional message key (used by Kafka for partitioning).
	Send(ctx context.Context, payload []byte, key string) error

	// Close releases any resources held by the sink.
	Close() error
}
