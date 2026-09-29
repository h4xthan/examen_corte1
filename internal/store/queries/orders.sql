-- name: GetAllOrders :many
SELECT * FROM orders;

-- name: GetOrderByID :one
SELECT * FROM orders WHERE id = ?;

-- name: CreateOrder :execresult
-- The four money columns are set together, from values the service computed
-- inside the transaction. subtotal_cents and discount_cents are stored alongside
-- the total so the charge can be checked against the lines that produced it
-- later, without trusting whatever the ledger used to say.
INSERT INTO orders (user_id, status, subtotal_cents, discount_cents, total_cents, coupon_id)
VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateOrder :exec
-- user_id is absent on purpose. Reassigning an order to a different account is a
-- takeover primitive, and no caller should be able to do it by editing one.
UPDATE orders
SET status = ?, subtotal_cents = ?, discount_cents = ?, total_cents = ?, coupon_id = ?
WHERE id = ?;

-- name: DeleteOrder :exec
DELETE FROM orders WHERE id = ?;

-- name: GetOrdersByUserID :many
-- A customer listing their own orders. The global listing is admin-only, so
-- this is what the frontend uses.
SELECT * FROM orders WHERE user_id = ? ORDER BY created_at DESC, id DESC;

-- CountOrders and SumOrderTotals exist for the admin dashboard, which used to load
-- every row of every table just to count them and add up a column: linear in the
-- size of the shop, and worse with every visit to the panel.

-- name: CountOrders :one
SELECT count(*) FROM orders;

-- name: SumOrderTotals :one
-- Revenue, not volume. The panel labels this figure what it is: money actually
-- taken. An order that was cancelled or refunded has had its money returned, so
-- summing it in would overstate the shop by exactly the refunds it granted.
-- 'pending' is not money taken either; nothing has been charged yet.
--
-- The CAST is not decoration. MySQL widens SUM() over an integer column to
-- DECIMAL so the result cannot overflow, and the driver hands a DECIMAL back as
-- []byte. Left uncast, sqlc types the column as interface{}, and the store
-- received bytes it could only reject, which is why GET /admin/stats answered
-- 500 with "unexpected type for sum" and took the whole panel down with it.
-- CAST(... AS SIGNED) makes TiDB return BIGINT, so the sum is an int64 from the
-- wire to the JSON, and no amount of money passes through a float.
SELECT COALESCE(CAST(SUM(total_cents) AS SIGNED), 0)
FROM orders
WHERE status IN ('completed', 'shipped', 'delivered');