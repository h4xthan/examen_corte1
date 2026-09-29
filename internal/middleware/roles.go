package middleware

import (
	"net/http"
)

// RequireRoles gates a route to one of the named roles.
//
// It is AdminOnly generalised. The check itself is a single map lookup because
// the hard part is upstream: Auth puts the role in the context from the row it
// just read in the database, not from the token, so a token claiming a role is
// worth nothing on its own and a demotion takes effect on the next request.
func RequireRoles(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !allowed[RoleFrom(r.Context())] {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"privileges required"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
