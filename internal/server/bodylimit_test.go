package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/config"
)

func TestBodyLimitMiddleware(t *testing.T) {
	limits := config.Limits{RequestBodyMB: 1, BackupBodyMB: 2}
	h := bodyLimitMiddleware(limits, "/pika")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name   string
		method string
		path   string
		size   int
		chunk  bool
		want   int
	}{
		{"small body", http.MethodPost, "/pika/api/v1/file/a", 1024, false, http.StatusOK},
		{"over default limit", http.MethodPost, "/pika/api/v1/file/a", 1<<20 + 1, false, http.StatusRequestEntityTooLarge},
		{"over default limit chunked", http.MethodPost, "/pika/api/v1/file/a", 1<<20 + 1, true, http.StatusRequestEntityTooLarge},
		{"backup uses larger limit", http.MethodPost, "/pika/api/v1/backup", 1<<20 + 1, false, http.StatusOK},
		{"over backup limit", http.MethodPost, "/pika/api/v1/backup", 2<<20 + 1, false, http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader = strings.NewReader(strings.Repeat("a", tt.size))
			if tt.chunk {
				body = io.MultiReader(body) // hides the length
			}
			req := httptest.NewRequest(tt.method, tt.path, body)
			if tt.chunk {
				req.ContentLength = -1
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestBodyLimitMiddlewareDisabled(t *testing.T) {
	h := bodyLimitMiddleware(config.Limits{}, "")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/file/a", strings.NewReader(strings.Repeat("a", 2<<20)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
