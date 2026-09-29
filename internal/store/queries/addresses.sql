-- name: GetAllAddresses :many
SELECT * FROM addresses;

-- name: GetAddressByID :one
SELECT * FROM addresses WHERE id = ?;

-- name: GetAddressesByUserID :many
SELECT * FROM addresses WHERE user_id = ?;

-- name: CreateAddress :execresult
INSERT INTO addresses (user_id, street, city, zip)
VALUES (?, ?, ?, ?);

-- name: UpdateAddress :exec
UPDATE addresses
SET user_id = ?, street = ?, city = ?, zip = ?
WHERE id = ?;

-- name: DeleteAddress :exec
DELETE FROM addresses WHERE id = ?;