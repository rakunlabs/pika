package server

import (
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/config"
)

// bodyLimitMiddleware caps request bodies with http.MaxBytesReader so a
// single request can't make pika (or the cluster forwarder, which
// buffers bodies) allocate unbounded memory. Backup restore gets its own,
// larger limit. A limit of 0 disables the cap.
func bodyLimitMiddleware(limits config.Limits, basePath string) func(http.Handler) http.Handler {
	defaultLimit := limits.RequestBodyBytes()
	backupLimit := limits.BackupBodyBytes()
	backupPath := basePath + "/api/v1/backup"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := defaultLimit
			if r.Method == http.MethodPost && strings.TrimRight(r.URL.Path, "/") == backupPath {
				limit = backupLimit
			}

			if limit > 0 && r.Body != nil && r.Body != http.NoBody {
				if r.ContentLength > limit {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					_, _ = w.Write([]byte(`{"message":"request body too large"}`))
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}

			next.ServeHTTP(w, r)
		})
	}
}
