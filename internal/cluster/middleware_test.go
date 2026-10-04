package cluster

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/bw"
)

type recordingHandler struct {
	calls  int
	method string
	path   string
	body   string
	header http.Header
	status int
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls++
	h.method = r.Method
	h.path = r.URL.Path
	h.header = r.Header.Clone()
	b, _ := io.ReadAll(r.Body)
	h.body = string(b)
	w.Header().Set("X-Handled", "1")
	status := h.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte("ok"))
}

func TestMiddleware_SingleNodePassThrough(t *testing.T) {
	t.Parallel()

	c, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Enabled() {
		t.Fatal("disabled cluster reports Enabled")
	}
	if !c.IsLeader() {
		t.Fatal("single-node cluster must always be leader")
	}

	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			h := &recordingHandler{status: http.StatusCreated}
			mw := c.Middleware()(h)

			req := httptest.NewRequest(method, "/api/v1/thing", strings.NewReader("data"))
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)

			if h.calls != 1 {
				t.Fatalf("handler calls = %d; want 1", h.calls)
			}
			if h.method != method || h.path != "/api/v1/thing" {
				t.Errorf("handler saw %s %s", h.method, h.path)
			}
			if rec.Code != http.StatusCreated {
				t.Errorf("status = %d; want 201", rec.Code)
			}
			if rec.Header().Get("X-Handled") != "1" {
				t.Error("handler header not propagated")
			}
			if h.header.Get(internalForwardHeader) != "" {
				t.Error("single-node mode must not mark requests as forwarded")
			}
			if method != http.MethodHead && h.body != "data" {
				t.Errorf("body = %q", h.body)
			}
		})
	}
}

func TestMiddleware_NilClusterPassThrough(t *testing.T) {
	t.Parallel()

	var c *Cluster
	h := &recordingHandler{}
	mw := c.Middleware()(h)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if h.calls != 1 || rec.Code != http.StatusOK {
		t.Fatalf("nil cluster: calls=%d status=%d", h.calls, rec.Code)
	}
	if !c.IsLeader() || c.Enabled() {
		t.Error("nil cluster should be leader and disabled")
	}
	if err := c.NotifySync(t.Context()); err != nil {
		t.Errorf("nil NotifySync: %v", err)
	}
	if _, err := c.Forward(t.Context(), nil); !errors.Is(err, ErrNoLeader) {
		t.Errorf("nil Forward err = %v; want ErrNoLeader", err)
	}
}

func TestIsReadMethod(t *testing.T) {
	t.Parallel()

	for m, want := range map[string]bool{
		"GET": true, "get": true, "HEAD": true, "OPTIONS": true,
		"POST": false, "PUT": false, "PATCH": false, "DELETE": false, "": false, "PROPFIND": false,
	} {
		if got := isReadMethod(m); got != want {
			t.Errorf("isReadMethod(%q) = %v; want %v", m, got, want)
		}
	}
}

func TestWriteNotifier_CapturesStatus(t *testing.T) {
	t.Parallel()

	c, _ := New(Config{}, nil)

	rec := httptest.NewRecorder()
	n := &writeNotifier{ResponseWriter: rec, cluster: c, ctx: t.Context()}
	n.WriteHeader(http.StatusAccepted)
	n.WriteHeader(http.StatusInternalServerError) // ignored
	_, _ = n.Write([]byte("x"))
	if n.statusCode != http.StatusAccepted || rec.Code != http.StatusAccepted {
		t.Errorf("status = %d / %d; want 202", n.statusCode, rec.Code)
	}
	n.Flush()
	if !rec.Flushed {
		t.Error("Flush not forwarded")
	}
	n.maybeNotify() // disabled cluster: must not panic

	rec2 := httptest.NewRecorder()
	n2 := &writeNotifier{ResponseWriter: rec2, cluster: c}
	_, _ = n2.Write([]byte("implicit"))
	if n2.statusCode != http.StatusOK || !n2.headerWritten {
		t.Errorf("implicit Write status = %d, written=%v", n2.statusCode, n2.headerWritten)
	}
	n2.maybeNotify() // nil ctx path

	n3 := &writeNotifier{ResponseWriter: httptest.NewRecorder(), cluster: c}
	n3.maybeNotify() // nothing written
}

