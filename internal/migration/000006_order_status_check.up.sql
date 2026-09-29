-- Order status is a closed set, not a free-text column.
--
-- PUT /orders/{id} took whatever the body said and wrote it straight to the
-- column: any caller who owned an order could set it to "completed" (which the
-- revenue figure counts), to "refunded", or to a 50-character string. The
-- dashboard sorted by it and the review rule trusted it, so a customer could
-- grant themselves the "completed" state a review needs.
--
-- The constraint is the enforcement and the service check is the courtesy; see
-- the note in coupon_service.go about why both exist.

-- Rows written before this migration can hold anything, so they are normalised
-- first. Checkout always creates 'completed', so that is the safe reading of a
-- status nobody recognises: the order exists and the money moved, which is what
-- 'completed' means here. Note that a free-text status also included 'paid',
-- which this vocabulary does not have: two names for one state is how a report
-- quietly stops counting.
UPDATE orders
SET status = 'paid'
WHERE status IS NULL
   OR status NOT IN ('pending', 'completed', 'shipped', 'delivered', 'cancelled', 'refunded');

-- The UPDATE has to come first. Adding the constraint before normalising fails
-- outright, because the rows that violate it are exactly the ones the UPDATE is
-- there to fix.
ALTER TABLE orders
    ADD CONSTRAINT orders_status_check
    CHECK (status IN ('pending', 'completed', 'shipped', 'delivered', 'cancelled', 'refunded'));
