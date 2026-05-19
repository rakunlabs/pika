package virtualbase_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/virtualbase"
)

// stubResolver implements virtualbase.Resolver.
type stubResolver struct {
	regs map[string]registry.Registry
}

func (s *stubResolver) Lookup(_, repo string) (registry.Registry, bool) {
	r, ok := s.regs[repo]
	return r, ok
}

// stubReg is a Registry that returns a canned HTTP response.
type stubReg struct {
	status int
	body   string
	header http.Header
}

func (s *stubReg) Namespace() string { return "default" }
func (s *stubReg) Name() string      { return "stub" }
func (s *stubReg) Type() string      { return "stub" }
func (s *stubReg) Kind() string      { return "stub" }
func (s *stubReg) Close() error      { return nil }
func (s *stubReg) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	for k, vs := range s.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(s.status)
	_, _ = w.Write([]byte(s.body))
}

// stubDetailer adds PackageDetail support.
type stubDetailer struct {
	stubReg
	detail *registry.PackageDetail
	err    error
}

func (s *stubDetailer) PackageDetail(_ context.Context, _ string) (*registry.PackageDetail, error) {
	return s.detail, s.err
}

func TestServeFirstHit_ReturnsFirst2xx(t *testing.T) {
	res := &stubResolver{regs: map[string]registry.Registry{
		"a": &stubReg{status: 404, body: "miss"},
		"b": &stubReg{status: 200, body: "hit"},
		"c": &stubReg{status: 200, body: "later"},
	}}
	base := virtualbase.New("default", "virt", []string{"a", "b", "c"}, res)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	if !base.ServeFirstHit(w, r) {
		t.Fatalf("expected served=true")
	}
	if w.Code != 200 || w.Body.String() != "hit" {
		t.Errorf("got code=%d body=%q, want 200/hit", w.Code, w.Body.String())
	}
}

func TestServeFirstHit_AllMissReturnsFalse(t *testing.T) {
	res := &stubResolver{regs: map[string]registry.Registry{
		"a": &stubReg{status: 404, body: "no"},
		"b": &stubReg{status: 500, body: "boom"},
	}}
	base := virtualbase.New("default", "virt", []string{"a", "b"}, res)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	if base.ServeFirstHit(w, r) {
		t.Errorf("expected served=false when no 2xx")
	}
}

func TestDelegatePackageDetail_FirstNonError(t *testing.T) {
	want := &registry.PackageDetail{Type: "stub", Name: "found"}
	res := &stubResolver{regs: map[string]registry.Registry{
		"a": &stubDetailer{err: registry.ErrPackageNotFound},
		"b": &stubDetailer{detail: want},
		"c": &stubDetailer{detail: &registry.PackageDetail{Name: "shadowed"}},
	}}
	base := virtualbase.New("default", "virt", []string{"a", "b", "c"}, res)
	got, err := base.DelegatePackageDetail(context.Background(), "anything")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got != want {
		t.Errorf("got=%v, want %v", got, want)
	}
}

func TestDelegatePackageDetail_NoneFoundReturnsSentinel(t *testing.T) {
	res := &stubResolver{regs: map[string]registry.Registry{
		"a": &stubDetailer{err: registry.ErrPackageNotFound},
	}}
	base := virtualbase.New("default", "virt", []string{"a"}, res)
	_, err := base.DelegatePackageDetail(context.Background(), "x")
	if err != registry.ErrPackageNotFound {
		t.Errorf("err=%v, want ErrPackageNotFound", err)
	}
}

func TestCollectListLines_Union(t *testing.T) {
	res := &stubResolver{regs: map[string]registry.Registry{
		"a": &stubReg{status: 200, body: "v1.0.0\nv1.1.0\n"},
		"b": &stubReg{status: 200, body: "v1.1.0\nv2.0.0\n"}, // overlap on v1.1.0
		"c": &stubReg{status: 404, body: ""},                 // skipped
	}}
	base := virtualbase.New("default", "virt", []string{"a", "b", "c"}, res)
	r := httptest.NewRequest(http.MethodGet, "/list", nil)
	got := base.CollectListLines(r)
	want := []string{"v1.0.0", "v1.1.0", "v2.0.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got=%v, want %v", got, want)
	}
}

func TestCopyHeaders_SkipsHopByHop(t *testing.T) {
	src := http.Header{
		"Content-Type": []string{"application/json"},
		"Connection":   []string{"close"}, // hop-by-hop, must skip
		"X-Custom":     []string{"keep"},
	}
	dst := http.Header{}
	virtualbase.CopyHeaders(dst, src)
	if dst.Get("Connection") != "" {
		t.Errorf("hop-by-hop Connection should not be copied")
	}
	if dst.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type missing")
	}
	if dst.Get("X-Custom") != "keep" {
		t.Errorf("X-Custom missing")
	}
}

func TestResolver_MissingMemberSkipped(t *testing.T) {
	// One member is missing from the resolver — ForEachMember
	// must skip silently rather than error.
	res := &stubResolver{regs: map[string]registry.Registry{
		"b": &stubReg{status: 200, body: "ok"},
	}}
	base := virtualbase.New("default", "virt", []string{"a", "b"}, res)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	if !base.ServeFirstHit(w, r) {
		t.Fatalf("expected served=true (b should serve)")
	}
	if w.Body.String() != "ok" {
		t.Errorf("got body=%q, want ok", w.Body.String())
	}
}
