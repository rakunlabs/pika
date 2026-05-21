package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// captureLogger swaps slog.Default with a JSON handler writing to buf at the
// given minimum level, and returns a restore func. Use as:
//
//	var buf bytes.Buffer
//	restore := captureLogger(t, &buf, slog.LevelDebug)
//	defer restore()
func captureLogger(t *testing.T, buf *bytes.Buffer, level slog.Level) func() {
	t.Helper()
	prev := slog.Default()
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
	return func() { slog.SetDefault(prev) }
}

// decodeLogLines parses one JSON object per non-empty line produced by
// slog.NewJSONHandler and returns them in order.
func decodeLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func sampleEvent() Event {
	return Event{
		Type:      EventFileCreated,
		Timestamp: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Hook:      "test-hook",
		Mount:     "uploads",
		Path:      "images/cat.png",
		Size:      1234,
		Protocol:  "http",
		User:      "alice",
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"", slog.LevelInfo, false},
		{"info", slog.LevelInfo, false},
		{"INFO", slog.LevelInfo, false},
		{"debug", slog.LevelDebug, false},
		{" Debug ", slog.LevelDebug, false},
		{"warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"fatal", 0, true},
		{"trace", 0, true},
	}
	for _, tc := range cases {
		got, err := parseLogLevel(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseLogLevel(%q): want error, got nil", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLogLevel(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNewLogSinkNilTarget(t *testing.T) {
	if _, err := NewLogSink(nil); err == nil {
		t.Fatal("NewLogSink(nil): want error")
	}
}

func TestNewLogSinkInvalidLevel(t *testing.T) {
	if _, err := NewLogSink(&LogTarget{Level: "bogus"}); err == nil {
		t.Fatal("NewLogSink with invalid level: want error")
	}
}

func TestNewLogSinkInvalidMessageTemplate(t *testing.T) {
	if _, err := NewLogSink(&LogTarget{Message: "{{.Mount"}); err == nil {
		t.Fatal("NewLogSink with bad message template: want error")
	}
}

func TestNewLogSinkInvalidFieldTemplate(t *testing.T) {
	_, err := NewLogSink(&LogTarget{
		Fields: map[string]string{"bad": "{{.Mount"},
	})
	if err == nil {
		t.Fatal("NewLogSink with bad field template: want error")
	}
	if !strings.Contains(err.Error(), `"bad"`) {
		t.Errorf("error should mention the field name, got: %v", err)
	}
}

func TestLogSinkSendDefaults(t *testing.T) {
	var buf bytes.Buffer
	defer captureLogger(t, &buf, slog.LevelDebug)()

	sink, err := NewLogSink(&LogTarget{})
	if err != nil {
		t.Fatalf("NewLogSink: %v", err)
	}

	ctx := contextWithEvent(context.Background(), sampleEvent())
	if err := sink.Send(ctx, []byte("ignored"), "ignored-key"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	lines := decodeLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("want 1 log line, got %d", len(lines))
	}
	line := lines[0]
	if line["level"] != "INFO" {
		t.Errorf("default level = %v, want INFO", line["level"])
	}
	if line["msg"] != string(EventFileCreated) {
		t.Errorf("default message = %v, want %s", line["msg"], EventFileCreated)
	}
	if line["hook"] != "test-hook" {
		t.Errorf("hook attr = %v, want test-hook", line["hook"])
	}
}

func TestLogSinkSendMessageAndFields(t *testing.T) {
	var buf bytes.Buffer
	defer captureLogger(t, &buf, slog.LevelDebug)()

	sink, err := NewLogSink(&LogTarget{
		Level:   "warn",
		Message: "file {{.Path}} on {{.Mount}}",
		Fields: map[string]string{
			"mount": "{{.Mount}}",
			"path":  "{{.Path}}",
			"size":  "{{.Size}}",
			"user":  "{{.User}}",
		},
	})
	if err != nil {
		t.Fatalf("NewLogSink: %v", err)
	}

	ctx := contextWithEvent(context.Background(), sampleEvent())
	if err := sink.Send(ctx, nil, ""); err != nil {
		t.Fatalf("Send: %v", err)
	}

	lines := decodeLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("want 1 log line, got %d", len(lines))
	}
	line := lines[0]
	if line["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", line["level"])
	}
	if line["msg"] != "file images/cat.png on uploads" {
		t.Errorf("msg = %v", line["msg"])
	}
	if line["mount"] != "uploads" {
		t.Errorf("mount attr = %v", line["mount"])
	}
	if line["path"] != "images/cat.png" {
		t.Errorf("path attr = %v", line["path"])
	}
	if line["size"] != "1234" {
		t.Errorf("size attr = %v, want string \"1234\"", line["size"])
	}
	if line["user"] != "alice" {
		t.Errorf("user attr = %v", line["user"])
	}
	if line["hook"] != "test-hook" {
		t.Errorf("hook attr = %v", line["hook"])
	}
}

func TestLogSinkSendRespectsGlobalLevel(t *testing.T) {
	var buf bytes.Buffer
	// Handler at Info — a debug sink should produce no output.
	defer captureLogger(t, &buf, slog.LevelInfo)()

	sink, err := NewLogSink(&LogTarget{Level: "debug", Message: "hi"})
	if err != nil {
		t.Fatalf("NewLogSink: %v", err)
	}

	ctx := contextWithEvent(context.Background(), sampleEvent())
	if err := sink.Send(ctx, nil, ""); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("expected no output at debug below info threshold, got: %s", buf.String())
	}
}

