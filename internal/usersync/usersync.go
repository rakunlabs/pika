// Package usersync runs periodic and on-demand reconciliation of pika's
// local users table against an external directory (currently LDAP).
//
// Two paths exist for getting external users into pika:
//
//  1. JIT (just-in-time) — when a user logs in via the LDAP strategy and
//     the strategy opts into auto_create_user, the session store calls this
//     package to sync exactly that user plus its group-derived permissions.
//
//  2. Batch — this package. Walks the directory, calls
//     FindOrCreateExternalUser for every match (idempotent), then reconciles
//     missing users + per-user permissions per the SyncSource policy.
//
// Both paths use the same group-to-permission projection, so LDAP users get
// consistent source-owned grants whether they arrive via schedule, manual sync,
// or first login.
package usersync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
	adaldap "github.com/rakunlabs/ada/middleware/auth/strategy/ldap"

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

	groupIndex, err := searchGroupIndex(conn, spec)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("group search: %v", err))
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

		groups := groupsForEntry(e, spec.Attributes, groupIndex)
		_, created, permsApplied, err := s.syncEntry(ctx, src.ID, spec, e, groups)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("upsert %q: %v", subject, err))
			continue
		}

		if created {
			report.Created++
		} else {
			report.Updated++
		}
		report.PermsApplied += permsApplied
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

// RunOne syncs a single LDAP user during login. It uses the source's LDAP
// connection/search/group mapping so first-login provisioning gets the same
// user row and permission grants as a full batch sync.
func (s *Syncer) RunOne(ctx context.Context, src service.SyncSource, in service.ExternalIdentityInput) (*service.UserInfo, error) {
	if err := validateSource(src); err != nil {
		return nil, err
	}
	spec := src.LDAP

	connector := ldapclient.New(ldapclient.Config{
		Address:      spec.Address,
		TLS:          spec.TLS,
		InsecureSkip: spec.InsecureSkip,
	})

	conn, err := connector.NewConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(spec.BindDN, spec.BindPassword); err != nil {
		return nil, fmt.Errorf("bind: %w", err)
	}

	filter, err := loginUserFilter(spec.UserFilter, spec.Attributes, in)
	if err != nil {
		return nil, err
	}
	entries, err := conn.SearchAll(spec.UserBaseDN, filter, requestedAttrs(spec.Attributes), 5)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	entry := bestLoginEntry(entries, spec.Attributes, in)
	if entry == nil {
		return nil, service.ErrNotFound
	}

	groupIndex, err := searchGroupIndex(conn, spec)
	if err != nil {
		return nil, fmt.Errorf("group search: %w", err)
	}
	groups := groupsForEntry(*entry, spec.Attributes, groupIndex)
	info, _, _, err := s.syncEntry(ctx, src.ID, spec, *entry, groups)
	return info, err
}

