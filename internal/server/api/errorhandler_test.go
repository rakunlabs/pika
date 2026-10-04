package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/pika/internal/service"
)

func TestErrorHandler(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    int
		wantLog bool
	}{
		{"not found", fmt.Errorf("file x: %w", service.ErrNotFound), http.StatusNotFound, false},
		{"bad request", errors.Join(errors.New("bad"), service.ErrBadRequest), http.StatusBadRequest, false},
		{"body too large", fmt.Errorf("read: %w", &http.MaxBytesError{Limit: 1}), http.StatusRequestEntityTooLarge, false},
		{"internal", errors.New("boom"), http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))

			a := &api{}
			m := ada.New()
			m.ErrorHandler(a.errorHandler)
			m.GET("/x", m.Wrap(func(c *ada.Context) error { return tt.err }))

			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.Header.Set(mrequestid.HeaderXRequestID, "req-1")
			req = req.WithContext(logi.WithContext(req.Context(), logger))
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}

			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Message != tt.err.Error() {
				t.Fatalf("message = %q, want %q", body.Message, tt.err.Error())
			}

			logged := strings.Contains(logs.String(), "request failed")
			if logged != tt.wantLog {
				t.Fatalf("logged = %v, want %v (logs: %s)", logged, tt.wantLog, logs.String())
			}
			if tt.wantLog {
				if body.RequestID != "req-1" {
					t.Fatalf("request_id = %q, want req-1", body.RequestID)
				}
				if !strings.Contains(logs.String(), "boom") {
					t.Fatalf("log missing error text: %s", logs.String())
				}
			}
		})
	}
}
