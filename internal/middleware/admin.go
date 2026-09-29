package middleware

import (
	"net/http"

	"dvbs/internal/model"
)

// AdminOnly gates the administrative routes.
//
// The check itself is a single comparison because the hard part is upstream: Auth
// puts role in the context from the row it just read in the database, not from
// the token. A token claiming role=admin is worth nothing on its own, and a
// demotion takes effect on the next request instead of whenever the token
// happens to expire.
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RoleFrom(r.Context()) != model.RoleAdmin {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"admin privileges required"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
