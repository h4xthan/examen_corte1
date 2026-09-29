package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"dvbs/internal/model"
	"dvbs/internal/token"
)

type ctxKey string

const (
	ctxUserID ctxKey = "user_id"
	ctxEmail  ctxKey = "email"
	ctxRole   ctxKey = "role"
	// ctxViaCookie records that the session came from a browser cookie rather
	// than an Authorization header. CSRF only applies to the former: a bearer
	// token is not attached by the browser automatically, so a cross-site
	// request cannot make the browser send one.
	ctxViaCookie ctxKey = "via_cookie"
)

// SessionResolver reads the authoritative account state for a request.
//
// This is the fix for trusting the role claim. A token is a bearer credential
// that the client holds; anything the server decides based on data inside it is
// only as trustworthy as the client's copy. Reading role and token_version from
// the database on every request costs one indexed primary-key lookup and makes
// a demotion or a password change take effect at once.
type SessionResolver interface {
	ResolveSession(ctx context.Context, userID int64) (model.SessionInfo, error)
	Ping(ctx context.Context) error
}

// lookupSession turns a raw credential into the account state the request runs
// under. It is shared by Auth (which refuses a caller it cannot attribute) and
// OptionalAuth (which treats a missing or broken credential as "no session").
var (
	errNoCredential   = errors.New("no credential")
	errBadCredential  = errors.New("bad credential")
	errAccountOffline = errors.New("account disabled")
)

func lookupSession(ctx context.Context, verifier *token.Manager, sessions SessionResolver, raw string) (model.SessionInfo, error) {
	if raw == "" {
		return model.SessionInfo{}, errNoCredential
	}

	claims, err := verifier.Verify(raw)
	if err != nil {
		return model.SessionInfo{}, errBadCredential
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		return model.SessionInfo{}, errBadCredential
	}

	session, err := sessions.ResolveSession(ctx, userID)
	if err != nil {
		// The resolver reports a deleted account and a database outage
		// the same way to the client, but the server keeps them apart in
		// the log: one is routine, the other is an incident.
		slog.Warn("session lookup failed", "user_id", userID, "error", err)
		return model.SessionInfo{}, err
	}

	// The version check is what a password reset relies on to revoke
	// outstanding sessions. Without it, a stolen token stays useful until
	// it expires.
	if session.TokenVersion != claims.TokenVersion {
		slog.Info("session revoked by token version",
			"user_id", session.UserID,
			"token_version", claims.TokenVersion,
			"current_version", session.TokenVersion)
		return model.SessionInfo{}, errBadCredential
	}

	// An account that was taken off ("dado de baja") must not keep using
	// a session that was valid a moment before. This is the same read
	// that fetched the role, so the revocation takes effect on the very
	// next request and there is no window in which the disabled account
	// can keep buying.
	if !session.IsActive {
		slog.Info("session for disabled account rejected", "user_id", session.UserID)
		return model.SessionInfo{}, errAccountOffline
	}

	return session, nil
}

func sessionContext(ctx context.Context, session model.SessionInfo, viaCookie bool) context.Context {
	ctx = context.WithValue(ctx, ctxUserID, session.UserID)
	ctx = context.WithValue(ctx, ctxEmail, session.Email)
	ctx = context.WithValue(ctx, ctxRole, session.Role)
	ctx = context.WithValue(ctx, ctxViaCookie, viaCookie)
	return ctx
}

// Auth authenticates a request from the session cookie, falling back to a
// Authorization header for non-browser clients such as curl and the test suite.
//
// The cookie is the primary path because it is httpOnly: script on the page
// cannot read it, so an XSS cannot walk away with a long-lived credential. The
// header remains supported deliberately, since an API that only speaks cookies
// is awkward to script against, and it carries no CSRF risk.
func Auth(verifier *token.Manager, sessions SessionResolver, cookieName string, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, viaCookie := credentialFrom(r, cookieName)
			session, err := lookupSession(r.Context(), verifier, sessions, raw)
			switch {
			case errors.Is(err, errNoCredential):
				writeAuthError(w, "authentication required")
				return
			case errors.Is(err, errBadCredential):
				writeAuthError(w, "invalid session")
				return
			case errors.Is(err, errAccountOffline):
				writeAuthError(w, "account disabled")
				return
			case err != nil:
				writeAuthError(w, "invalid session")
				return
			}
			next.ServeHTTP(w, r.WithContext(sessionContext(r.Context(), session, viaCookie)))
		})
	}
}

// OptionalAuth resolves the session when the request carries one, and lets
// anonymous traffic through untouched. It sits in front of public catalogue
// routes so the operating history can attribute a consultation to a panel role
// without turning the shop into a login wall: a customer browsing a book stays
// anonymous, an auditor browsing the same book shows up in the audit trail.
func OptionalAuth(verifier *token.Manager, sessions SessionResolver, cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, viaCookie := credentialFrom(r, cookieName)
			session, err := lookupSession(r.Context(), verifier, sessions, raw)
			if err != nil {
				// A public route answers the same to everyone. A session that
				// broke (expired, revoked, disabled account, gone user) simply
				// means the caller browses as a stranger, which is their right:
				// the page is public.
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(sessionContext(r.Context(), session, viaCookie)))
		})
	}
}

// credentialFrom prefers the cookie and falls back to the header. The cookie
// wins so that a request carrying both cannot be downgraded to a path that skips
// the CSRF check.
func credentialFrom(r *http.Request, cookieName string) (string, bool) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	authz := r.Header.Get("Authorization")
	if after, found := strings.CutPrefix(authz, "Bearer "); found && after != "" {
		return after, false
	}
	return "", false
}

func writeAuthError(w http.ResponseWriter, msg string) {
	slog.Warn("auth rejected", "reason", msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}

func UserIDFrom(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxUserID).(int64)
	return id, ok && id != 0
}

func EmailFrom(ctx context.Context) string {
	email, _ := ctx.Value(ctxEmail).(string)
	return email
}

// RoleFrom returns the role as recorded in the database at the time of the
// request, not as asserted by the token.
func RoleFrom(ctx context.Context) string {
	role, _ := ctx.Value(ctxRole).(string)
	return role
}

// ViaCookieFrom reports whether the session arrived in a cookie. The CSRF
// middleware uses it to decide if this request is forgeable.
func ViaCookieFrom(ctx context.Context) bool {
	via, _ := ctx.Value(ctxViaCookie).(bool)
	return via
}
