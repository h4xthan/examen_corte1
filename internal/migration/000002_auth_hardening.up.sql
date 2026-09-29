-- Phase 1 hardening: session revocation and single-use password reset tokens.
--
-- Everything here is additive or a widening of an existing column, so this
-- migration is safe to apply to a database that already has rows.

-- token_version lets a password change invalidate every session issued before
-- it. The value travels in the session token as the "ver" claim, so a
-- forgotten or hijacked session dies at the next request instead of at expiry.
ALTER TABLE users ADD COLUMN IF NOT EXISTS token_version INTEGER NOT NULL DEFAULT 0;

-- used_at makes a reset token single-use. Revoking a token by deleting the row
-- works too, but leaving the row and stamping it keeps the audit trail and
-- makes replay attempts visible instead of indistinguishable from a typo.
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS used_at TIMESTAMPTZ;

-- The token column was TEXT with no index: every reset attempt was a full table
-- scan, and there was no way to enforce uniqueness.
ALTER TABLE password_reset_tokens ALTER COLUMN token TYPE VARCHAR(128);
CREATE UNIQUE INDEX IF NOT EXISTS idx_password_reset_tokens_token
    ON password_reset_tokens (token);

-- At most one live reset token per user. Enforcing it in the database means a
-- concurrent double request cannot leave two usable tokens behind.
CREATE UNIQUE INDEX IF NOT EXISTS idx_password_reset_tokens_user_live
    ON password_reset_tokens (user_id)
    WHERE used_at IS NULL;

-- Reset lookups by user are needed to revoke prior tokens.
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_user_id
    ON password_reset_tokens (user_id);

-- The role is read from the database on every admin request, and the email is
-- looked up on every login. email already has a UNIQUE constraint, which
-- Postgres backs with an index.
