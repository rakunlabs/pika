package service

import "context"

type contextKey string

const (
	userContextKey           contextKey = "user"
	userIDContextKey         contextKey = "user_id"
	externalGroupsContextKey contextKey = "external_groups"
)

// WithUser returns a new context with the user name set.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// WithUserID returns a new context with the user ID set.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

// WithUserInfo returns a new context with both username and user ID.
func WithUserInfo(ctx context.Context, username, userID string) context.Context {
	ctx = WithUser(ctx, username)
	ctx = WithUserID(ctx, userID)
	return ctx
}

// UserFromContext returns the authenticated user name from the context,
// or "" if no user is set. Callers that need a non-empty fallback for
// audit fields should use AuditUserFromContext instead.
func UserFromContext(ctx context.Context) string {
	if user, ok := ctx.Value(userContextKey).(string); ok {
		return user
	}
	return ""
}

// AuditUserFromContext returns the user name from the context, falling back
// to the "system" label when no user is present. Use this only for audit
// metadata (CreatedBy, Author, …) where a human-readable placeholder is
// preferable to an empty string. Permission checks must use UserFromContext
// so that a real user named "system" is not confused with an unset caller.
func AuditUserFromContext(ctx context.Context) string {
	if user := UserFromContext(ctx); user != "" {
		return user
	}
	return "system"
}

// UserIDFromContext returns the user ID from the context.
// Returns empty string if not set.
func UserIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(userIDContextKey).(string); ok {
		return id
	}
	return ""
}

// WithExternalGroups returns a new context carrying the list of external
// group names supplied by the forward-auth gateway (typically via an
// X-Groups response header). The list is opaque to the service layer and
// is translated to capability keys by the permission resolver using the
// Settings.ExternalPermissions.Mapping.
func WithExternalGroups(ctx context.Context, groups []string) context.Context {
	return context.WithValue(ctx, externalGroupsContextKey, groups)
}

// ExternalGroupsFromContext returns the external group names previously
// stashed by WithExternalGroups. Returns nil if no groups are set — for
// built-in auth users, for requests without a groups header, or when
// external permissions are disabled.
func ExternalGroupsFromContext(ctx context.Context) []string {
	if g, ok := ctx.Value(externalGroupsContextKey).([]string); ok {
		return g
	}
	return nil
}
