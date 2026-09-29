-- Removes stored cardholder data that must never be persisted after
-- authorisation.
--
-- cvv is dropped rather than encrypted. PCI DSS forbids storing it once the
-- authorisation has happened, so there is no key that would make keeping it
-- acceptable, and no legitimate read that would break.
--
-- card_number is dropped for the same reason in practice: a PAN is sensitive
-- authentication data, and storing it means a database leak becomes a card
-- compromise for every customer at once. What a receipt needs is the brand and
-- the last four digits, and those are kept.
--
-- The application is a shop with no real payment processor, so it derives
-- brand and last4 from the number the client sent over TLS and then discards the
-- number. It never comes back out of the database because it is never in it.

ALTER TABLE payment_methods ADD COLUMN brand varchar(32) NOT NULL DEFAULT 'unknown';
ALTER TABLE payment_methods ADD COLUMN last4 varchar(4) NOT NULL DEFAULT '0000';

-- Backfill from whatever PAN is already stored, so a live database keeps its
-- cards instead of degrading every one of them to 0000.
UPDATE payment_methods
SET last4 = right(regexp_replace(card_number, '\D', '', 'g'), 4),
    brand = CASE
        WHEN card_number LIKE '4%' THEN 'visa'
        WHEN card_number LIKE '5%' THEN 'mastercard'
        WHEN card_number LIKE '3%' THEN 'amex'
        WHEN card_number LIKE '6%' THEN 'discover'
        ELSE 'unknown'
    END;

ALTER TABLE payment_methods DROP COLUMN cvv;
ALTER TABLE payment_methods DROP COLUMN card_number;

-- expiry was a single opaque integer packing month and year. Splitting it means
-- a card can be checked for expiry without decoding a number.
ALTER TABLE payment_methods ADD COLUMN expiry_month smallint NOT NULL DEFAULT 1;
ALTER TABLE payment_methods ADD COLUMN expiry_year smallint NOT NULL DEFAULT 2030;
ALTER TABLE payment_methods DROP COLUMN expiry;

-- No query ever filtered by holder name, and it is personal data sitting in a
-- table an admin can list.
ALTER TABLE payment_methods DROP COLUMN card_holder;
