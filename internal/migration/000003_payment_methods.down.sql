-- Undoes 000003 by re-adding the columns it dropped.
--
-- This is a development rollback, not a production plan. The PAN and CVV it
-- restores would be null, and re-adding a NOT NULL column for them is not
-- meaningful: the data is gone and it is not coming back. The only reason this
-- file exists is so a schema built from these migrations stays symmetrical while
-- they are being iterated on, before anything real is stored.

ALTER TABLE payment_methods ADD COLUMN card_number varchar(20);
ALTER TABLE payment_methods ADD COLUMN card_holder varchar(255);
ALTER TABLE payment_methods ADD COLUMN cvv integer;
ALTER TABLE payment_methods ADD COLUMN expiry integer;

UPDATE payment_methods
SET card_number = brand || last4,
    card_holder = '',
    cvv = 0,
    expiry = expiry_year * 100 + expiry_month;

ALTER TABLE payment_methods ALTER COLUMN card_number SET NOT NULL;
ALTER TABLE payment_methods ALTER COLUMN card_holder SET NOT NULL;
ALTER TABLE payment_methods ALTER COLUMN cvv SET NOT NULL;
ALTER TABLE payment_methods ALTER COLUMN expiry SET NOT NULL;

ALTER TABLE payment_methods DROP COLUMN brand;
ALTER TABLE payment_methods DROP COLUMN last4;
ALTER TABLE payment_methods DROP COLUMN expiry_month;
ALTER TABLE payment_methods DROP COLUMN expiry_year;
