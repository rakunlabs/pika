package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/external"
)

// stubProvider is an in-memory Provider used to drive the export
// engine without a live Consul/Vault. tree maps a listing prefix to
// the children that prefix returns (folders carry a trailing "/",
// mirroring what the real Consul/Vault clients emit); values maps a
// leaf path to the entry Read returns. A leaf missing from values
// produces a read error, which is how the partial-failure path is
// exercised.
type stubProvider struct {
	kind      string
	tree      map[string][]string
	values    map[string]map[string]any
	listFails map[string]bool
}

func (s *stubProvider) Kind() string { return s.kind }

func (s *stubProvider) Capabilities() external.Capabilities {
	return external.Capabilities{CanRead: true, CanList: true}
}

func (s *stubProvider) Validate() error { return nil }

func (s *stubProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	return nil, external.ErrNotSupported
}

func (s *stubProvider) Read(ctx context.Context, path string) (*external.Entry, error) {
	data, ok := s.values[path]
	if !ok {
		return nil, errors.New("not found")
	}
	raw, _ := json.Marshal(data)
	return &external.Entry{Data: data, Raw: raw, ContentType: "application/json"}, nil
}

func (s *stubProvider) List(ctx context.Context, prefix string) ([]string, error) {
	if s.listFails[prefix] {
		return nil, errors.New("permission denied")
	}
	return s.tree[prefix], nil
}

func (s *stubProvider) Write(ctx context.Context, path string, data map[string]any) error {
	return external.ErrNotSupported
}

func (s *stubProvider) Delete(ctx context.Context, path string) error {
	return external.ErrNotSupported
}

func (s *stubProvider) ListVersions(ctx context.Context, path string) ([]external.Version, error) {
	return nil, external.ErrNotSupported
}

func (s *stubProvider) ReadVersion(ctx context.Context, path, version string) (*external.Entry, error) {
	return nil, external.ErrNotSupported
}

func (s *stubProvider) Test(ctx context.Context) external.TestResult {
	return external.TestResult{OK: true}
}

// readZip returns the archive contents as name -> body.
func readZip(t *testing.T, buf *bytes.Buffer) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("opening zip: %v", err)
	}
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", f.Name, err)
		}
		out[f.Name] = string(b)
	}
	return out
}

