-- name: CreateAuditLog :execlastid
-- One row per operating-history entry. user_id/user_email may be NULL (a
-- public catalogue read is not attributed to a staff member), entity_id is the
-- row the action touched, and details is a short human line such as the title
-- or the backup filename.
INSERT INTO audit_log (user_id, user_email, entity, action, entity_id, details)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListAuditLogs :many
-- The three panels' history, newest first. entity filters the view a tab asks
-- for; limit is pages of 200 so a history cannot grow an unbounded response.
SELECT id, user_id, user_email, entity, action, entity_id, details, created_at
FROM audit_log
WHERE (entity = sqlc.arg(entity) OR sqlc.arg(entity) = '')
ORDER BY id DESC
LIMIT ?;