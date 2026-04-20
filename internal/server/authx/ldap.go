package authx

import (
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	"github.com/rakunlabs/ada/middleware/auth/strategy/ldap"

	"github.com/rakunlabs/pika/internal/service"
)

// BuildLDAP constructs an LDAP strategy from settings. Returns nil when
// settings are absent or incomplete.
//
// NOTE: BindPassword is treated as plaintext — TODO: re-encrypt via secret store (Task 20).
//
// NOTE: ada's ldap.Config only supports Address, BaseDN, BindDN, BindPassword, UserFilter.
// LDAPStrategySettings fields GroupBaseDN, GroupFilter, TLS, InsecureSkip have no
// corresponding fields in the ada LDAP Config at this time — they are stored but
// not forwarded until ada adds support.
//
// NOTE: ldap.New requires a non-nil Connector. Since pika ships no LDAP connector
// implementation, BuildLDAP returns nil with a TODO if settings are present.
// TODO: implement a go-ldap-based Connector and wire it here.
func BuildLDAP(s *service.LDAPStrategySettings) (strategy.Authenticator, error) {
	if s == nil || s.Addr == "" {
		return nil, nil
	}
	name := s.Name
	if name == "" {
		name = "ldap"
	}
	// TODO: decrypt s.BindPassword via secret store when available.
	pw := s.BindPassword

	cfg := ldap.Config{
		Address:      s.Addr,
		BindDN:       s.BindDN,
		BindPassword: pw,
		UserFilter:   s.UserFilter,
	}
	attrs := ldap.AttributeMap{
		Subject: "uid",
		Email:   "mail",
		Name:    "cn",
	}

	// TODO: supply a real Connector implementation (e.g. using go-ldap).
	// Returning nil for now — LDAP strategy is not functional until a
	// Connector is provided. This stub prevents build failures.
	_ = name
	_ = cfg
	_ = attrs
	return nil, nil
}
