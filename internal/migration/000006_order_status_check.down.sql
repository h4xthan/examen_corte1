-- The CHECK goes; the normalising UPDATE is not undone, because there is no way
-- to know what a free-text status used to say. Reverting this migration is only
-- reasonable on a database that is about to be dropped anyway.
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
