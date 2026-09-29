-- name: GetAllCoupons :many
SELECT * FROM coupons;

-- name: GetCouponByID :one
SELECT * FROM coupons WHERE id = ?;

-- name: GetCouponByCode :one
SELECT * FROM coupons WHERE code = ?;

-- name: CreateCoupon :execresult
INSERT INTO coupons (code, discount_percent, max_uses, expires_at, used_count)
VALUES (?, ?, ?, ?, ?);

-- name: UpdateCoupon :exec
UPDATE coupons
SET code = ?, discount_percent = ?, max_uses = ?, expires_at = ?, used_count = ?
WHERE id = ?;

-- name: DeleteCoupon :execrows
-- The row count is what distinguishes deleting a coupon from deleting nothing.
-- As :exec the panel got a 204 and a "borrado" for a coupon that was never
-- there. The caller turns zero into sql.ErrNoRows, which is a 404.
DELETE FROM coupons WHERE id = ?;

-- name: RedeemCoupon :execrows
-- The redemption, done as a single conditional update.
--
-- used_count < max_uses and the expiry are both part of the WHERE clause, so two
-- concurrent redemptions of the last remaining use cannot both match: the second
-- one updates zero rows. The old code read the count, compared it in Go and
-- wrote it back, which is a time-of-check to time-of-use gap wide enough for a
-- dozen parallel requests to walk through together.
--
-- The caller must check the returned row count. Zero means the coupon was
-- exhausted or expired, and the whole transaction has to be abandoned.
UPDATE coupons
SET used_count = used_count + 1
WHERE id = ?
  AND used_count < max_uses
  AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP);

-- name: GiveBackCouponUse :exec
-- Returns a use when an order is abandoned after the coupon was spent, so a
-- failed checkout does not permanently consume a customer's single use.
UPDATE coupons SET used_count = used_count - 1 WHERE id = ? AND used_count > 0;

-- name: CountCoupons :one
-- Dashboard aggregate: the old Stats loaded every row of the table to take
-- len() of it. Counting in the database is a single index-only scan.
SELECT count(*) FROM coupons;