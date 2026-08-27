package authx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/ada/middleware/auth/guard"
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	authheader "github.com/rakunlabs/ada/middleware/auth/strategy/header"

	"github.com/rakunlabs/pika/internal/service"
)

// BuildHeader constructs the header strategy from settings. Returns nil when
// settings are absent.
//
// The trust boundary is ada's now. pika used to wrap the strategy in its own
// CIDR guard; the strategy enforces the same check internally, and doing it
// there means the shared-secret option composes with it and a rejected caller
// gets the same answer as one with no header at all — an outsider learns
// nothing about why it failed.
//
// Header auth has no verifier by construction: whoever can set
// X-Forwarded-User is whoever the request claims to be. Without
// TrustedProxies that is only sound on a network where nothing but the proxy
// can reach this port, so a deployment that leaves it empty gets a warning
// from ada at construction — worth reading rather than silencing.
func BuildHeader(s *service.HeaderStrategySettings) strategy.Authenticator {
	if s == nil {
		return nil
	}

	name := s.Name
	if name == "" {
		name = "header"
	}

	opts := []authheader.Option{
		authheader.WithHeaderMap(authheader.HeaderMap{
			User:   s.User,
			Email:  s.Email,
			Name:   s.DisplayName,
			Roles:  s.Roles,
			Groups: s.Groups,
		}),
	}

	// Validate before handing the list over: ada panics on a malformed
	// CIDR, and these values come from an editable settings form that is
	// re-applied on every hot reload. A typo must not take the server
	// down — but it must not silently widen the boundary either, so an
	// unusable list is dropped whole and logged.
	if len(s.TrustedProxies) > 0 {
		if _, err := guard.ParseCIDRs(s.TrustedProxies); err != nil {
			slog.Error("header strategy: ignoring trusted_proxies, list is not parseable",
				"strategy", name,
				"error", err.Error(),
			)
		} else {
			opts = append(opts, authheader.WithTrustedProxies(s.TrustedProxies...))
		}
	}

	return authheader.New(name, opts...)
}

func writeJSONErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}