func TestLogSinkSendMissingEvent(t *testing.T) {
	var buf bytes.Buffer
	defer captureLogger(t, &buf, slog.LevelDebug)()

	sink, err := NewLogSink(&LogTarget{})
	if err != nil {
		t.Fatalf("NewLogSink: %v", err)
	}

	// Plain ctx, no event stashed.
	if err := sink.Send(context.Background(), nil, ""); err == nil {
		t.Fatal("Send without event in ctx: want error")
	}
}

func TestLogSinkSendFieldTemplateRuntimeError(t *testing.T) {
	var buf bytes.Buffer
	defer captureLogger(t, &buf, slog.LevelDebug)()

	// Template parses fine but fails at execution time: Mount is a string,
	// so method calls on it will fail.
	sink, err := NewLogSink(&LogTarget{
		Fields: map[string]string{"x": "{{.Mount.NoSuchMethod}}"},
	})
	if err != nil {
		t.Fatalf("NewLogSink: %v", err)
	}

	ctx := contextWithEvent(context.Background(), sampleEvent())
	if err := sink.Send(ctx, nil, ""); err == nil {
		t.Fatal("Send with failing field template: want error")
	}
}

func TestLogSinkCloseIdempotent(t *testing.T) {
	sink, err := NewLogSink(&LogTarget{})
	if err != nil {
		t.Fatalf("NewLogSink: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close (second call): %v", err)
	}
}

func TestContextWithEventRoundTrip(t *testing.T) {
	ev := sampleEvent()
	ctx := contextWithEvent(context.Background(), ev)

	got, ok := eventFromContext(ctx)
	if !ok {
		t.Fatal("eventFromContext: ok=false")
	}
	if got.Type != ev.Type || got.Mount != ev.Mount || got.Path != ev.Path {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, ev)
	}

	if _, ok := eventFromContext(context.Background()); ok {
		t.Error("eventFromContext on plain ctx: ok=true, want false")
	}
}

func TestDispatcherEmitLogsStructuredEvent(t *testing.T) {
	var buf bytes.Buffer
	defer captureLogger(t, &buf, slog.LevelDebug)()

	d := NewDispatcher(1)
	if !d.EventLogEnabled() {
		t.Fatal("event logging should be enabled by default")
	}
	d.Emit(Event{
		Type:          EventConfigCreated,
		ConfigKey:     "app.yaml",
		ConfigVersion: 2,
		User:          "alice",
	})

	lines := decodeLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("want 1 log line, got %d", len(lines))
	}
	line := lines[0]
	if line["msg"] != "pika event emitted" {
		t.Errorf("msg = %v", line["msg"])
	}
	if line["event_type"] != string(EventConfigCreated) {
		t.Errorf("event_type = %v", line["event_type"])
	}
	if line["config_key"] != "app.yaml" {
		t.Errorf("config_key = %v", line["config_key"])
	}
	if line["config_version"] != float64(2) {
		t.Errorf("config_version = %v", line["config_version"])
	}
	if line["user"] != "alice" {
		t.Errorf("user = %v", line["user"])
	}
	if line["event_timestamp"] == nil {
		t.Error("event_timestamp missing")
	}
}

func TestDispatcherEmitRespectsEventLogToggle(t *testing.T) {
	var buf bytes.Buffer
	defer captureLogger(t, &buf, slog.LevelDebug)()

	d := NewDispatcher(1)
	d.SetEventLogEnabled(false)
	d.Emit(Event{Type: EventFileDeleted, Mount: "uploads", Path: "old.txt"})

	if got := decodeLogLines(t, &buf); len(got) != 0 {
		t.Fatalf("want no log lines when event logging is disabled, got %d", len(got))
	}
}

// TestDispatcherLogTargetIgnoresBodyTemplate verifies that a log target
// configured with a body_template is accepted (so stale UI input cannot
// break the hook) but the template is never compiled — the log sink
// renders its own message and fields directly from the Event.
func TestDispatcherLogTargetIgnoresBodyTemplate(t *testing.T) {
	var buf bytes.Buffer
	defer captureLogger(t, &buf, slog.LevelDebug)()

	d := NewDispatcher(4)
	d.UpdateHooks([]Hook{{
		Name:    "ignore-body-template",
		Enabled: true,
		Events:  []EventType{EventAll},
		Targets: []Target{{
			Type:         "log",
			Log:          &LogTarget{Level: "info", Message: "hi {{.Mount}}"},
			BodyTemplate: "{{.ThisShouldNotBeCompiled}}", // would fail at render if compiled
		}},
	}})

	if len(d.hooks) != 1 {
		t.Fatalf("want 1 active hook, got %d", len(d.hooks))
	}
	hi := d.hooks[0]
	if len(hi.bodyTemplates) != 1 {
		t.Fatalf("want 1 body template slot, got %d", len(hi.bodyTemplates))
	}
	if hi.bodyTemplates[0] != nil {
		t.Errorf("body template for log target should be nil, got %v", hi.bodyTemplates[0])
	}

	// Dispatching should not panic or error even though the body template
	// string references a non-existent field.
	d.dispatch(context.Background(), Event{
		Type:  EventFileCreated,
		Mount: "uploads",
		Path:  "a.txt",
	})

	// The structured "hi uploads" line should be present; the dispatcher
	// warning about body_template should also be in the captured output.
	out := buf.String()
	if !strings.Contains(out, `"msg":"hi uploads"`) {
		t.Errorf("expected rendered message in output; got:\n%s", out)
	}
	if !strings.Contains(out, "log target ignores body_template") {
		t.Errorf("expected warning about ignored body_template; got:\n%s", out)
	}
}
