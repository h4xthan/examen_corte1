-- Reverts 000002_auth_hardening.up.sql.
--
-- The partial unique index and the token index are dropped before the column
-- type changes, because changing the type back while a unique index exists on
-- it requires the index to be rebuilt.

DROP INDEX IF EXISTS idx_password_reset_tokens_user_id;
DROP INDEX IF EXISTS idx_password_reset_tokens_user_live;
DROP INDEX IF EXISTS idx_password_reset_tokens_token;

ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS used_at;
ALTER TABLE password_reset_tokens ALTER COLUMN token TYPE TEXT;

ALTER TABLE users DROP COLUMN IF EXISTS token_version;
