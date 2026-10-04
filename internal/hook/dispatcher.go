package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"text/template"
	"time"
)

// sinkQueueSize bounds how many rendered payloads may wait for a single
// sink. Each sink drains its own queue so one slow target can't stall
// delivery to the others.
const sinkQueueSize = 64

// Dispatcher receives events and fans them out to matching hooks and their sinks.
type Dispatcher struct {
	mu        sync.RWMutex
	hooks     []hookInstance
	ch        chan Event
	done      chan struct{}
	pool      *kafkaClientPool
	resolver  *Resolver
	parentCtx context.Context
	logEvents atomic.Bool

	// emitMu guards ch against Emit racing with Stop's close.
	emitMu   sync.RWMutex
	stopped  bool
	started  atomic.Bool
	stopOnce sync.Once
}

// hookInstance pairs a hook definition with compiled templates and live sinks.
type hookInstance struct {
	hook          Hook
	sinks         []*sinkEntry
	bodyTemplates []*template.Template // per-target, nil means use default JSON
	keyTemplates  []*template.Template // per-target (Kafka only), nil means default
}

// sinkEntry pairs a sink with its delivery queue and worker.
type sinkEntry struct {
	sink       Sink
	targetType string
	hookName   string
	queue      chan sinkJob
	done       chan struct{}
}

// sinkJob is one rendered payload waiting for delivery.
type sinkJob struct {
	ctx     context.Context
	payload []byte
	key     string
	event   Event
}

func newSinkEntry(sink Sink, targetType, hookName string) *sinkEntry {
	se := &sinkEntry{
		sink:       sink,
		targetType: targetType,
		hookName:   hookName,
		queue:      make(chan sinkJob, sinkQueueSize),
		done:       make(chan struct{}),
	}
	go se.run()
	return se
}

// run delivers queued payloads until the queue is closed, then closes
// the sink.
func (se *sinkEntry) run() {
	defer close(se.done)

	for job := range se.queue {
		if err := se.sink.Send(job.ctx, job.payload, job.key); err != nil {
			slog.Error("failed to send hook event",
				"hook", se.hookName,
				"target_type", se.targetType,
				"event_type", job.event.Type,
				"error", err,
			)
			continue
		}
		slog.Debug("hook event sent",
			"hook", se.hookName,
			"target_type", se.targetType,
			"event_type", job.event.Type,
			"mount", job.event.Mount,
			"path", job.event.Path,
		)
	}

	if err := se.sink.Close(); err != nil {
		slog.Warn("error closing hook sink", "hook", se.hookName, "target_type", se.targetType, "error", err)
	}
}

// NewDispatcher creates a dispatcher with the given buffer size.
func NewDispatcher(bufferSize int) *Dispatcher {
	if bufferSize <= 0 {
		bufferSize = 256
	}
	d := &Dispatcher{
		ch:   make(chan Event, bufferSize),
		done: make(chan struct{}),
		pool: newKafkaClientPool(),
	}
	d.logEvents.Store(true)
	return d
}

// SetEventLogEnabled controls the built-in structured log line emitted for
// every event. Hook sinks still receive events regardless of this setting.
func (d *Dispatcher) SetEventLogEnabled(enabled bool) {
	d.logEvents.Store(enabled)
}

// EventLogEnabled reports whether built-in event logging is enabled.
func (d *Dispatcher) EventLogEnabled() bool {
	return d.logEvents.Load()
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
	d.started.Store(true)
	go d.loop(ctx)
}

// Stop shuts the dispatcher down: no new events are accepted, queued
// events are delivered (or fail fast once ctx is cancelled), and every
// sink plus the Kafka pool is closed. Safe to call more than once and
// safe to race with Emit.
func (d *Dispatcher) Stop() {
	d.stopOnce.Do(func() {
		d.emitMu.Lock()
		d.stopped = true
		close(d.ch)
		d.emitMu.Unlock()

		if d.started.Load() {
			<-d.done
		}

		d.mu.Lock()
		old := d.hooks
		d.hooks = nil
		d.mu.Unlock()

		closeSinks(old, true)
		d.pool.closeAll()
	})
}

