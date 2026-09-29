-- name: GetAllBooks :many
SELECT * FROM books;

-- name: GetBookByID :one
SELECT * FROM books WHERE id = ?;

-- name: CreateBook :execresult
INSERT INTO books (author, title, pages, isbn, price_cents, stock, url_cover_image)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpdateBook :exec
UPDATE books
SET author = ?, title = ?, pages = ?, isbn = ?, price_cents = ?, stock = ?, url_cover_image = ?
WHERE id = ?;

-- name: DeleteBook :execrows
-- The row count is the whole point of this one. As :exec it was indistinguishable
-- from deleting something that exists: no error, no count, a 204 to the panel and
-- the book still in the catalogue on the next load. The caller turns a count of
-- zero into sql.ErrNoRows, which the transport answers as 404.
DELETE FROM books WHERE id = ?;

-- name: DecrementBookStock :execrows
-- Conditional on stock, so two concurrent buyers cannot both pass. The caller
-- checks the returned row count: one means the stock was really reserved, zero
-- means somebody got there first and the whole order has to be abandoned.
UPDATE books SET stock = stock - ? WHERE id = ? AND stock >= ?;

-- name: IncrementBookStock :exec
UPDATE books SET stock = stock + ? WHERE id = ?;

-- name: CountBooks :one
-- Dashboard aggregate: the old Stats loaded every row of the table to take
-- len() of it. Counting in the database is a single index-only scan.
SELECT count(*) FROM books;