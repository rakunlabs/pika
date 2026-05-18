package proxy

import (
	"net/http"

	mlog "github.com/rakunlabs/ada/middleware/log"
)

// adaLogMiddleware returns ada's structured request logger as a
// stdlib middleware. Isolated in its own file so the import doesn't
// leak into middlewares.go (which would force every test that
// builds middlewares to pull in the ada log package transitively).
func adaLogMiddleware() func(http.Handler) http.Handler {
	return mlog.Middleware()
}
