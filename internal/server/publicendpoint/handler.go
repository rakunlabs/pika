package publicendpoint

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/pika/internal/service"
)

// BuildHandlerForProbe wraps buildHandler for callers outside the
// package — specifically the admin "/test" endpoint which wants to
// exercise a not-yet-running endpoint's pipeline without binding a
// real socket. The exported name discourages mis-use: production
// traffic always goes through the manager's live listener; this
// shape is for diagnostics only.
func BuildHandlerForProbe(ep service.PublicEndpoint, svc Service, logger *slog.Logger) (http.Handler, error) {
	return buildHandler(ep, svc, logger)
}

// buildHandler wires the per-endpoint shim, request-check, and
// auth chain. It is the single dispatch point that translates a
// PublicEndpoint config into a serving http.Handler, used both by
// the live listener path and the diagnostic /test path
// (HandlerForID re-exposes whichever handler the live listener is
// using).
//
// The middleware order is:
//
//	recover -> logger -> auth -> request_check -> shim
//
// recover sits outermost so a panic in the chain still emits a
// 500 instead of crashing the goroutine; logger is next so we
// always log the outcome; auth gates everything below it;
// request_check inspects/modifies/blocks an authenticated request
// before it reaches the shim. The shim sees the post-modify
// request (path, query, headers may differ from the wire).
func buildHandler(ep service.PublicEndpoint, svc Service, logger *slog.Logger) (http.Handler, error) {
	var shim http.Handler
	switch ep.Mode {
	case "static":
		shim = newStaticHandler(ep, svc)
	case "consul":
		shim = newConsulHandler(ep, svc)
	case "external":
		shim = newExternalHandler(ep, svc)
	case "custom":
		h, err := newCustomHandler(ep, svc)
		if err != nil {
			return nil, fmt.Errorf("build custom handler: %w", err)
		}
		shim = h
	default:
		return nil, fmt.Errorf("unsupported mode %q", ep.Mode)
	}

	chained := requestCheckMiddleware(ep.RequestCheck, shim)
	chained = authMiddleware(ep, svc, chained)
	chained = requestLogger(ep, logger, chained)
	chained = recoverMiddleware(ep, logger, chained)
	return chained, nil
}

// recoverMiddleware turns a panic in the downstream chain into a 500
// response so a malformed template or a shim regression never tears
// down the listener goroutine.
func recoverMiddleware(ep service.PublicEndpoint, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("public endpoint panic",
					"id", ep.ID, "name", ep.Name, "path", r.URL.Path,
					"panic", fmt.Sprintf("%v", rec))
				writeJSONError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestLogger emits a single structured log line per request,
// matching the verbosity the admin port uses. The logger is the
// manager-level slog handle so all endpoint traffic ends up in the
// same log stream operators are already watching.
func requestLogger(ep service.PublicEndpoint, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Info("public endpoint request",
			"id", ep.ID, "name", ep.Name,
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status,
			"remote", r.RemoteAddr)
	})
}

// statusRecorder captures the response status code for the logger
// without buffering the body. WriteHeader can legally be called
// zero or one time; if the handler writes a body without calling
// WriteHeader, net/http will materialise an implicit 200 and our
// default value already covers it.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.written {
		s.written = true
	}
	return s.ResponseWriter.Write(b)
}
