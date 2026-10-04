package hook

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/netguard"
)

type capturedRequest struct {
	method string
	path   string
	header http.Header
	body   []byte
}

func newCaptureServer(t *testing.T, status int) (*httptest.Server, func() []capturedRequest) {
	t.Helper()
	var (
		mu   sync.Mutex
		reqs []capturedRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, capturedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body})
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []capturedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]capturedRequest(nil), reqs...)
	}
}

func newHTTPSinkT(t *testing.T, target *HTTPTarget) Sink {
	t.Helper()
	sink, err := NewHTTPSink(target)
	if err != nil {
		t.Fatalf("NewHTTPSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	return sink
}

func TestNewHTTPSink_Validation(t *testing.T) {
	if _, err := NewHTTPSink(nil); err == nil {
		t.Error("NewHTTPSink(nil) succeeded")
	}
	if _, err := NewHTTPSink(&HTTPTarget{}); err == nil {
		t.Error("NewHTTPSink(empty url) succeeded")
	}
	if _, err := NewHTTPSink(&HTTPTarget{URL: "http://x", Timeout: "soon"}); err == nil {
		t.Error("NewHTTPSink(bad timeout) succeeded")
	}
}

func TestHTTPSink_DefaultPOST(t *testing.T) {
	srv, reqs := newCaptureServer(t, http.StatusOK)
	sink := newHTTPSinkT(t, &HTTPTarget{URL: srv.URL + "/hook"})

	payload := []byte(`{"type":"file.created","path":"a/b"}`)
	if err := sink.Send(t.Context(), payload, "ignored-key"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := reqs()
	if len(got) != 1 {
		t.Fatalf("server received %d requests; want 1", len(got))
	}
	r := got[0]
	if r.method != http.MethodPost {
		t.Errorf("method = %q; want POST", r.method)
	}
	if r.path != "/hook" {
		t.Errorf("path = %q", r.path)
	}
	if string(r.body) != string(payload) {
		t.Errorf("body = %q; want %q", r.body, payload)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if ua := r.header.Get("User-Agent"); ua != UserAgent() {
		t.Errorf("User-Agent = %q; want %q", ua, UserAgent())
	}
}

func TestHTTPSink_MethodAndHeaders(t *testing.T) {
	srv, reqs := newCaptureServer(t, http.StatusNoContent)
	sink := newHTTPSinkT(t, &HTTPTarget{
		URL:    srv.URL,
		Method: http.MethodPut,
		Headers: map[string]string{
			"Authorization": "Bearer xyz",
			"X-Custom":      "v1",
			"Content-Type":  "text/plain", // custom headers override defaults
		},
	})

	if err := sink.Send(t.Context(), []byte("hello"), ""); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := reqs()
	if len(got) != 1 {
		t.Fatalf("server received %d requests", len(got))
	}
	r := got[0]
	if r.method != http.MethodPut {
		t.Errorf("method = %q; want PUT", r.method)
	}
	if r.header.Get("Authorization") != "Bearer xyz" || r.header.Get("X-Custom") != "v1" {
		t.Errorf("headers = %v", r.header)
	}
	if ct := r.header.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q; want override text/plain", ct)
	}
	if string(r.body) != "hello" {
		t.Errorf("body = %q", r.body)
	}
}

func TestHTTPSink_StatusCodes(t *testing.T) {
	cases := []struct {
		status  int
		wantErr bool
	}{
		{http.StatusOK, false},
		{http.StatusAccepted, false},
		{http.StatusNoContent, false},
		{http.StatusBadRequest, true},
		{http.StatusNotFound, true},
		{http.StatusInternalServerError, true},
		{http.StatusServiceUnavailable, true},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			srv, _ := newCaptureServer(t, c.status)
			sink := newHTTPSinkT(t, &HTTPTarget{URL: srv.URL})
			err := sink.Send(t.Context(), []byte("{}"), "")
			if (err != nil) != c.wantErr {
				t.Fatalf("Send status %d err = %v; wantErr %v", c.status, err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "status") {
				t.Errorf("error %q should mention the status", err)
			}
		})
	}
}

func TestHTTPSink_Timeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	sink := newHTTPSinkT(t, &HTTPTarget{URL: srv.URL, Timeout: "100ms"})
	start := time.Now()
	err := sink.Send(t.Context(), []byte("{}"), "")
	if err == nil {
		t.Fatal("Send succeeded; want timeout error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took %v; expected ~100ms", elapsed)
	}
}

func TestHTTPSink_ContextCanceled(t *testing.T) {
	srv, reqs := newCaptureServer(t, http.StatusOK)
	sink := newHTTPSinkT(t, &HTTPTarget{URL: srv.URL})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := sink.Send(ctx, []byte("{}"), "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send(canceled) err = %v; want context.Canceled", err)
	}
	if n := len(reqs()); n != 0 {
		t.Errorf("server received %d requests for canceled ctx", n)
	}
}

func TestHTTPSink_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	sink := newHTTPSinkT(t, &HTTPTarget{URL: url, Timeout: "2s"})
	if err := sink.Send(t.Context(), []byte("{}"), ""); err == nil {
		t.Fatal("Send to closed server succeeded")
	}
}

func TestHTTPSink_BlockedByNetguard(t *testing.T) {
	// 169.254.0.0/16 is denied by the default policy; the dial must be
	// refused before any packet leaves.
	sink := newHTTPSinkT(t, &HTTPTarget{URL: "http://169.254.169.254/latest/meta-data", Timeout: "2s"})
	err := sink.Send(t.Context(), []byte("{}"), "")
	if err == nil {
		t.Fatal("Send to link-local metadata address succeeded")
	}
	var blocked *netguard.ErrBlocked
	if !errors.As(err, &blocked) {
		t.Errorf("err = %v; want *netguard.ErrBlocked in chain", err)
	}
}
