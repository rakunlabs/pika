package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"text/template"
	"time"
)

// Dispatcher receives events and fans them out to matching hooks and their sinks.
type Dispatcher struct {
	mu        sync.RWMutex
	hooks     []hookInstance
	ch        chan Event
	done      chan struct{}
	pool      *kafkaClientPool
	resolver  *Resolver
	parentCtx context.Context
}

// hookInstance pairs a hook definition with compiled templates and live sinks.
type hookInstance struct {
	hook          Hook
	sinks         []sinkEntry
	bodyTemplates []*template.Template // per-target, nil means use default JSON
	keyTemplates  []*template.Template // per-target (Kafka only), nil means default
}

// sinkEntry pairs a sink with its target index for logging.
type sinkEntry struct {
	sink       Sink
	targetType string
}

// NewDispatcher creates a dispatcher with the given buffer size.
func NewDispatcher(bufferSize int) *Dispatcher {
	if bufferSize <= 0 {
		bufferSize = 256
	}
	return &Dispatcher{
		ch:   make(chan Event, bufferSize),
		done: make(chan struct{}),
		pool: newKafkaClientPool(),
	}
}

// SetResolver sets the reference resolver for raw:// and config:// PEM references.
func (d *Dispatcher) SetResolver(r *Resolver) {
	d.mu.Lock()
	d.resolver = r
	d.mu.Unlock()
}

// Start begins the background event processing loop.
func (d *Dispatcher) Start(ctx context.Context) {
	d.parentCtx = ctx
	go d.loop(ctx)
}

// Stop signals the dispatcher to shut down and waits for completion.
func (d *Dispatcher) Stop() {
	close(d.ch)
	<-d.done
}

// Emit sends an event to the dispatcher for async processing.
// If the buffer is full, the event is dropped with a warning log.
func (d *Dispatcher) Emit(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	select {
	case d.ch <- event:
	default:
		slog.Warn("hook event buffer full, dropping event",
			"type", event.Type,
			"mount", event.Mount,
			"path", event.Path,
		)
	}
}

// UpdateHooks replaces all hooks. It closes old sinks and builds new ones.
// Kafka targets that share the same brokers and security config will reuse
// a single underlying client connection from the pool.
func (d *Dispatcher) UpdateHooks(hooks []Hook) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Close old sinks (this releases pool references)
	for _, hi := range d.hooks {
		for _, se := range hi.sinks {
			if err := se.sink.Close(); err != nil {
				slog.Warn("error closing hook sink", "error", err)
			}
		}
	}

	d.hooks = nil

	ctx := d.parentCtx
	if ctx == nil {
		ctx = context.Background()
	}

	for _, h := range hooks {
		if !h.Enabled {
			continue
		}

		hi := hookInstance{hook: h}

		for _, t := range h.Targets {
			sink, err := d.buildSink(ctx, t)
			if err != nil {
				slog.Error("failed to build hook sink",
					"hook", h.Name,
					"target_type", t.Type,
					"error", err,
				)
				continue
			}

			hi.sinks = append(hi.sinks, sinkEntry{
				sink:       sink,
				targetType: t.Type,
			})

			// Compile body template
			var bodyTmpl *template.Template
			if t.BodyTemplate != "" {
				tmpl, err := template.New("body").Parse(t.BodyTemplate)
				if err != nil {
					slog.Error("failed to compile body template",
						"hook", h.Name,
						"target_type", t.Type,
						"error", err,
					)
					// Use default JSON payload as fallback
				} else {
					bodyTmpl = tmpl
				}
			}
			hi.bodyTemplates = append(hi.bodyTemplates, bodyTmpl)

			// Compile key template (Kafka only)
			var keyTmpl *template.Template
			if t.Type == "kafka" && t.Kafka != nil && t.Kafka.KeyTemplate != "" {
				tmpl, err := template.New("key").Parse(t.Kafka.KeyTemplate)
				if err != nil {
					slog.Error("failed to compile key template",
						"hook", h.Name,
						"error", err,
					)
				} else {
					keyTmpl = tmpl
				}
			}
			hi.keyTemplates = append(hi.keyTemplates, keyTmpl)
		}

		if len(hi.sinks) > 0 {
			d.hooks = append(d.hooks, hi)
		}
	}

	slog.Info("hooks updated", "active_hooks", len(d.hooks))
}

// loop processes events from the channel until it is closed.
func (d *Dispatcher) loop(ctx context.Context) {
	defer close(d.done)

	for event := range d.ch {
		d.dispatch(ctx, event)
	}

	// Cleanup: close all sinks and pool
	d.mu.Lock()
	for _, hi := range d.hooks {
		for _, se := range hi.sinks {
			if err := se.sink.Close(); err != nil {
				slog.Warn("error closing hook sink on shutdown", "error", err)
			}
		}
	}
	d.hooks = nil
	d.pool.closeAll()
	d.mu.Unlock()
}

// dispatch sends an event to all matching hooks.
func (d *Dispatcher) dispatch(ctx context.Context, event Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, hi := range d.hooks {
		if !hi.hook.Matches(event.Type, event.Mount, event.Path) {
			continue
		}

		// Set the hook name in the event
		eventCopy := event
		eventCopy.Hook = hi.hook.Name

		for i, se := range hi.sinks {
			payload, key, err := renderPayload(eventCopy, hi.bodyTemplates[i], hi.keyTemplates[i])
			if err != nil {
				slog.Error("failed to render hook payload",
					"hook", hi.hook.Name,
					"target_type", se.targetType,
					"error", err,
				)
				continue
			}

			if err := se.sink.Send(ctx, payload, key); err != nil {
				slog.Error("failed to send hook event",
					"hook", hi.hook.Name,
					"target_type", se.targetType,
					"event_type", event.Type,
					"error", err,
				)
			} else {
				slog.Debug("hook event sent",
					"hook", hi.hook.Name,
					"target_type", se.targetType,
					"event_type", event.Type,
					"mount", event.Mount,
					"path", event.Path,
				)
			}
		}
	}
}

// renderPayload renders the event payload and key using the provided templates.
// If bodyTmpl is nil, the default JSON marshaling is used.
// If keyTmpl is nil, the default key "mount/path" is used.
func renderPayload(event Event, bodyTmpl, keyTmpl *template.Template) ([]byte, string, error) {
	var payload []byte
	if bodyTmpl != nil {
		var buf bytes.Buffer
		if err := bodyTmpl.Execute(&buf, event); err != nil {
			return nil, "", fmt.Errorf("executing body template: %w", err)
		}
		payload = buf.Bytes()
	} else {
		var err error
		payload, err = json.Marshal(event)
		if err != nil {
			return nil, "", fmt.Errorf("marshaling event: %w", err)
		}
	}

	key := event.Mount + "/" + event.Path
	if keyTmpl != nil {
		var buf bytes.Buffer
		if err := keyTmpl.Execute(&buf, event); err != nil {
			return nil, "", fmt.Errorf("executing key template: %w", err)
		}
		key = buf.String()
	}

	return payload, key, nil
}

// buildSink creates a Sink from a Target configuration.
func (d *Dispatcher) buildSink(ctx context.Context, t Target) (Sink, error) {
	switch t.Type {
	case "http":
		return NewHTTPSink(t.HTTP)
	case "kafka":
		return NewKafkaSink(ctx, t.Kafka, d.pool, d.resolver)
	case "redis":
		return NewRedisSink(ctx, t.Redis, d.resolver)
	case "nats":
		return NewNATSSink(t.NATS)
	default:
		return nil, fmt.Errorf("unknown target type %q", t.Type)
	}
}
