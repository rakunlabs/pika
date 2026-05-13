package authx

import (
	"github.com/rakunlabs/pika/internal/service"
)

// BuildTOTPService instantiates the TOTP coordinator. The issuer
// string baked into otpauth URLs comes from the configured login UI
// title with a sensible fallback — operators have already branded
// the login screen, no reason to ask for the same string twice.
//
// Returns nil only when settings are nil — TOTP itself has no
// settings.AuthSettings sub-block by design. Enrollment is a per-user
// choice; there's nothing for an operator to configure beyond "is the
// feature wired in or not". If we add policy knobs later (force-MFA
// for admin roles, exempt certain providers, etc.) they'll live in a
// future TOTPSettings struct alongside PasskeyStrategySettings.
//
// The returned service is always wired even when no user is yet
// enrolled — the MFA strategy wrappers nil-check IsEnabledForUser
// per-request, so having coord != nil with zero enrolled users is
// the resting state of a fresh install.
func BuildTOTPService(svc *service.Service, s *service.AuthSettings) *service.TOTPService {
	if s == nil {
		return nil
	}
	issuer := s.UI.Title
	if issuer == "" {
		issuer = "Pika"
	}
	return service.NewTOTPService(svc, issuer)
}
