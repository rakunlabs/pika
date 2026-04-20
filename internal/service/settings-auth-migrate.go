package service

// MigrateLegacyAuthSettings converts ForwardAuthSettings + ExternalPermissionsSettings
// into the new AuthSettings shape, in place. Returns the same Settings pointer.
//
// Idempotent: if Auth is already populated, the function leaves the existing
// value untouched and clears only truly-legacy fields.
func MigrateLegacyAuthSettings(s *Settings) *Settings {
	if s == nil {
		return s
	}

	if s.Auth != nil {
		s.ForwardAuth = nil
		s.ExternalPermissions = nil
		return s
	}

	if s.ForwardAuth == nil && s.ExternalPermissions == nil {
		return s
	}

	out := &AuthSettings{}

	if fa := s.ForwardAuth; fa != nil && fa.Enabled {
		out.Header = &HeaderStrategySettings{
			Name: "header",
		}
	}

	if ep := s.ExternalPermissions; ep != nil && ep.Enabled {
		if out.Header == nil {
			out.Header = &HeaderStrategySettings{Name: "header"}
		}
		if ep.GroupsHeader != "" {
			out.Header.Groups = ep.GroupsHeader
		}

		out.Capabilities = CapabilityMapping{
			Superadmins: append([]string(nil), ep.Superadmins...),
		}
		if len(ep.Mapping) > 0 {
			out.Capabilities.RoleMapping = make(map[string][]string, len(ep.Mapping))
			for k, v := range ep.Mapping {
				out.Capabilities.RoleMapping[k] = append([]string(nil), v...)
			}
		}
	}

	s.Auth = out
	s.ForwardAuth = nil
	s.ExternalPermissions = nil

	return s
}
