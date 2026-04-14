package hook

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"text/template"
)

// logSink writes matched events to the local slog default logger.
// Unlike the other sinks, it does not transmit anywhere off-process;
// it renders its own message and attribute templates against the Event
// and ignores the pre-rendered payload passed to Send.
type logSink struct {
	level   slog.Level
	msgTmpl *template.Template            // nil => use event type as message
	fields  map[string]*template.Template // pre-compiled per key
}

// NewLogSink creates a new local slog logging sink.
func NewLogSink(target *LogTarget) (Sink, error) {
	if target == nil {
		return nil, fmt.Errorf("log target is required")
	}

	level, err := parseLogLevel(target.Level)
	if err != nil {
		return nil, err
	}

	s := &logSink{level: level}

	if target.Message != "" {
		tmpl, err := template.New("log_message").Parse(target.Message)
		if err != nil {
			return nil, fmt.Errorf("log: parsing message template: %w", err)
		}
		s.msgTmpl = tmpl
	}

	if len(target.Fields) > 0 {
		s.fields = make(map[string]*template.Template, len(target.Fields))
		// Sort keys so template names (and any resulting errors) are deterministic.
		keys := make([]string, 0, len(target.Fields))
		for k := range target.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			tmpl, err := template.New("log_field_" + k).Parse(target.Fields[k])
			if err != nil {
				return nil, fmt.Errorf("log: parsing field %q template: %w", k, err)
			}
			s.fields[k] = tmpl
		}
	}

	return s, nil
}

// Send renders the message and field templates against the Event carried
// on ctx and writes one structured log line at the configured level.
// The payload and key arguments are ignored — see package-level notes on
// why the log sink re-renders from the Event instead of the body template.
func (s *logSink) Send(ctx context.Context, _ []byte, _ string) error {
	ev, ok := eventFromContext(ctx)
	if !ok {
		return fmt.Errorf("log: event missing from context")
	}

	msg := string(ev.Type)
	if s.msgTmpl != nil {
		var buf bytes.Buffer
		if err := s.msgTmpl.Execute(&buf, ev); err != nil {
			return fmt.Errorf("log: executing message template: %w", err)
		}
		msg = strings.TrimSpace(buf.String())
	}

	attrs := make([]slog.Attr, 0, len(s.fields)+1)
	if ev.Hook != "" {
		attrs = append(attrs, slog.String("hook", ev.Hook))
	}

	// Render fields in deterministic (sorted) order to keep output stable.
	if len(s.fields) > 0 {
		keys := make([]string, 0, len(s.fields))
		for k := range s.fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			var buf bytes.Buffer
			if err := s.fields[k].Execute(&buf, ev); err != nil {
				return fmt.Errorf("log: executing field %q template: %w", k, err)
			}
			attrs = append(attrs, slog.String(k, buf.String()))
		}
	}

	slog.Default().LogAttrs(ctx, s.level, msg, attrs...)
	return nil
}

// Close is a no-op for the log sink.
func (s *logSink) Close() error { return nil }

// parseLogLevel converts a user-provided string to a slog.Level.
// Empty or "info" => LevelInfo. Case-insensitive.
func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("log: unknown level %q (want debug|info|warn|error)", s)
	}
}

// eventCtxKey is the context key used to stash the current Event so
// event-aware sinks (like logSink) can recover it from the ctx passed
// to Sink.Send. Unexported so only the hook package can write/read it.
type eventCtxKey struct{}

// contextWithEvent returns a derived context carrying the given event.
// The dispatcher calls this once per (event, sink) before Sink.Send.
func contextWithEvent(ctx context.Context, ev Event) context.Context {
	return context.WithValue(ctx, eventCtxKey{}, ev)
}

// eventFromContext retrieves an Event previously stashed by contextWithEvent.
// Returns ok=false if none is present (e.g. when the sink is invoked outside
// the dispatcher — in tests the caller must wrap ctx explicitly).
func eventFromContext(ctx context.Context) (Event, bool) {
	ev, ok := ctx.Value(eventCtxKey{}).(Event)
	return ev, ok
}
