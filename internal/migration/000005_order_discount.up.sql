-- Puts the discount on the order, replacing the in-memory ledger.
--
-- internal/service/discount_ledger.go kept "which coupon is applied to this
-- customer and how much has it knocked off so far" in a map guarded by a mutex.
-- Three things follow from that. It is lost on restart, so a customer's pending
-- discount evaporates. It is per process, so two instances disagree. And it is
-- the mechanism behind #14: because the discount compounds onto whatever the
-- running total already was, redeeming the same code ten times in parallel
-- produces a price close to zero.
--
-- The discount is a property of one order, so it belongs in the order. With
-- subtotal_cents, discount_cents and coupon_id on the row, the amount charged is
-- reproducible from the row itself, and the ledger has nothing left to do.
BEGIN;

ALTER TABLE orders
    ADD COLUMN subtotal_cents  bigint NOT NULL DEFAULT 0,
    ADD COLUMN discount_cents bigint NOT NULL DEFAULT 0,
    ADD COLUMN coupon_id      bigint REFERENCES coupons (id);

-- Orders that already exist were priced by the buggy path, so their totals are
-- not trustworthy. Recomputing subtotal from their lines gives a real figure and
-- leaves the discount at zero, which is the honest answer for a discount that
-- was never recorded anywhere.
UPDATE orders o
SET subtotal_cents = COALESCE(
    (SELECT sum(oi.quantity * oi.unit_price_cents) FROM order_items oi WHERE oi.order_id = o.id),
    0);

-- The totals no longer agree with the lines, and an order whose recorded total
-- is not derivable from its own contents is exactly the kind of record the
-- next audit will question.
UPDATE orders SET total_cents = subtotal_cents - discount_cents;

CREATE INDEX orders_coupon_id_idx ON orders (coupon_id);

COMMIT;
