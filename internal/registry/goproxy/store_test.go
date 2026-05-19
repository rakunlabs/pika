package goproxy

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs/localfs"
)

// newTempStore returns a Store rooted at a fresh temp directory.
func newTempStore(t *testing.T, basePath string) *Store {
	t.Helper()
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	return NewStore(fs, basePath)
}

func TestStore_WriteAndRead(t *testing.T) {
	s := newTempStore(t, "go")
	body := []byte(`{"Version":"v1.0.0","Time":"2024-01-01T00:00:00Z"}`)
	if err := s.WriteVersionFile("github.com/foo/bar", "v1.0.0", "info",
		strings.NewReader(string(body)), int64(len(body))); err != nil {
		t.Fatalf("WriteVersionFile: %v", err)
	}

	rc, _, err := s.OpenVersionFile("github.com/foo/bar", "v1.0.0", "info")
	if err != nil {
		t.Fatalf("OpenVersionFile: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("got %q, want %q", got, body)
	}
}

func TestStore_NotFound(t *testing.T) {
	s := newTempStore(t, "")
	_, _, err := s.OpenVersionFile("github.com/missing/mod", "v1.0.0", "zip")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	_, err = s.StatVersionFile("github.com/missing/mod", "v1.0.0", "zip")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_ListVersions(t *testing.T) {
	s := newTempStore(t, "")
	mod := "github.com/foo/bar"
	for _, v := range []string{"v1.0.0", "v1.2.0", "v0.9.0"} {
		if err := s.WriteInfo(mod, v, VersionInfo{Version: v, Time: time.Now()}); err != nil {
			t.Fatalf("WriteInfo %s: %v", v, err)
		}
	}
	got, err := s.ListVersions(mod)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	want := []string{"v0.9.0", "v1.0.0", "v1.2.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d] got %s want %s", i, got[i], want[i])
		}
	}
}

func TestStore_ListVersions_OnlyComplete(t *testing.T) {
	// A version without .info should be ignored — incomplete.
	s := newTempStore(t, "")
	mod := "github.com/foo/bar"

	// Complete version.
	if err := s.WriteInfo(mod, "v1.0.0", VersionInfo{Version: "v1.0.0", Time: time.Now()}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	// Incomplete: write only .zip.
	body := []byte("zip-content")
	if err := s.WriteVersionFile(mod, "v1.1.0", "zip", strings.NewReader(string(body)), int64(len(body))); err != nil {
		t.Fatalf("WriteVersionFile zip: %v", err)
	}

	got, err := s.ListVersions(mod)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(got) != 1 || got[0] != "v1.0.0" {
		t.Fatalf("got %v, want [v1.0.0]", got)
	}
}

func TestStore_CachedList(t *testing.T) {
	s := newTempStore(t, "")
	mod := "github.com/foo/bar"
	for _, v := range []string{"v1.0.0", "v1.2.0"} {
		if err := s.WriteInfo(mod, v, VersionInfo{Version: v, Time: time.Now()}); err != nil {
			t.Fatalf("WriteInfo %s: %v", v, err)
		}
	}
	body, err := s.CachedList(mod, time.Hour)
	if err != nil {
		t.Fatalf("CachedList: %v", err)
	}
	if string(body) != "v1.0.0\nv1.2.0\n" {
		t.Fatalf("got %q", body)
	}

	// Add a new version; cached list still returns the old contents
	// (within TTL) since the file is freshly written.
	if err := s.WriteInfo(mod, "v2.0.0", VersionInfo{Version: "v2.0.0", Time: time.Now()}); err != nil {
		t.Fatalf("WriteInfo v2: %v", err)
	}
	// WriteVersionFile invalidates the list cache, so the next
	// CachedList call rebuilds.
	body2, err := s.CachedList(mod, time.Hour)
	if err != nil {
		t.Fatalf("CachedList post-add: %v", err)
	}
	if !strings.Contains(string(body2), "v2.0.0") {
		t.Fatalf("expected v2.0.0 in rebuilt list, got %q", body2)
	}
}

func TestStore_CachedList_EmptyModule(t *testing.T) {
	s := newTempStore(t, "")
	body, err := s.CachedList("github.com/empty", time.Hour)
	if err != nil {
		t.Fatalf("CachedList empty: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("empty module should produce empty list, got %q", body)
	}
}

func TestStore_CachedLatest(t *testing.T) {
	s := newTempStore(t, "")
	mod := "github.com/foo/bar"
	now := time.Now().UTC()
	if err := s.WriteInfo(mod, "v0.1.0", VersionInfo{Version: "v0.1.0", Time: now}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	if err := s.WriteInfo(mod, "v1.0.0", VersionInfo{Version: "v1.0.0", Time: now}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	body, err := s.CachedLatest(mod, time.Hour)
	if err != nil {
		t.Fatalf("CachedLatest: %v", err)
	}
	if !strings.Contains(string(body), `"Version":"v1.0.0"`) {
		t.Fatalf("expected v1.0.0 in latest, got %q", body)
	}
}

func TestStore_CachedLatest_NoVersions(t *testing.T) {
	s := newTempStore(t, "")
	_, err := s.CachedLatest("github.com/empty", time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_DeleteVersion(t *testing.T) {
	s := newTempStore(t, "")
	mod := "github.com/foo/bar"
	if err := s.WriteInfo(mod, "v1.0.0", VersionInfo{Version: "v1.0.0", Time: time.Now()}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	body := []byte("mod-body")
	if err := s.WriteVersionFile(mod, "v1.0.0", "mod", strings.NewReader(string(body)), int64(len(body))); err != nil {
		t.Fatalf("WriteVersionFile mod: %v", err)
	}

	if err := s.DeleteVersion(mod, "v1.0.0"); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}
	if _, err := s.StatVersionFile(mod, "v1.0.0", "info"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected info gone, got %v", err)
	}
	if _, err := s.StatVersionFile(mod, "v1.0.0", "mod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected mod gone, got %v", err)
	}
}

func TestStore_ListModules(t *testing.T) {
	s := newTempStore(t, "")
	mods := []string{
		"github.com/foo/bar",
		"github.com/foo/baz",
		"github.com/Azure/azure-sdk-for-go",
		"golang.org/x/sync",
		"k8s.io/api",
	}
	for _, m := range mods {
		if err := s.WriteInfo(m, "v1.0.0", VersionInfo{Version: "v1.0.0", Time: time.Now()}); err != nil {
			t.Fatalf("WriteInfo %s: %v", m, err)
		}
	}
	got, err := s.ListModules()
	if err != nil {
		t.Fatalf("ListModules: %v", err)
	}
	want := map[string]bool{
		"github.com/foo/bar":                  true,
		"github.com/foo/baz":                  true,
		"github.com/Azure/azure-sdk-for-go":   true,
		"golang.org/x/sync":                   true,
		"k8s.io/api":                          true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("unexpected module %q in result", m)
		}
	}
}

func TestStore_VersionPathEncoding(t *testing.T) {
	// Sanity: the underlying file path uses the encoded module form.
	s := newTempStore(t, "")
	mod := "github.com/Azure/azure-sdk-for-go"
	if err := s.WriteInfo(mod, "v1.0.0", VersionInfo{Version: "v1.0.0", Time: time.Now()}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	// Path the store would use.
	expected := "modules/github.com/!azure/azure-sdk-for-go/@v/v1.0.0.info"
	if _, err := s.RawFS().Stat(expected); err != nil {
		t.Fatalf("expected encoded path %q to exist, got %v", expected, err)
	}
}
