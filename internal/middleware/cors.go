package middleware

import (
	"net/http"
	"strings"
)

// allowMethods and allowHeaders are written out rather than sent as "*".
//
// Both fields reject the wildcard once credentials are allowed, so a "*" here
// would not even work: the browser would refuse the response and the developer
// would be left debugging a header the spec says is invalid. Naming the methods
// we actually serve is also what a reader needs in order to know the API's
// surface.
var (
	allowMethods = strings.Join([]string{
		http.MethodGet, http.MethodHead, http.MethodPost,
		http.MethodPut, http.MethodDelete, http.MethodOptions,
	}, ", ")
	allowHeaders = strings.Join([]string{
		"Content-Type", "Authorization", CSRFHeader,
	}, ", ")
)

// CORS allows cross-origin requests from an explicit list of origins.
//
// The previous version answered Access-Control-Allow-Origin: * to everything,
// which is the textbook #15. Combined with cookie sessions it is not even
// usable: a browser discards a wildcard on a credentialed request, so the
// obvious "fix" of switching a header for a cookie would have silently broken
// the frontend and left the operator to widen it back to *.
//
// The rule here is a closed list. An origin that is not on it gets no CORS
// headers at all, and its preflights are refused outright. Requests that are not
// preflights are still served without the headers: a cross-origin form post or an
// <img> can reach us either way, and pretending otherwise by rejecting them would
// only make the endpoint's behaviour harder to reason about. The security
// boundary is the session cookie and the CSRF token, not this header. CORS is
// here to stop a hostile page from *reading* responses, which is what an allowlist
// actually does.
func CORS(allowed []string) func(http.Handler) http.Handler {
	// A set, because the lookup happens on every request of every route.
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		if o != "" {
			set[o] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Vary goes on every response, allowed or not. Without it a shared
			// cache or a CDN can hand a response carrying one origin's
			// Allow-Origin to a request from a different one, which reopens the
			// hole the allowlist just closed.
			w.Header().Add("Vary", "Origin")

			// No Origin at all means curl, a server-side render, or a same-origin
			// request. There is nothing to negotiate.
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if _, ok := set[origin]; !ok {
				// Refusing the preflight is the useful part: a preflight only
				// exists to ask whether the real request may follow, so saying
				// no here is the CORS layer's version of a 403.
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", "Content-Type")

			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", allowMethods)
				h.Set("Access-Control-Allow-Headers", allowHeaders)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
