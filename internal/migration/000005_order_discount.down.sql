-- Undoes 000005.
--
-- Safe in both directions: subtotal_cents and discount_cents are recorded on the
-- order, so nothing is lost by dropping them. coupon_id goes with them. A coupon
-- redemption is not deleted, because the row that prevents a second redemption
-- is still wanted.
BEGIN;

DROP INDEX IF EXISTS orders_coupon_id_idx;

ALTER TABLE orders
    DROP COLUMN IF EXISTS coupon_id,
    DROP COLUMN IF EXISTS discount_cents,
    DROP COLUMN IF EXISTS subtotal_cents;

COMMIT;
