package model

import "time"

// Roles.
//
// Registration always assigns RoleCustomer. The three operating roles are the
// admin, who can do everything; the capturista, who can read and write the
// catalogue and see its operations; and the auditor, who can only read the
// catalogue and its operations. A capturista or an auditor is an operating
// role, never a second kind of administrator: neither touches orders, coupons,
// reviews, users or backups. See the matrix in AGENTS.md §2.
const (
	RoleAdmin      = "admin"
	RoleCustomer   = "customer"
	RoleCapturista = "capturista"
	RoleAuditor    = "auditor"
)

// AllRoles is the closed set used by role validation and by the panel's
// permission dropdown. Adding a role means touching this list, the CHECK-like
// validation in the service, and the middleware gating routes to it.
var AllRoles = []string{RoleAdmin, RoleCapturista, RoleAuditor, RoleCustomer}

// IsRole reports whether name is one of the closed set.
func IsRole(name string) bool {
	for _, r := range AllRoles {
		if r == name {
			return true
		}
	}
	return false
}

// SessionInfo is the trusted view of an account for an authenticated request.
//
// It lives in model rather than in either middleware or store because both need
// it: the store produces it from a query, and the session middleware consumes it
// to build the request context. Putting it here keeps the dependency pointing
// one way.
//
// Role and TokenVersion travel with every request precisely so that neither has
// to be believed from the token. A demotion and a password change then take
// effect on the next request instead of whenever a bearer credential happens to
// expire.
type SessionInfo struct {
	UserID       int64
	Email        string
	Role         string
	TokenVersion int
	// IsActive is the account's enabled bit, read from the same row as the
	// role. A user that was taken off ("dado de baja") must stop being able to
	// use a session that was valid a moment before, and the only way to make a
	// revocation take effect on the very next request is to check it here, in
	// the same read that the session middleware already does.
	IsActive bool
}

type User struct {
	ID           int64     `json:"id,string"`
	FirstName    string    `json:"first_name"`
	LastName     string    `json:"last_name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	BalanceCents int64     `json:"balance_cents"`
	CreatedAt    time.Time `json:"created_at"`

	// IsActive is whether the account can log in and use a session. The admin
	// panel's "baja" flips it to false and bumps token_version, so deactivating
	// somebody kills the sessions they already had instead of just the next
	// login.
	IsActive bool `json:"is_active"`

	// TokenVersion is deliberately not serialised. It is internal session
	// bookkeeping, and exposing it would tell an attacker how many times the
	// password behind a session has been changed.
	TokenVersion int `json:"-"`
}
