-- name: GetAllOrderItems :many
SELECT * FROM order_items;

-- name: GetOrderItemByID :one
SELECT * FROM order_items WHERE id = ?;

-- name: GetOrderItemsByOrderID :many
SELECT * FROM order_items WHERE order_id = ?;

-- name: CreateOrderItem :execresult
INSERT INTO order_items (order_id, book_id, quantity, unit_price_cents)
VALUES (?, ?, ?, ?);

-- name: UpdateOrderItem :exec
UPDATE order_items
SET order_id = ?, book_id = ?, quantity = ?, unit_price_cents = ?
WHERE id = ?;

-- name: DeleteOrderItem :exec
DELETE FROM order_items WHERE id = ?;

-- name: GetOrderItemOwnerID :one
-- The account that owns an order item is two joins away, and the handler needs
-- it to answer "is this yours" before touching the row. Returning the owner
-- rather than the item keeps the check in one place instead of every caller
-- having to remember to walk the relationship.
SELECT o.user_id
FROM order_items oi
JOIN orders o ON o.id = oi.order_id
WHERE oi.id = ?;