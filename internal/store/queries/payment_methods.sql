-- There is deliberately no "list every payment method" query. It was here for
-- the #12 fix and it was never routed, so deleting it removed a query whose
-- only behaviour is to hand back every card in the table to whoever asks.

-- name: GetPaymentMethodByID :one
SELECT * FROM payment_methods WHERE id = ?;

-- name: GetPaymentMethodsByUserID :many
SELECT * FROM payment_methods WHERE user_id = ?;

-- name: CreatePaymentMethod :execresult
-- brand and last4 only. There is no column for the card number, which is the
-- point: the value the client sent is not storable.
INSERT INTO payment_methods (user_id, brand, last4, expiry_month, expiry_year)
VALUES (?, ?, ?, ?, ?);

-- name: UpdatePaymentMethod :exec
-- user_id is deliberately absent. Reassigning a card to another account is a
-- takeover primitive, and the handler already forces the stored owner.
UPDATE payment_methods
SET brand = ?, last4 = ?, expiry_month = ?, expiry_year = ?
WHERE id = ?;

-- name: DeletePaymentMethod :exec
DELETE FROM payment_methods WHERE id = ?;

-- name: CountPaymentMethods :one
-- Dashboard aggregate: the old Stats loaded every row of the table to take
-- len() of it. Counting in the database is a single index-only scan.
SELECT count(*) FROM payment_methods;