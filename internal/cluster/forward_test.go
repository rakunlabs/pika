package cluster

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	t.Parallel()

	body := strings.NewReader(`{"hello":"world"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/file/foo/bar?variant=prod", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cookie", "pika_session=abc123")
	r.Header.Set("X-Custom", "yes")
	r.RemoteAddr = "10.0.0.5:51234"

	payload, err := SerializeRequest(r)
	if err != nil {
		t.Fatalf("SerializeRequest: %v", err)
	}

	got, err := DeserializeRequest(payload)
	if err != nil {
		t.Fatalf("DeserializeRequest: %v", err)
	}

	if got.Method != http.MethodPost {
		t.Errorf("method: got %q, want POST", got.Method)
	}
	if got.URL.Path != "/api/v1/file/foo/bar" {
		t.Errorf("path: got %q, want /api/v1/file/foo/bar", got.URL.Path)
	}
	if got.URL.Query().Get("variant") != "prod" {
		t.Errorf("query: variant=%q, want prod", got.URL.Query().Get("variant"))
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type: %q", got.Header.Get("Content-Type"))
	}
	if got.Header.Get("Cookie") != "pika_session=abc123" {
		t.Errorf("Cookie not preserved: %q", got.Header.Get("Cookie"))
	}
	if got.Header.Get("X-Custom") != "yes" {
		t.Errorf("X-Custom header dropped")
	}
	if got.RemoteAddr != "10.0.0.5:51234" {
		t.Errorf("RemoteAddr not preserved: %q", got.RemoteAddr)
	}

	gotBody, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("read forwarded body: %v", err)
	}
	if string(gotBody) != `{"hello":"world"}` {
		t.Errorf("body: got %q", string(gotBody))
	}
}

func TestResponseRoundTripThroughRunForwarded(t *testing.T) {
	t.Parallel()

	// Leader-side handler that echoes the request body and adds two headers.
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Echo-Method", r.Method)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("echo:" + string(body)))
	})

	// Build a forwardable request, run it through runForwardedRequest, then
	// decode the response back to a real ResponseWriter.
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader("payload"))
	req.Header.Set("Content-Type", "text/plain")
	payload, err := SerializeRequest(req)
	if err != nil {
		t.Fatalf("SerializeRequest: %v", err)
	}

	respBytes := runForwardedRequest(context.Background(), mux, payload)

	rec := httptest.NewRecorder()
	if err := WriteForwardedResponse(rec, respBytes); err != nil {
		t.Fatalf("WriteForwardedResponse: %v", err)
	}

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type: %q", got)
	}
	if got := rec.Header().Get("X-Echo-Method"); got != http.MethodPost {
		t.Errorf("X-Echo-Method: %q", got)
	}
	if got := rec.Body.String(); got != "echo:payload" {
		t.Errorf("body: %q", got)
	}
}

func TestEncodeForwardError(t *testing.T) {
	t.Parallel()

	payload := encodeForwardError(http.StatusServiceUnavailable, "leader gone")

	rec := httptest.NewRecorder()
	if err := WriteForwardedResponse(rec, payload); err != nil {
		t.Fatalf("WriteForwardedResponse: %v", err)
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
	if rec.Header().Get("X-Pika-Cluster-Error") != "1" {
		t.Errorf("missing X-Pika-Cluster-Error marker")
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("leader gone")) {
		t.Errorf("body missing message: %q", rec.Body.String())
	}
}

func TestDeserializeRequest_Truncated(t *testing.T) {
	t.Parallel()

	if _, err := DeserializeRequest(nil); err == nil {
		t.Errorf("expected error on nil")
	}
	if _, err := DeserializeRequest([]byte{0, 0, 0, 100}); err == nil {
		t.Errorf("expected error on truncated payload")
	}
}