func (s *Syncer) syncEntry(ctx context.Context, sourceID string, spec *service.LDAPSyncSpec, e adaEntry, groups []string) (*service.UserInfo, bool, int, error) {
	subject := pickSubject(e, spec.Attributes)
	if subject == "" {
		return nil, false, 0, fmt.Errorf("entry %q has no usable subject (attr=%q)", e.DN, spec.Attributes.Subject)
	}
	username := firstAttr(e.Attributes, spec.Attributes.Username)
	if username == "" {
		username = subject
	}
	email := firstAttr(e.Attributes, spec.Attributes.Email)
	display := pickDisplayName(e.Attributes, spec.Attributes)

	input := service.ExternalIdentityInput{
		Provider:      sourceID,
		Subject:       subject,
		Email:         email,
		EmailVerified: false, // LDAP has no "verified" concept; never auto-link by email
		DisplayName:   display,
		Username:      username,
	}

	userInfo, err := s.svc.GetUserByIdentity(ctx, sourceID, subject)
	created := false
	if err != nil {
		if !errors.Is(err, service.ErrNotFound) {
			return nil, false, 0, err
		}
		userInfo, err = s.svc.FindOrCreateExternalUser(ctx, input)
		if err != nil {
			return nil, false, 0, err
		}
		created = true
	} else if err := s.svc.RefreshExternalIdentity(ctx, userInfo.ID, input); err != nil {
		return nil, false, 0, err
	}

	// Refresh email / display_name when they drift. Also re-enable a user
	// disabled by a previous missing-user reconciliation once LDAP returns it.
	if needsUserPatch(userInfo, email, display) {
		disabled := false
		if userInfo.Disabled {
			slog.Info("usersync: re-enabling user that reappeared in directory",
				"source", sourceID, "user_id", userInfo.ID, "username", userInfo.Username)
		}
		emailPtr := normalizeOrNil(email, userInfo.Email)
		dispPtr := stringPtr(display, userInfo.DisplayName)
		req := &service.UpdateUserRequest{Email: emailPtr, DisplayName: dispPtr, Disabled: &disabled}
		if err := s.svc.UpdateUser(ctx, userInfo.ID, req); err != nil {
			return nil, created, 0, fmt.Errorf("update %q: %w", userInfo.Username, err)
		}
	}

	// Group → permission projection. Even when a user has no groups we still
	// rewrite with an empty list so removed group memberships remove stale grants.
	permIDs := projectGroupsToPermissions(groups, spec.GroupPermissions)
	if err := s.svc.SetUserPermissionsBySource(ctx, userInfo.ID, sourceID, permIDs); err != nil {
		return nil, created, 0, fmt.Errorf("permissions %q: %w", userInfo.Username, err)
	}
	return userInfo, created, len(permIDs), nil
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
	for i, gs := range src.LDAP.GroupSearches {
		if strings.TrimSpace(gs.BaseDN) == "" {
			return fmt.Errorf("source %q: group_searches[%d].base_dn is required", src.ID, i)
		}
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
	vs := attrValues(attrs, key)
	if len(vs) == 0 {
		return ""
	}
	return strings.TrimSpace(vs[0])
}

func attrValues(attrs map[string][]string, key string) []string {
	if key == "" {
		return nil
	}
	if vs, ok := attrs[key]; ok {
		return vs
	}
	for k, vs := range attrs {
		if strings.EqualFold(k, key) {
			return vs
		}
	}
	return nil
}

type groupIndex map[string][]string

func searchGroupIndex(conn *ldapclient.Conn, spec *service.LDAPSyncSpec) (groupIndex, error) {
	idx := groupIndex{}
	if spec == nil || len(spec.GroupSearches) == 0 {
		return idx, nil
	}
	for _, search := range spec.GroupSearches {
		baseDN := strings.TrimSpace(search.BaseDN)
		if baseDN == "" {
			continue
		}
		filter := strings.TrimSpace(search.Filter)
		if filter == "" {
			filter = "(objectClass=*)"
		}
		nameAttr, memberAttr, memberUIDAttr := groupSearchAttrs(search)
		entries, err := conn.SearchAll(baseDN, filter, requestedGroupAttrs(search, nameAttr, memberAttr), spec.PageSize)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			groupName := firstAttr(e.Attributes, nameAttr)
			if groupName == "" {
				continue
			}
			for _, member := range attrValues(e.Attributes, memberAttr) {
				memberKey := groupMemberKey(member, memberUIDAttr)
				if memberKey == "" {
					continue
				}
				idx.add(memberKey, groupName)
			}
		}
	}
	return idx, nil
}

func groupSearchAttrs(search service.LDAPGroupSearchSpec) (nameAttr, memberAttr, memberUIDAttr string) {
	nameAttr = strings.TrimSpace(search.NameAttribute)
	if nameAttr == "" {
		nameAttr = "cn"
	}
	memberAttr = strings.TrimSpace(search.MemberAttribute)
	if memberAttr == "" {
		memberAttr = "uniqueMember"
	}
	memberUIDAttr = strings.TrimSpace(search.MemberUIDAttribute)
	if memberUIDAttr == "" {
		memberUIDAttr = "uid"
	}
	return nameAttr, memberAttr, memberUIDAttr
}

func requestedGroupAttrs(search service.LDAPGroupSearchSpec, nameAttr, memberAttr string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(a string) {
		a = strings.TrimSpace(a)
		if a == "" {
			return
		}
		key := strings.ToLower(a)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, a)
	}
	add(nameAttr)
	add(memberAttr)
	for _, a := range search.Attributes {
		add(a)
	}
	return out
}

func (g groupIndex) add(memberKey, groupName string) {
	memberKey = normalizeGroupMemberKey(memberKey)
	groupName = strings.TrimSpace(groupName)
	if memberKey == "" || groupName == "" {
		return
	}
	for _, existing := range g[memberKey] {
		if existing == groupName {
			return
		}
	}
	g[memberKey] = append(g[memberKey], groupName)
}

func (g groupIndex) groupsFor(keys []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, key := range keys {
		for _, group := range g[normalizeGroupMemberKey(key)] {
			if _, dup := seen[group]; dup {
				continue
			}
			seen[group] = struct{}{}
			out = append(out, group)
		}
	}
	return out
}

