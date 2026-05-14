// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
)

type AuthContext struct {
	Subject string
	Claims  map[string]any
}

type Authenticator interface {
	Authenticate(r *http.Request) (*AuthContext, error)
}

type authCtxKey struct{}

// GetAuth returns the AuthContext set by auth middleware, or nil if no auth
// is configured or the request was anonymous.
func GetAuth(ctx context.Context) *AuthContext {
	v, _ := ctx.Value(authCtxKey{}).(*AuthContext)
	return v
}

func authMiddleware(a Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ac, err := a.Authenticate(r)
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			ctx := context.WithValue(r.Context(), authCtxKey{}, ac)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