func newUnstartedEnabledCluster(t *testing.T) *Cluster {
	t.Helper()
	db, err := bw.Open("", bw.WithInMemory(true), bw.WithLogger(nil))
	if err != nil {
		t.Fatalf("bw.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	c, err := New(Config{
		Enabled:        true,
		BindAddr:       "127.0.0.1",
		Replicas:       3,
		ForwardTimeout: 200 * time.Millisecond,
	}, db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNew_EnabledRequiresDB(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{Enabled: true}, nil); err == nil {
		t.Fatal("New(enabled, nil db) succeeded")
	}
}

func TestMiddleware_FollowerReadsStayLocal(t *testing.T) {
	t.Parallel()

	c := newUnstartedEnabledCluster(t)
	if c.IsLeader() {
		t.Skip("unstarted cluster unexpectedly reports leader")
	}
	h := &recordingHandler{}
	mw := c.Middleware()(h)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/read", nil))
	if h.calls != 1 || rec.Code != http.StatusOK {
		t.Fatalf("follower read: calls=%d status=%d", h.calls, rec.Code)
	}
}

func TestMiddleware_FollowerRejectsForwardedLoop(t *testing.T) {
	t.Parallel()

	c := newUnstartedEnabledCluster(t)
	if c.IsLeader() {
		t.Skip("unstarted cluster unexpectedly reports leader")
	}
	h := &recordingHandler{}
	mw := c.Middleware()(h)

	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader("x"))
	req.Header.Set(internalForwardHeader, "1")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if h.calls != 0 {
		t.Error("follower executed a forwarded write locally")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503", rec.Code)
	}
}

func TestMiddleware_FollowerNoLeader(t *testing.T) {
	t.Parallel()

	c := newUnstartedEnabledCluster(t)
	if c.IsLeader() {
		t.Skip("unstarted cluster unexpectedly reports leader")
	}
	h := &recordingHandler{}
	mw := c.Middleware()(h)

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/write", strings.NewReader("x")))

	if h.calls != 0 {
		t.Error("follower executed a write locally instead of forwarding")
	}
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d; want 503 or 502", rec.Code)
	}
}

func TestMiddleware_FollowerBodyTooLarge(t *testing.T) {
	t.Parallel()

	c := newUnstartedEnabledCluster(t)
	if c.IsLeader() {
		t.Skip("unstarted cluster unexpectedly reports leader")
	}
	h := &recordingHandler{}
	mw := c.Middleware()(h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/write", strings.NewReader(strings.Repeat("a", 100)))
	req.Body = http.MaxBytesReader(rec, req.Body, 10)
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d; want 413", rec.Code)
	}
}

func TestSerializeRequest_EmptyBodyAndRestoredBody(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/x", nil)
	r.RemoteAddr = "[::1]:9999"
	payload, err := SerializeRequest(r)
	if err != nil {
		t.Fatalf("SerializeRequest: %v", err)
	}
	got, err := DeserializeRequest(payload)
	if err != nil {
		t.Fatalf("DeserializeRequest: %v", err)
	}
	if got.Method != http.MethodDelete || got.URL.Path != "/api/v1/x" {
		t.Errorf("got %s %s", got.Method, got.URL.Path)
	}
	if got.RemoteAddr != "[::1]:9999" {
		t.Errorf("RemoteAddr = %q", got.RemoteAddr)
	}
	b, _ := io.ReadAll(got.Body)
	if len(b) != 0 {
		t.Errorf("body = %q; want empty", b)
	}

	// The original request body must remain readable after serialization.
	r2 := httptest.NewRequest(http.MethodPut, "/y", strings.NewReader("keep me"))
	if _, err := SerializeRequest(r2); err != nil {
		t.Fatalf("SerializeRequest: %v", err)
	}
	b, _ = io.ReadAll(r2.Body)
	if string(b) != "keep me" {
		t.Errorf("original body after serialize = %q", b)
	}
}

func TestSerializeRequest_BinaryBodyAndMultiValueHeaders(t *testing.T) {
	t.Parallel()

	body := []byte{0x00, 0x01, 0xFF, '\r', '\n', '\r', '\n', 0x7F}
	r := httptest.NewRequest(http.MethodPost, "/bin?a=1&a=2", bytes.NewReader(body))
	r.Header.Add("X-Multi", "one")
	r.Header.Add("X-Multi", "two")
	r.Header.Set("Authorization", "Bearer t")

	payload, err := SerializeRequest(r)
	if err != nil {
		t.Fatalf("SerializeRequest: %v", err)
	}
	got, err := DeserializeRequest(payload)
	if err != nil {
		t.Fatalf("DeserializeRequest: %v", err)
	}
	gb, _ := io.ReadAll(got.Body)
	if !bytes.Equal(gb, body) {
		t.Errorf("body = %v; want %v", gb, body)
	}
	if vs := got.Header.Values("X-Multi"); len(vs) != 2 || vs[0] != "one" || vs[1] != "two" {
		t.Errorf("X-Multi = %v", vs)
	}
	if got.Header.Get("Authorization") != "Bearer t" {
		t.Errorf("Authorization = %q", got.Header.Get("Authorization"))
	}
	if qs := got.URL.Query()["a"]; len(qs) != 2 {
		t.Errorf("query a = %v", qs)
	}
	if got.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength = %d; want %d", got.ContentLength, len(body))
	}
}

