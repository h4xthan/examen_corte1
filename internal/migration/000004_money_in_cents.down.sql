-- Undoes 000004.
--
-- This is lossy in a way the earlier down files were not, and it is honest to
-- say so: cents convert back to the old NUMERIC columns exactly, but the
-- constraints are gone, and the coupon redemption history is dropped. Rows that
-- existed before the unique index are not restored as duplicates — they simply
-- stop existing. The intended use is a development rollback while iterating, not
-- a production plan.

DROP TABLE IF EXISTS coupon_redemptions;

ALTER TABLE coupons
    DROP CONSTRAINT IF EXISTS coupons_percent_range,
    DROP CONSTRAINT IF EXISTS coupons_max_uses_positive,
    DROP CONSTRAINT IF EXISTS coupons_used_within_limit;

DROP INDEX IF EXISTS reviews_one_per_book_and_user;

ALTER TABLE reviews
    DROP CONSTRAINT IF EXISTS reviews_rating_range;

ALTER TABLE order_items
    ADD COLUMN unit_price numeric(10, 2) NOT NULL DEFAULT 0;
UPDATE order_items SET unit_price = unit_price_cents / 100.0;
ALTER TABLE order_items DROP COLUMN unit_price_cents;

ALTER TABLE order_items
    DROP CONSTRAINT IF EXISTS order_items_quantity_positive;

ALTER TABLE books ADD COLUMN price numeric(10, 2) NOT NULL DEFAULT 0;
UPDATE books SET price = price_cents / 100.0;
ALTER TABLE books DROP COLUMN price_cents;

ALTER TABLE orders ADD COLUMN total numeric(10, 2) NOT NULL DEFAULT 0;
UPDATE orders SET total = total_cents / 100.0;
ALTER TABLE orders DROP COLUMN total_cents;
