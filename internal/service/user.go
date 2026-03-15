package service

import "context"

type contextKey string

const userContextKey contextKey = "user"

// WithUser returns a new context with the user name set.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext returns the user name from the context.
// Returns "system" if not set.
func UserFromContext(ctx context.Context) string {
	if user, ok := ctx.Value(userContextKey).(string); ok && user != "" {
		return user
	}
	return "system"
}
