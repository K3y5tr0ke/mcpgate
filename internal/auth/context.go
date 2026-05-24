package auth

import (
	"context"

	"github.com/google/uuid"
)

type contextKey struct{}

// UserIdentity holds the authenticated user's identity as stored in the request context.
type UserIdentity struct {
	UserID    uuid.UUID
	Email     string
	Roles     []string
	SessionID uuid.UUID
}

// WithIdentity returns a new context carrying id.
func WithIdentity(ctx context.Context, id UserIdentity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// IdentityFromContext retrieves the UserIdentity stored by WithIdentity.
// The second return value is false if no identity has been set.
func IdentityFromContext(ctx context.Context) (UserIdentity, bool) {
	id, ok := ctx.Value(contextKey{}).(UserIdentity)
	return id, ok
}

// MustIdentity returns the UserIdentity from ctx or panics.
// Only call this inside handlers that are guaranteed to run behind Authenticate().
func MustIdentity(ctx context.Context) UserIdentity {
	id, ok := IdentityFromContext(ctx)
	if !ok {
		panic("auth: identity not present in context — handler missing Authenticate middleware")
	}
	return id
}
