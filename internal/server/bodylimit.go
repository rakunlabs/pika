package server

import (
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/config"
)

// bodyLimitMiddleware caps request bodies with http.MaxBytesReader so a
// single request can't make pika (or the cluster forwarder, which
// buffers bodies) allocate unbounded memory. Backup restore and vault file
// uploads get their own limits. A limit of 0 disables the cap.
func bodyLimitMiddleware(limits config.Limits, basePath string) func(http.Handler) http.Handler {
	defaultLimit := limits.RequestBodyBytes()
	backupLimit := limits.BackupBodyBytes()
	backupPath := basePath + "/api/v1/backup"
	vaultFileLimit := limits.VaultFileBodyBytes()
	vaultFilePath := basePath + "/api/v1/me/vault/files-upload"
	vaultContentPrefix := basePath + "/api/v1/me/vault/files-content/"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := defaultLimit
			if r.Method == http.MethodPost && strings.TrimRight(r.URL.Path, "/") == backupPath {
				limit = backupLimit
			}
			if r.Method == http.MethodPut && (r.URL.Path == vaultFilePath || strings.HasPrefix(r.URL.Path, vaultContentPrefix)) {
				limit = vaultFileLimit
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