func normalizeGroupMemberKey(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func groupMemberKey(member, uidAttr string) string {
	member = strings.TrimSpace(member)
	uidAttr = strings.TrimSpace(uidAttr)
	if member == "" {
		return ""
	}
	if strings.EqualFold(uidAttr, "dn") {
		return member
	}
	if uidAttr == "" {
		uidAttr = "uid"
	}
	if dn, err := goldap.ParseDN(member); err == nil {
		for _, rdn := range dn.RDNs {
			for _, attr := range rdn.Attributes {
				if strings.EqualFold(attr.Type, uidAttr) {
					return strings.TrimSpace(attr.Value)
				}
			}
		}
	}
	for _, part := range strings.Split(member, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), uidAttr) {
			return strings.TrimSpace(v)
		}
	}
	if !strings.Contains(member, "=") && !strings.Contains(member, ",") {
		return member
	}
	return ""
}

func groupsForEntry(e adaEntry, attrs service.LDAPAttributeMap, idx groupIndex) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(groups []string) {
		for _, group := range groups {
			group = strings.TrimSpace(group)
			if group == "" {
				continue
			}
			if _, dup := seen[group]; dup {
				continue
			}
			seen[group] = struct{}{}
			out = append(out, group)
		}
	}
	add(attrValues(e.Attributes, attrs.Groups))
	add(idx.groupsFor(memberKeysForEntry(e, attrs)))
	return out
}

func memberKeysForEntry(e adaEntry, attrs service.LDAPAttributeMap) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		key := normalizeGroupMemberKey(v)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	add(e.DN)
	add(pickSubject(e, attrs))
	add(firstAttr(e.Attributes, attrs.Username))
	add(firstAttr(e.Attributes, attrs.Email))
	add(firstAttr(e.Attributes, attrs.DisplayName))
	add(firstAttr(e.Attributes, attrs.GivenName))
	add(firstAttr(e.Attributes, attrs.Surname))
	return out
}

func loginUserFilter(baseFilter string, attrs service.LDAPAttributeMap, in service.ExternalIdentityInput) (string, error) {
	baseFilter = strings.TrimSpace(baseFilter)
	if strings.Contains(baseFilter, "%s") {
		value := firstNonEmpty(in.Username, in.Subject, in.Email)
		if value == "" {
			return "", fmt.Errorf("login user has no searchable identifier: %w", service.ErrBadRequest)
		}
		escaped := escapeLDAPFilter(value)
		args := make([]any, strings.Count(baseFilter, "%s"))
		for i := range args {
			args[i] = escaped
		}
		return fmt.Sprintf(baseFilter, args...), nil
	}

	var filters []string
	add := func(attr, value string) {
		attr = strings.TrimSpace(attr)
		value = strings.TrimSpace(value)
		if attr == "" || value == "" {
			return
		}
		f := fmt.Sprintf("(%s=%s)", attr, escapeLDAPFilter(value))
		for _, existing := range filters {
			if existing == f {
				return
			}
		}
		filters = append(filters, f)
	}
	add(attrs.Subject, in.Subject)
	add(attrs.Username, in.Subject)
	add(attrs.Username, in.Username)
	add(attrs.Email, in.Email)
	if len(filters) == 0 {
		return "", fmt.Errorf("login user has no searchable identifier: %w", service.ErrBadRequest)
	}
	match := filters[0]
	if len(filters) > 1 {
		match = "(|" + strings.Join(filters, "") + ")"
	}
	if baseFilter == "" {
		return match, nil
	}
	return "(&" + baseFilter + match + ")", nil
}

func bestLoginEntry(entries []adaEntry, attrs service.LDAPAttributeMap, in service.ExternalIdentityInput) *adaEntry {
	for i := range entries {
		if in.Subject != "" && pickSubject(entries[i], attrs) == in.Subject {
			return &entries[i]
		}
	}
	for i := range entries {
		if in.Username != "" && firstAttr(entries[i].Attributes, attrs.Username) == in.Username {
			return &entries[i]
		}
	}
	for i := range entries {
		if in.Email != "" && strings.EqualFold(firstAttr(entries[i].Attributes, attrs.Email), in.Email) {
			return &entries[i]
		}
	}
	if len(entries) == 1 {
		return &entries[0]
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func escapeLDAPFilter(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		switch c {
		case '\\', '(', ')', '*', '\x00':
			fmt.Fprintf(&b, "\\%02x", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
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

// adaEntry locally names the ada-shaped LDAP entry used by ldapclient.
type adaEntry = adaldap.Entry
