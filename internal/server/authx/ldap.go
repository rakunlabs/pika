package authx

import (
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	"github.com/rakunlabs/ada/middleware/auth/strategy/ldap"

	"github.com/rakunlabs/pika/internal/ldapclient"
	"github.com/rakunlabs/pika/internal/service"
)

// BuildLDAP constructs a working LDAP login strategy from settings.
// Returns nil when settings are absent or incomplete.
//
// The strategy is plugged into ada's LDAP package, which calls into the
// pika ldapclient connector for the actual LDAP wire protocol. The
// connector is shared with the user-sync engine so creds and TLS settings
// live in one place.
//
// LDAP attribute mapping for login: defaults to (uid, mail, cn). The
// user-sync layer carries a richer mapping that admins configure per
// source; for the login leg we keep the historical defaults so plain
// LDAP installs work with no extra setup.
//
// NOTE: BindPassword is currently plaintext, matching the rest of
// AuthSettings. TODO: re-encrypt via secret store.
func BuildLDAP(s *service.LDAPStrategySettings) (strategy.Authenticator, error) {
	if s == nil || s.Addr == "" {
		return nil, nil
	}
	name := s.Name
	if name == "" {
		name = "ldap"
	}

	connector := ldapclient.New(ldapclient.Config{
		Address:      s.Addr,
		TLS:          s.TLS,
		InsecureSkip: s.InsecureSkip,
	})

	cfg := ldap.Config{
		Address:      s.Addr,
		BaseDN:       s.UserBaseDN,
		BindDN:       s.BindDN,
		BindPassword: s.BindPassword, // TODO: decrypt via secret store
		UserFilter:   s.UserFilter,
	}
	if cfg.UserFilter == "" {
		cfg.UserFilter = "(uid=%s)"
	}

	attrs := ldap.AttributeMap{
		Subject: "uid",
		Email:   "mail",
		Name:    "cn",
	}

	return ldap.New(name, connector, cfg, attrs), nil
}
