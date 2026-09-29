-- name: GetAllUsers :many
SELECT * FROM users;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserBalanceForUpdate :one
-- Locking the account row is what makes the conditional debit below actually
-- conditional on TiDB. In pessimistic mode an UPDATE that matched the read
-- snapshot still rewrites the row once the lock is granted, so two concurrent
-- checkouts could each see a balance of 1000 cents and both debit, driving it
-- to -1000 (TestBalanceCannotGoNegative caught it against the real cluster).
-- Acquiring the lock as the first statement of the transaction makes the later
-- UPDATE read current data and the loser fail its balance_cents >= ? check.
SELECT balance_cents
FROM users
WHERE id = ?
FOR UPDATE;

-- name: CreateUser :execresult
INSERT INTO users (first_name, last_name, email, password_hash, role)
VALUES (?, ?, ?, ?, ?);

-- name: UpdateUser :exec
UPDATE users
SET first_name = ?, last_name = ?, email = ?, password_hash = ?, role = ?, balance_cents = ?
WHERE id = ?;

-- name: DebitUserBalance :exec
UPDATE users SET balance_cents = balance_cents - ? WHERE id = ?;

-- name: DeleteUser :execrows
-- As :exec, deleting an account that does not exist was indistinguishable from
-- deleting one that did, and the panel reported the success either way. Zero
-- rows becomes sql.ErrNoRows at the store, which is a 404.
DELETE FROM users WHERE id = ?;

-- name: UpdatePasswordHash :exec
-- Only the password column. Used by the reset flow so a password change can
-- never be combined with a mass-assigned email, role or balance.
UPDATE users
SET password_hash = ?
WHERE id = ?;

-- name: BumpTokenVersion :exec
-- Invalidates every session issued before this call: the middleware compares the
-- token's "ver" claim against this value on each request.
UPDATE users
SET token_version = token_version + 1
WHERE id = ?;

-- name: GetSessionState :one
-- The authoritative account state for an authenticated request. The admin gate
-- reads role from here rather than from the token, so demoting a user takes
-- effect on the next request.
SELECT id, email, role, token_version, is_active
FROM users WHERE id = ?;

-- name: DebitUserBalanceIfSufficient :execrows
-- The debit and the affordability check are the same statement.
--
-- The old DebitUserBalance subtracted unconditionally, after the service had
-- read the balance and compared it in Go. Two requests spending the same balance
-- both read a sufficient figure and both debited, driving it negative. With the
-- comparison in the WHERE clause the second update matches no rows, and the
-- caller sees zero affected and aborts.
--
-- RowsAffected is the answer; there is nothing to read afterwards.
UPDATE users
SET balance_cents = balance_cents - ?
WHERE id = ? AND balance_cents >= ?;

-- name: CreditUserBalance :exec
-- The refund half of a debit. Used when an order is cancelled, and it is the
-- only way a balance goes up outside the seed.
UPDATE users
SET balance_cents = balance_cents + ?
WHERE id = ?;

-- name: CountUsers :one
-- Dashboard aggregate. Stats used to call GetAllUsers and take len() of the
-- result, so asking for one number cost a full table read and a full
-- materialisation of every user in the shop.
SELECT count(*) FROM users;

-- name: SetUserRole :exec
-- The role change is admin-only at the route, and AdminOnly re-reads the role
-- from this column on every admin request, so a forged claim does not survive
-- the change and a demoted administrator loses access immediately.
UPDATE users
SET role = ?
WHERE id = ?;

-- name: SetUserActive :execrows
-- The "dar de baja" / "dar de alta" flip. The service bumps token_version when
-- taking somebody off, so the sessions they already hold die with it; this
-- statement only moves the bit.
UPDATE users
SET is_active = ?
WHERE id = ?;