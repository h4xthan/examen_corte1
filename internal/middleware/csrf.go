package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// CSRFHeader is the header the client must echo the CSRF cookie back in.
const CSRFHeader = "X-CSRF-Token"

// CSRF implements the double-submit-cookie defence.
//
// A session cookie is attached by the browser to any request to our origin,
// including one triggered by another site. That is what makes cookie
// authentication convenient and also what makes it forgeable: without a second
// factor, any page on the internet can POST to /checkout using the visitor's
// session. The second factor is a value the browser will not attach on its own,
// so the request must also carry it in a header that a cross-origin form or
// image cannot set.
//
// Requests authenticated by an Authorization header are exempt. A bearer token
// is not stored in a cookie, so the browser never attaches it by itself, and
// there is nothing for a hostile page to ride on. Exempting them keeps curl and
// the test suite usable without teaching every non-browser client about cookies.
func CSRF(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			unsafe := r.Method != http.MethodGet &&
				r.Method != http.MethodHead &&
				r.Method != http.MethodOptions

			// Note this covers the sessionless case too: a cross-site form that
			// logs a victim into an attacker's account is a login-CSRF, and the
			// only thing stopping it is requiring the header.
			if unsafe && !bearerPresent(r) && !validCSRF(r) {
				slog.Warn("csrf rejected",
					"method", r.Method,
					"path", r.URL.Path,
					"has_session_cookie", sessionCookiePresent(r),
				)
				writeJSONError(w, http.StatusForbidden, "invalid csrf token")
				return
			}

			// Keep a token available so the client can always obtain one.
			if _, err := ensureCSRFCookie(w, r, secure); err != nil {
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CSRFToken is the bootstrap endpoint for the double-submit scheme.
//
// A client that has never talked to us holds no CSRF cookie, so it cannot
// satisfy the check on a state-changing request. Requiring it to obtain the
// token first is what makes the rule strict without making it impossible to
// meet: a cross-origin form can trigger a GET, but it cannot read the response
// body, so it still cannot forge the header on a later POST.
//
// Unlike the middleware's lazy mint (which keeps an existing token for the
// life of the cookie), this endpoint always hands out a fresh value and
// re-sets the cookie. That gives a desynced client a way out: when the browser
// holds a stale or duplicated csrf cookie and the request came back 403, the
// client can call here, tunnel out from under the old values in one
// Set-Cookie, and retry. The client only calls it to recover, never on every
// request, so a legitimately held token is not churned.
func CSRFToken(secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		value, err := NewCSRFToken()
		if err != nil {
			slog.Error("could not generate csrf token", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		SetCSRFCookie(w, value, secure, 0)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"csrf_token":` + strconv.Quote(value) + `}`))
	}
}

func sessionCookiePresent(r *http.Request) bool {
	c, err := r.Cookie(SessionCookieName)
	return err == nil && c.Value != ""
}

func bearerPresent(r *http.Request) bool {
	authz := r.Header.Get("Authorization")
	_, found := strings.CutPrefix(authz, "Bearer ")
	return found && strings.TrimSpace(authz[len("Bearer "):]) != ""
}

// validCSRF compares the cookie and the header in constant time.
//
// The comparison must not short-circuit: a byte-by-byte early exit leaks how
// many leading characters of a guess were correct, which turns brute force from
// an all-or-nothing search into a cheap prefix search.
func validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get(CSRFHeader)
	if header == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) == 1
}

// ensureCSRFCookie issues a token when the request has none, so a client that
// just landed on the site can immediately perform a state-changing call.
//
// It returns the token the client should send, and the error only in the case
// where the response has already been written.
func ensureCSRFCookie(w http.ResponseWriter, r *http.Request, secure bool) (string, error) {
	if existing, err := r.Cookie(CSRFCookieName); err == nil && existing.Value != "" {
		return existing.Value, nil
	}

	value, err := NewCSRFToken()
	if err != nil {
		// Without a token the client cannot make any state-changing request, so
		// failing the request is clearer than letting it through and breaking
		// later in a confusing way.
		slog.Error("could not generate csrf token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return "", err
	}
	SetCSRFCookie(w, value, secure, 0)
	return value, nil
}

// NewCSRFToken returns a fresh 256-bit token, URL-safe so it needs no escaping
// in a cookie or a header.
func NewCSRFToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