func TestExportConsulWritesRawValuesAtKeyPaths(t *testing.T) {
	p := &stubProvider{
		kind: "consul",
		tree: map[string][]string{
			"":        {"app/", "topkey"},
			"app/":    {"db/", "name"},
			"app/db/": {"password", "settings"},
		},
		values: map[string]map[string]any{
			"topkey":          {"value": "plain-top"},
			"app/name":        {"value": "pika"},
			"app/db/password": {"value": "s3cr3t"},
			"app/db/settings": {"host": "db.example.com", "port": float64(5432)},
		},
	}

	var buf bytes.Buffer
	stats, err := writeExternalExport(context.Background(), p, &buf, ExternalExportOptions{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if stats.Entries != 4 {
		t.Fatalf("entries: got %d, want 4", stats.Entries)
	}
	if stats.Failed != 0 || stats.Truncated {
		t.Fatalf("unexpected failures: %+v", stats)
	}

	files := readZip(t, &buf)
	// Plain string values are written verbatim under the key path —
	// no extension, no JSON wrapper, so the archive mirrors the KV
	// tree exactly.
	if got := files["app/db/password"]; got != "s3cr3t" {
		t.Errorf("app/db/password: got %q, want %q", got, "s3cr3t")
	}
	if got := files["app/name"]; got != "pika" {
		t.Errorf("app/name: got %q, want %q", got, "pika")
	}
	if got := files["topkey"]; got != "plain-top" {
		t.Errorf("topkey: got %q, want %q", got, "plain-top")
	}
	// A value Consul stored as JSON comes back parsed; it is
	// re-emitted as indented JSON under the same key path.
	if got := files["app/db/settings"]; !strings.Contains(got, `"host": "db.example.com"`) {
		t.Errorf("app/db/settings: got %q", got)
	}
	if _, ok := files["_errors.txt"]; ok {
		t.Errorf("unexpected _errors.txt in a clean export")
	}
}

func TestExportVaultWritesJSONFiles(t *testing.T) {
	p := &stubProvider{
		kind: "vault",
		tree: map[string][]string{
			"":       {"myapp/"},
			"myapp/": {"db"},
		},
		values: map[string]map[string]any{
			"myapp/db": {"username": "admin", "password": "hunter2"},
		},
	}

	var buf bytes.Buffer
	if _, err := writeExternalExport(context.Background(), p, &buf, ExternalExportOptions{}); err != nil {
		t.Fatalf("export: %v", err)
	}

	files := readZip(t, &buf)
	body, ok := files["myapp/db.json"]
	if !ok {
		t.Fatalf("myapp/db.json missing; got %v", files)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding entry: %v", err)
	}
	if decoded["password"] != "hunter2" {
		t.Errorf("password: got %v", decoded["password"])
	}
}

func TestExportRecordsPartialFailures(t *testing.T) {
	p := &stubProvider{
		kind: "consul",
		tree: map[string][]string{
			"":        {"ok/", "denied/"},
			"ok/":     {"key", "missing"},
			"denied/": {},
		},
		values: map[string]map[string]any{
			"ok/key": {"value": "fine"},
		},
		listFails: map[string]bool{"denied/": true},
	}

	var buf bytes.Buffer
	stats, err := writeExternalExport(context.Background(), p, &buf, ExternalExportOptions{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if stats.Entries != 1 {
		t.Fatalf("entries: got %d, want 1", stats.Entries)
	}
	// One denied prefix listing + one unreadable leaf.
	if stats.Failed != 2 {
		t.Fatalf("failed: got %d, want 2", stats.Failed)
	}

	files := readZip(t, &buf)
	errsBody, ok := files["_errors.txt"]
	if !ok {
		t.Fatalf("_errors.txt missing")
	}
	if !strings.Contains(errsBody, "denied/") || !strings.Contains(errsBody, "ok/missing") {
		t.Errorf("_errors.txt does not list both failures: %q", errsBody)
	}
}

func TestExportTruncatesAtLimit(t *testing.T) {
	p := &stubProvider{
		kind: "consul",
		tree: map[string][]string{
			"": {"a", "b", "c"},
		},
		values: map[string]map[string]any{
			"a": {"value": "1"},
			"b": {"value": "2"},
			"c": {"value": "3"},
		},
	}

	var buf bytes.Buffer
	stats, err := writeExternalExport(context.Background(), p, &buf, ExternalExportOptions{MaxEntries: 2})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !stats.Truncated {
		t.Fatalf("expected truncated export")
	}
	if stats.Entries != 2 {
		t.Fatalf("entries: got %d, want 2", stats.Entries)
	}
	files := readZip(t, &buf)
	if body := files["_errors.txt"]; !strings.Contains(body, "TRUNCATED") {
		t.Errorf("_errors.txt should announce truncation: %q", body)
	}
}

// A Consul key can be both a value and a folder ("app" plus
// "app/db"). Zip cannot hold a file and a directory with the same
// name, so the value-bearing key gets a ".value" suffix.
func TestExportDisambiguatesKeyThatIsAlsoAFolder(t *testing.T) {
	p := &stubProvider{
		kind: "consul",
		tree: map[string][]string{
			"":     {"app", "app/"},
			"app/": {"db"},
		},
		values: map[string]map[string]any{
			"app":    {"value": "folder-and-value"},
			"app/db": {"value": "inner"},
		},
	}

	var buf bytes.Buffer
	if _, err := writeExternalExport(context.Background(), p, &buf, ExternalExportOptions{}); err != nil {
		t.Fatalf("export: %v", err)
	}

	files := readZip(t, &buf)
	if got := files["app.value"]; got != "folder-and-value" {
		t.Errorf("app.value: got %q, want %q", got, "folder-and-value")
	}
	if got := files["app/db"]; got != "inner" {
		t.Errorf("app/db: got %q, want %q", got, "inner")
	}
	if _, bad := files["app"]; bad {
		t.Errorf("archive contains both file %q and directory %q", "app", "app/")
	}
}

func TestSanitizeExportPathRejectsTraversal(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd": "etc/passwd",
		"/leading/slash":   "leading/slash",
		`win\\path`:        "win__path",
		"":                 "_root",
		"a/../b":           "b",
	}
	for in, want := range cases {
		if got := sanitizeExportPath(in); got != want {
			t.Errorf("sanitizeExportPath(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestCheckExportableRejectsUnsupportedKinds(t *testing.T) {
	p := &stubProvider{kind: "http"}
	err := checkExportable(p)
	if err == nil {
		t.Fatalf("expected an error for http resources")
	}
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}
	if !strings.Contains(err.Error(), "consul") || !strings.Contains(err.Error(), "vault") {
		t.Errorf("error should name the supported kinds: %v", err)
	}
}