// closeSinks closes the queues of every sink in hooks. Workers drain what
// is already queued and then close their sink. When wait is true it blocks
// until every worker has finished.
func closeSinks(hooks []hookInstance, wait bool) {
	for _, hi := range hooks {
		for _, se := range hi.sinks {
			close(se.queue)
		}
	}
	if !wait {
		return
	}
	for _, hi := range hooks {
		for _, se := range hi.sinks {
			<-se.done
		}
	}
}

// Emit sends an event to the dispatcher for async processing.
// If the buffer is full, the event is dropped with a warning log.
func (d *Dispatcher) Emit(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	if d.logEvents.Load() {
		logEvent(event)
	}

	d.emitMu.RLock()
	defer d.emitMu.RUnlock()

	if d.stopped {
		return
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

func logEvent(event Event) {
	attrs := []slog.Attr{
		slog.String("event_type", string(event.Type)),
		slog.Time("event_timestamp", event.Timestamp),
	}
	if event.Hook != "" {
		attrs = append(attrs, slog.String("hook", event.Hook))
	}
	if event.Mount != "" {
		attrs = append(attrs, slog.String("mount", event.Mount))
	}
	if event.Path != "" {
		attrs = append(attrs, slog.String("path", event.Path))
	}
	if event.Size != 0 {
		attrs = append(attrs, slog.Int64("size", event.Size))
	}
	if event.Protocol != "" {
		attrs = append(attrs, slog.String("protocol", event.Protocol))
	}
	if event.User != "" {
		attrs = append(attrs, slog.String("user", event.User))
	}
	if event.OldPath != "" {
		attrs = append(attrs, slog.String("old_path", event.OldPath))
	}
	if event.DstMount != "" {
		attrs = append(attrs, slog.String("dst_mount", event.DstMount))
	}
	if event.DstPath != "" {
		attrs = append(attrs, slog.String("dst_path", event.DstPath))
	}
	if event.ConfigKey != "" {
		attrs = append(attrs, slog.String("config_key", event.ConfigKey))
	}
	if event.ConfigVersion != 0 {
		attrs = append(attrs, slog.Int64("config_version", event.ConfigVersion))
	}
	if event.Variant != "" {
		attrs = append(attrs, slog.String("variant", event.Variant))
	}

	slog.LogAttrs(context.Background(), slog.LevelInfo, "pika event emitted", attrs...)
}

// UpdateHooks replaces all hooks. It builds new sinks, swaps them in and
// retires the old ones; old sinks finish delivering what was already
// queued before closing. Kafka targets that share the same brokers and
// security config reuse a single underlying client from the pool.
func (d *Dispatcher) UpdateHooks(hooks []Hook) {
	d.mu.Lock()
	defer d.mu.Unlock()

	old := d.hooks
	d.hooks = nil
	defer closeSinks(old, false)

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

			hi.sinks = append(hi.sinks, newSinkEntry(sink, t.Type, h.Name))

			// Compile body template. The "log" target renders its own
			// Message and Fields directly from the Event, so it ignores
			// any body template — warn if one was supplied and skip compilation
			// to avoid wasted work on every dispatch.
			var bodyTmpl *template.Template
			switch {
			case t.Type == "log":
				if t.BodyTemplate != "" {
					slog.Warn("log target ignores body_template; use LogTarget.Message and LogTarget.Fields instead",
						"hook", h.Name,
					)
				}
			case t.BodyTemplate != "":
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
}

// dispatch renders the event for every matching sink and queues it for
// that sink's worker. It never blocks on delivery: a full sink queue
// drops the event for that sink only.
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

		// Carry the full event on the context so event-aware sinks (e.g. the
		// local log sink) can recover it without a Sink interface change.
		// Other sinks ignore ctx.Value entirely.
		sinkCtx := contextWithEvent(ctx, eventCopy)

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

			select {
			case se.queue <- sinkJob{ctx: sinkCtx, payload: payload, key: key, event: eventCopy}:
			default:
				slog.Warn("hook sink queue full, dropping event",
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
	case "log":
		return NewLogSink(t.Log)
	default:
		return nil, fmt.Errorf("unknown target type %q", t.Type)
	}
}