func TestDeserializeRequest_NoRemoteTrailer(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/z", strings.NewReader("b"))
	r.RemoteAddr = "1.2.3.4:5"
	payload, err := SerializeRequest(r)
	if err != nil {
		t.Fatalf("SerializeRequest: %v", err)
	}
	reqLen := binary.BigEndian.Uint32(payload[:4])
	trimmed := payload[:4+reqLen]

	got, err := DeserializeRequest(trimmed)
	if err != nil {
		t.Fatalf("DeserializeRequest without trailer: %v", err)
	}
	if got.RemoteAddr != "" {
		t.Errorf("RemoteAddr = %q; want empty", got.RemoteAddr)
	}

	// Truncated trailer: length says 100 but only a few bytes follow.
	bad := append(bytes.Clone(trimmed), 0, 0, 0, 100, 'x')
	got, err = DeserializeRequest(bad)
	if err != nil {
		t.Fatalf("DeserializeRequest truncated trailer: %v", err)
	}
	if got.RemoteAddr != "" {
		t.Errorf("RemoteAddr from truncated trailer = %q; want empty", got.RemoteAddr)
	}
}

func TestDeserializeRequest_Garbage(t *testing.T) {
	t.Parallel()

	data := append([]byte{0, 0, 0, 5}, []byte("hello")...)
	if _, err := DeserializeRequest(data); err == nil {
		t.Fatal("DeserializeRequest(garbage) succeeded")
	}
}

func TestRunForwardedRequest_BadPayload(t *testing.T) {
	t.Parallel()

	resp := runForwardedRequest(t.Context(), http.NotFoundHandler(), []byte{1})
	rec := httptest.NewRecorder()
	if err := WriteForwardedResponse(rec, resp); err != nil {
		t.Fatalf("WriteForwardedResponse: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
	if rec.Header().Get("X-Pika-Cluster-Error") != "1" {
		t.Error("missing cluster error marker")
	}
}

func TestWriteForwardedResponse_Malformed(t *testing.T) {
	t.Parallel()

	good := encodeResponse(http.StatusOK, http.Header{"X-A": {"b"}}, []byte("body"))
	cases := map[string][]byte{
		"empty":            nil,
		"bad version":      append([]byte{99}, good[1:]...),
		"short header":     good[:4],
		"truncated header": good[:8],
		"truncated body":   good[:len(good)-2],
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if err := WriteForwardedResponse(rec, payload); err != nil {
				t.Fatalf("WriteForwardedResponse: %v", err)
			}
			if rec.Code != http.StatusBadGateway {
				t.Errorf("status = %d; want 502", rec.Code)
			}
		})
	}
}

func TestWriteForwardedResponse_StripsHopByHop(t *testing.T) {
	t.Parallel()

	hdr := http.Header{}
	hdr.Set("Connection", "close")
	hdr.Set("Transfer-Encoding", "chunked")
	hdr.Set("Upgrade", "websocket")
	hdr.Set("Keep-Alive", "timeout=5")
	hdr.Add("Set-Cookie", "a=1")
	hdr.Add("Set-Cookie", "b=2")
	payload := encodeResponse(http.StatusTeapot, hdr, []byte("tea"))

	rec := httptest.NewRecorder()
	if err := WriteForwardedResponse(rec, payload); err != nil {
		t.Fatalf("WriteForwardedResponse: %v", err)
	}
	if rec.Code != http.StatusTeapot || rec.Body.String() != "tea" {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
	for _, h := range []string{"Connection", "Transfer-Encoding", "Upgrade", "Keep-Alive"} {
		if rec.Header().Get(h) != "" {
			t.Errorf("hop-by-hop header %s forwarded", h)
		}
	}
	if vs := rec.Header().Values("Set-Cookie"); len(vs) != 2 {
		t.Errorf("Set-Cookie = %v; want 2 values", vs)
	}
}

func TestIsHopByHopHeader(t *testing.T) {
	t.Parallel()

	for _, h := range []string{"connection", "Keep-Alive", "proxy-authenticate", "Proxy-Authorization", "te", "Trailer", "transfer-encoding", "Upgrade"} {
		if !isHopByHopHeader(h) {
			t.Errorf("isHopByHopHeader(%q) = false", h)
		}
	}
	for _, h := range []string{"Content-Type", "Set-Cookie", "Authorization", "X-Custom"} {
		if isHopByHopHeader(h) {
			t.Errorf("isHopByHopHeader(%q) = true", h)
		}
	}
}
