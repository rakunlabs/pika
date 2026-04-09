package service

import "context"

type contextKey string

const (
	userContextKey   contextKey = "user"
	userIDContextKey contextKey = "user_id"
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

// UserFromContext returns the user name from the context.
// Returns "system" if not set.
func UserFromContext(ctx context.Context) string {
	if user, ok := ctx.Value(userContextKey).(string); ok && user != "" {
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
