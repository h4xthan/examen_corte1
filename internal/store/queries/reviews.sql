-- name: GetAllReviews :many
SELECT * FROM reviews;

-- name: GetReviewByID :one
SELECT * FROM reviews WHERE id = ?;

-- name: GetReviewsByBookID :many
SELECT * FROM reviews WHERE book_id = ?;

-- name: GetReviewByBookAndUser :one
SELECT * FROM reviews WHERE book_id = ? AND user_id = ?;

-- name: CreateReview :execresult
INSERT INTO reviews (book_id, user_id, rating, comment, image_url)
VALUES (?, ?, ?, ?, ?);

-- name: UpdateReview :exec
UPDATE reviews
SET book_id = ?, user_id = ?, rating = ?, comment = ?, image_url = ?
WHERE id = ?;

-- name: DeleteReview :exec
DELETE FROM reviews WHERE id = ?;

-- name: GetReviewOwnerID :one
-- Reviews are public to read, so the owner is needed only to decide who may
-- edit or delete one.
SELECT user_id FROM reviews WHERE id = ?;

-- name: ListReviewsForModeration :many
-- The moderation queue.
--
-- The join is what makes the row useful in a table: a review on its own is a
-- rating and a paragraph, and the moderator's first question is always "of what,
-- by whom". Fetching that per row would be an N+1 in the one place where a
-- moderator is scrolling a queue, so it is done once in the database.
--
-- Newest first, because a queue is worked from the top.
SELECT
    r.id,
    r.book_id,
    b.title AS book_title,
    r.user_id,
    u.email AS author_email,
    r.rating,
    r.comment,
    r.image_url,
    r.created_at
FROM reviews r
JOIN books b ON b.id = r.book_id
JOIN users u ON u.id = r.user_id
ORDER BY r.id DESC;

-- name: HasPurchasedBook :one
-- Whether a customer actually bought a title.
--
-- The join through order_items is what makes this a real purchase check: an
-- order row alone proves nothing, because the previous checkout accepted a
-- client-supplied total and could record an order that was never paid for.
-- A review has to be attached to a line on a completed order.
--
-- EXISTS rather than a count, because the answer is a boolean and counting rows
-- that may be numerous tells nobody anything extra.
SELECT EXISTS (
    SELECT 1
    FROM order_items oi
    JOIN orders o ON o.id = oi.order_id
    WHERE oi.book_id = ?
      AND o.user_id = ?
      AND o.status = 'completed'
);