package middleware

import (
	"net/http"
	"time"
)

const (
	// SessionCookieName holds the httpOnly session token. JavaScript must not be
	// able to read it: an XSS that could exfiltrate it would get a credential
	// that outlives the token's TTL and works from any machine.
	SessionCookieName = "dvbs_session"

	// CSRFCookieName holds the double-submit token. This one is deliberately
	// readable by JavaScript, because the whole point is that the frontend
	// copies it into a header. It is not a secret on its own: without the
	// session cookie it grants nothing.
	CSRFCookieName = "dvbs_csrf"
)

// SetSessionCookie writes the session cookie after a successful login.
//
// HttpOnly keeps the token away from scripts. SameSite=Lax is what actually
// stops cross-site form submissions in modern browsers, and it is the primary
// defence here rather than a nicety. MaxAge matches the token's own expiry so
// the browser does not keep sending a token the server will already reject.
func SetSessionCookie(w http.ResponseWriter, value string, maxAge time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie expires the session cookie.
//
// The attributes must match the ones used when setting it, or the browser keeps
// the original: a cookie is identified by name, domain and path, so a mismatch
// adds a second cookie instead of replacing the first.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// SetCSRFCookie writes the double-submit token. Not HttpOnly, by design.
func SetCSRFCookie(w http.ResponseWriter, value string, secure bool, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
