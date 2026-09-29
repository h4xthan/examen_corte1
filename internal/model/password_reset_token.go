package model

import "time"

type PasswordResetToken struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"user_id"`
	Token  string `json:"-"`
	// UsedAt is nil while the token is still usable. A non-nil value means it
	// has been spent, either by a successful reset or because a newer one
	// replaced it.
	UsedAt    *time.Time `json:"used_at,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Usable reports whether the token may still be redeemed.
//
// The authoritative check is the atomic ConsumePasswordResetToken query, which
// re-applies the same conditions inside the UPDATE. This method exists for
// logging and for the tests, not as a substitute.
func (t *PasswordResetToken) Usable(now time.Time) bool {
	return t != nil && t.UsedAt == nil && t.ExpiresAt.After(now)
}
