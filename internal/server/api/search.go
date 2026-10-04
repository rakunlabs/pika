package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/pika/internal/service"
)

// searchHandler uses SSE to stream search results as they are found.
// The client can abort the connection to cancel the search.
func (a *api) searchHandler(w http.ResponseWriter, r *http.Request) {
	// Inline permission check — this is a raw http.Handler, not an ada
	// handler, so it can't use the withPerm wrapper.
	caps := service.CapabilitiesFromContext(r.Context())
	if !caps.Has(service.CapFilesRead) {
		http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
		return
	}
	patterns := service.CapabilityPatternsFromContext(r.Context())

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, `{"message":"query parameter 'q' is required"}`, http.StatusBadRequest)
		return
	}

	// mode=name skips reading file contents entirely (faster + safer,
	// no content scanning). Default (omitted or any other value) keeps
	// the existing path + content behaviour for backward compatibility.
	nameOnly := r.URL.Query().Get("mode") == "name"

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"message":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	// CORS headers are owned by the global CORS middleware (server.cors).
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Use a cancellable context — cancelled when client disconnects
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	results := make(chan service.SearchResult, 10)

	// Run search in background
	searchErr := make(chan error, 1)
	go func() {
		searchErr <- a.svc.Search(ctx, service.SearchOptions{Query: query, NameOnly: nameOnly}, results)
	}()

	// Stream results as SSE events. When the user's files.read grant is
	// path-scoped, each result's Path must match before being forwarded.
	// Empty patterns (the common case) are a no-op: Allows returns true.
	for result := range results {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !patterns.Allows(service.CapFilesRead, result.Path) {
			continue
		}

		data, err := json.Marshal(result)
		if err != nil {
			continue
		}

		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	if err := <-searchErr; err != nil && ctx.Err() == nil {
		logi.Ctx(ctx).ErrorContext(ctx, "search failed", slog.String("error", err.Error()))
		_, _ = fmt.Fprint(w, "event: error\ndata: {\"message\":\"search failed\"}\n\n")
	}

	// Send done event
	_, _ = fmt.Fprint(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}
