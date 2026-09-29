-- name: GetAllPasswordResetTokens :many
SELECT * FROM password_reset_tokens;

-- name: GetPasswordResetTokenByID :one
SELECT * FROM password_reset_tokens WHERE id = ?;

-- name: GetPasswordResetTokenByToken :one
SELECT * FROM password_reset_tokens WHERE token = ?;

-- name: CreatePasswordResetToken :execresult
INSERT INTO password_reset_tokens (user_id, token, expires_at)
VALUES (?, ?, ?);

-- name: UpdatePasswordResetToken :exec
UPDATE password_reset_tokens
SET user_id = ?, token = ?, expires_at = ?
WHERE id = ?;

-- name: DeletePasswordResetToken :exec
DELETE FROM password_reset_tokens WHERE id = ?;

-- name: RevokePasswordResetTokensForUser :execrows
-- Requesting a new code must invalidate the previous one, so an intercepted
-- older email cannot be used after the user asked for a fresh code.
UPDATE password_reset_tokens
SET used_at = CURRENT_TIMESTAMP
WHERE user_id = ? AND used_at IS NULL;

-- name: ConsumePasswordResetToken :execrows
-- Single-use redemption, enforced atomically.
--
-- The used_at IS NULL and expires_at > now() predicates live in the UPDATE, not
-- in a prior SELECT. That is what makes replay impossible under concurrency: two
-- simultaneous requests both try to stamp the same row, the database serialises
-- them, and the loser matches zero rows instead of also succeeding. Checking
-- expiry in Go before this call would leave the window open between the read and
-- the write.
UPDATE password_reset_tokens
SET used_at = CURRENT_TIMESTAMP
WHERE token = ?
  AND used_at IS NULL
  AND expires_at > CURRENT_TIMESTAMP;