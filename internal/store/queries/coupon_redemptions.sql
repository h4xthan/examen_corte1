-- name: CreateCouponRedemption :execresult
-- The record that a customer has spent a coupon. The unique index on
-- (coupon_id, user_id) is what makes a second attempt fail, and it fails in the
-- database rather than in a check that another request can slip between.
INSERT INTO coupon_redemptions (coupon_id, user_id, order_id)
VALUES (?, ?, ?);

-- name: GetCouponRedemption :one
SELECT * FROM coupon_redemptions WHERE coupon_id = ? AND user_id = ?;

-- name: DeleteCouponRedemptionByOrder :exec
-- Undoes a redemption when the order it belonged to is rolled back by hand.
DELETE FROM coupon_redemptions WHERE order_id = ?;

-- name: CountCouponRedemptions :one
SELECT count(*) FROM coupon_redemptions WHERE coupon_id = ?;