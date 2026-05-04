// Package usersync runs periodic and on-demand reconciliation of pika's
// local users table against an external directory (currently LDAP).
//
// Two paths exist for getting external users into pika:
//
//  1. JIT (just-in-time) — when a user logs in via the LDAP strategy, ada
//     hands an *identity.Identity to the session store, which calls
//     Service.FindOrCreateExternalUser. That path is unaffected by this
//     package; the user shows up the moment they log in.
//
//  2. Batch — this package. Walks the directory, calls
//     FindOrCreateExternalUser for every match (idempotent), then reconciles
//     missing users + per-user permissions per the SyncSource policy.
//
// The same SyncSource also drives JIT permission grants when the LDAP
// strategy carries a "groups" attribute on the Identity — the resolver in
// authx/caps.go will look up the source's GroupPermissions map so a
// freshly-logged-in user gets the right grants on their first request.
package usersync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/ldapclient"
	"github.com/rakunlabs/pika/internal/service"
)

// Report summarizes a single sync run for the API/UI.
type Report struct {
	SourceID     string    `json:"source_id"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	Found        int       `json:"found"`         // entries returned by the directory search
	Created      int       `json:"created"`       // brand-new pika users provisioned
	Updated      int       `json:"updated"`       // existing users whose attributes were refreshed
	Disabled     int       `json:"disabled"`      // local users disabled by reconciliation
	PermsApplied int       `json:"perms_applied"` // user_permissions rows written
	Errors       []string  `json:"errors,omitempty"`
}

// Syncer runs sync against a single SyncSource. Stateless across runs;
// safe to invoke from a scheduler goroutine and from a manual API call
// concurrently (the underlying service layer serializes writes per user).
type Syncer struct {
	svc *service.Service
}

// New returns a Syncer bound to the given service.
func New(svc *service.Service) *Syncer { return &Syncer{svc: svc} }

// Run executes one full sync for the given source. Returns a Report
// regardless of partial errors — failures are accumulated rather than
// short-circuited so an admin can see "27 of 30 users synced, 3 errors".
//
// Steps:
//  1. Validate source config; reject silently-misconfigured sources up-front.
//  2. Connect + bind, paged search of UserBaseDN with UserFilter.
//  3. For each entry: derive ExternalIdentityInput, call
//     FindOrCreateExternalUser (handles new vs existing + email auto-link).
//  4. Refresh email/display_name on the resulting user when they differ.
//  5. Recompute group→permission grants and write them via
//     SetUserPermissionsBySource (only this source's rows are touched).
//  6. Reconciliation: for every user_identities row tagged with this
//     source ID that wasn't seen this run, apply OnMissing policy.
func (s *Syncer) Run(ctx context.Context, src service.SyncSource) Report {
	report := Report{SourceID: src.ID, StartedAt: time.Now()}
	defer func() { report.FinishedAt = time.Now() }()

	if err := validateSource(src); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}

	spec := src.LDAP

	// Build the connector. Each Run gets a fresh connection — sync isn't
	// hot enough for pooling to matter, and a fresh bind per run avoids
	// stale-connection failure modes.
	connector := ldapclient.New(ldapclient.Config{
		Address:      spec.Address,
		TLS:          spec.TLS,
		InsecureSkip: spec.InsecureSkip,
	})

	conn, err := connector.NewConn(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("connect: %v", err))
		return report
	}
	defer conn.Close()

	if err := conn.Bind(spec.BindDN, spec.BindPassword); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("bind: %v", err))
		return report
	}

	filter := spec.UserFilter
	if filter == "" {
		filter = "(objectClass=*)"
	}

	entries, err := conn.SearchAll(spec.UserBaseDN, filter, requestedAttrs(spec.Attributes), spec.PageSize)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("search: %v", err))
		return report
	}
	report.Found = len(entries)

	// Track which subjects we saw so reconciliation can disable the rest.
	seenSubjects := make(map[string]struct{}, len(entries))

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("cancelled: %v", err))
			return report
		}

		subject := pickSubject(e, spec.Attributes)
		if subject == "" {
			report.Errors = append(report.Errors, fmt.Sprintf("entry %q has no usable subject (attr=%q)", e.DN, spec.Attributes.Subject))
			continue
		}
		seenSubjects[subject] = struct{}{}

		username := firstAttr(e.Attributes, spec.Attributes.Username)
		if username == "" {
			username = subject
		}
		email := firstAttr(e.Attributes, spec.Attributes.Email)
		display := pickDisplayName(e.Attributes, spec.Attributes)
		groups := e.Attributes[spec.Attributes.Groups]

		input := service.ExternalIdentityInput{
			Provider:      src.ID,
			Subject:       subject,
			Email:         email,
			EmailVerified: false, // LDAP has no "verified" concept; never auto-link by email
			DisplayName:   display,
			Username:      username,
		}

		// Was this user previously known? Drives the created/updated counter.
		_, preExisting := s.svc.GetUserByIdentity(ctx, src.ID, subject)
		preExistingHit := preExisting == nil

		userInfo, err := s.svc.FindOrCreateExternalUser(ctx, input)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("upsert %q: %v", subject, err))
			continue
		}

		if preExistingHit {
			report.Updated++
		} else {
			report.Created++
		}

		// Refresh email / display_name when they drifted. UpdateUser is
		// safe to call even when nothing changed.
		if needsUserPatch(userInfo, email, display) {
			disabled := false // re-enable a previously-disabled user that reappeared
			if userInfo.Disabled {
				slog.Info("usersync: re-enabling user that reappeared in directory",
					"source", src.ID, "user_id", userInfo.ID, "username", userInfo.Username)
			}
			emailPtr := normalizeOrNil(email, userInfo.Email)
			dispPtr := stringPtr(display, userInfo.DisplayName)
			req := &service.UpdateUserRequest{Email: emailPtr, DisplayName: dispPtr, Disabled: &disabled}
			if err := s.svc.UpdateUser(ctx, userInfo.ID, req); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("update %q: %v", userInfo.Username, err))
			}
		}

		// Group → permission projection. Even when a user has no groups
		// we still rewrite (with an empty list) so a user removed from
		// every group loses their previously-granted permissions.
		permIDs := projectGroupsToPermissions(groups, spec.GroupPermissions)
		if err := s.svc.SetUserPermissionsBySource(ctx, userInfo.ID, src.ID, permIDs); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("permissions %q: %v", userInfo.Username, err))
		} else {
			report.PermsApplied += len(permIDs)
		}
	}

	// Reconciliation pass: anyone we previously synced but didn't see now.
	// Skipped when OnMissing == "ignore".
	policy := strings.ToLower(strings.TrimSpace(src.OnMissing))
	if policy == "" {
		policy = "disable"
	}
	if policy != "ignore" {
		report.Disabled = s.reconcileMissing(ctx, src.ID, seenSubjects, &report)
	}

	return report
}

// reconcileMissing finds every user_identities row tagged with this source's
// ID whose subject is not in `seen`, then applies the OnMissing policy.
// Returns the count of users disabled.
func (s *Syncer) reconcileMissing(ctx context.Context, sourceID string, seen map[string]struct{}, report *Report) int {
	idents, err := s.svc.ListIdentitiesByProvider(ctx, sourceID)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("reconcile list: %v", err))
		return 0
	}

	disabled := 0
	disabledTrue := true
	for _, id := range idents {
		if _, ok := seen[id.Subject]; ok {
			continue
		}
		// Look up the user; if they're already disabled, skip (no-op).
		user, err := s.svc.GetUserByID(ctx, id.UserID)
		if err != nil || user == nil {
			continue
		}
		if user.Disabled {
			continue
		}
		if err := s.svc.UpdateUser(ctx, user.ID, &service.UpdateUserRequest{Disabled: &disabledTrue}); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("disable %q: %v", user.Username, err))
			continue
		}
		// Also clear this source's permission rows so a re-enabled user
		// doesn't inherit stale group grants.
		_ = s.svc.SetUserPermissionsBySource(ctx, user.ID, sourceID, nil)
		disabled++
	}
	return disabled
}

// validateSource returns nil when the source is configured well enough to
// run. Misconfiguration yields a single explanatory error rather than
// a sea of cryptic LDAP failures.
func validateSource(src service.SyncSource) error {
	if src.ID == "" {
		return fmt.Errorf("source has no id")
	}
	if src.Type != "ldap" {
		return fmt.Errorf("unsupported sync source type %q", src.Type)
	}
	if src.LDAP == nil {
		return fmt.Errorf("source %q: ldap spec missing", src.ID)
	}
	if src.LDAP.Address == "" {
		return fmt.Errorf("source %q: address is required", src.ID)
	}
	if src.LDAP.UserBaseDN == "" {
		return fmt.Errorf("source %q: user_base_dn is required", src.ID)
	}
	if src.LDAP.Attributes.Username == "" {
		return fmt.Errorf("source %q: attributes.username is required", src.ID)
	}
	return nil
}

// requestedAttrs collects the LDAP attribute names to fetch in the search.
// Always includes Username and Subject (when distinct); Groups, Email,
// DisplayName, GivenName, Surname only when configured.
func requestedAttrs(m service.LDAPAttributeMap) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(a string) {
		if a == "" {
			return
		}
		if _, dup := seen[a]; dup {
			return
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	add(m.Username)
	add(m.Subject)
	add(m.Email)
	add(m.DisplayName)
	add(m.GivenName)
	add(m.Surname)
	add(m.Groups)
	return out
}

// pickSubject returns the configured subject attribute's first value, or
// falls back to the username attribute. Empty string means we can't
// identify this entry — the caller skips it with an error.
func pickSubject(e adaEntry, m service.LDAPAttributeMap) string {
	if m.Subject != "" {
		if v := firstAttr(e.Attributes, m.Subject); v != "" {
			return v
		}
	}
	if m.Username != "" {
		if v := firstAttr(e.Attributes, m.Username); v != "" {
			return v
		}
	}
	return ""
}

// pickDisplayName uses DisplayName if set, otherwise falls back to
// "GivenName Surname" (when both configured + present), otherwise empty.
func pickDisplayName(attrs map[string][]string, m service.LDAPAttributeMap) string {
	if v := firstAttr(attrs, m.DisplayName); v != "" {
		return v
	}
	gn := firstAttr(attrs, m.GivenName)
	sn := firstAttr(attrs, m.Surname)
	switch {
	case gn != "" && sn != "":
		return gn + " " + sn
	case gn != "":
		return gn
	case sn != "":
		return sn
	}
	return ""
}

// firstAttr returns the first value of a (possibly missing) LDAP attribute.
func firstAttr(attrs map[string][]string, key string) string {
	if key == "" {
		return ""
	}
	vs, ok := attrs[key]
	if !ok || len(vs) == 0 {
		return ""
	}
	return strings.TrimSpace(vs[0])
}

// projectGroupsToPermissions maps an LDAP user's group values onto the
// union of pika permission IDs declared in the source's GroupPermissions.
// Membership is matched verbatim — if memberOf returns full DNs, the map
// keys must be full DNs.
func projectGroupsToPermissions(groups []string, mapping map[string][]string) []string {
	if len(groups) == 0 || len(mapping) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, g := range groups {
		for _, pid := range mapping[g] {
			if pid == "" {
				continue
			}
			if _, dup := seen[pid]; dup {
				continue
			}
			seen[pid] = struct{}{}
			out = append(out, pid)
		}
	}
	return out
}

// needsUserPatch decides whether UpdateUser is worth calling. It is when
// the user is currently disabled (we want to re-enable) OR when the email
// or display_name from LDAP differs from what's stored.
func needsUserPatch(u *service.UserInfo, email, display string) bool {
	if u == nil {
		return false
	}
	if u.Disabled {
		return true
	}
	if email != "" && !strings.EqualFold(u.Email, email) {
		return true
	}
	if display != "" && u.DisplayName != display {
		return true
	}
	return false
}

func normalizeOrNil(incoming, current string) *string {
	if incoming == "" || strings.EqualFold(incoming, current) {
		return nil
	}
	v := incoming
	return &v
}

func stringPtr(incoming, current string) *string {
	if incoming == "" || incoming == current {
		return nil
	}
	v := incoming
	return &v
}

// adaEntry locally names the ada-shaped entry to avoid leaking the
// adaldap import into this file's public surface (the test fixtures
// construct values of this type directly).
type adaEntry = struct {
	DN         string
	Attributes map[string][]string
}
