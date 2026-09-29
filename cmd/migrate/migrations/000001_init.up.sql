-- Consolidated TiDB schema (MySQL dialect) — replaces 6 Postgres migrations.
-- All PRIMARY KEYs use AUTO_RANDOM (BIGINT) as requested.
-- No RETURNING clauses; inserts use LastInsertId().
-- CHECK constraints require TiDB v8.5+ (GA); Starter supports them.

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- users
CREATE TABLE IF NOT EXISTS `users` (
    `id`              BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `first_name`      VARCHAR(255) NOT NULL,
    `last_name`       VARCHAR(255) NOT NULL,
    `email`           VARCHAR(255) NOT NULL UNIQUE,
    `password_hash`   VARCHAR(255) NOT NULL,
    `role`            VARCHAR(50) NOT NULL DEFAULT 'customer',
    `balance_cents`   BIGINT NOT NULL DEFAULT 1000,
    `token_version`   INT NOT NULL DEFAULT 0,
    `created_at`      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- password_reset_tokens
CREATE TABLE IF NOT EXISTS `password_reset_tokens` (
    `id`          BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `user_id`     BIGINT NOT NULL,
    `token`       VARCHAR(128) NOT NULL,
    `expires_at`  DATETIME(3) NOT NULL,
    `used_at`     DATETIME(3) NULL,
    `created_at`  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX IF NOT EXISTS `idx_password_reset_tokens_token` ON `password_reset_tokens` (`token`);

-- Replace UNIQUE(user_id) with generated column for exactly-one-live-token invariant
-- Must drop FK first because it depends on the index
ALTER TABLE `password_reset_tokens` DROP FOREIGN KEY `fk_password_reset_tokens_user`;
DROP INDEX IF EXISTS `idx_password_reset_tokens_user_id` ON `password_reset_tokens`;
ALTER TABLE `password_reset_tokens` ADD COLUMN `live_user_id` BIGINT AS (IF(`used_at` IS NULL, `user_id`, NULL)) VIRTUAL;
CREATE UNIQUE INDEX IF NOT EXISTS `idx_password_reset_tokens_live_user` ON `password_reset_tokens` (`live_user_id`);
-- Re-add FK (will use the new unique index implicitly or the user_id column index)
ALTER TABLE `password_reset_tokens` ADD CONSTRAINT `fk_password_reset_tokens_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE;

-- addresses
CREATE TABLE IF NOT EXISTS `addresses` (
    `id`        BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `user_id`   BIGINT NOT NULL,
    `street`    VARCHAR(255) NOT NULL,
    `city`      VARCHAR(255) NOT NULL,
    `zip`       VARCHAR(20) NOT NULL,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX IF NOT EXISTS `idx_addresses_user_id` ON `addresses` (`user_id`);

-- books
CREATE TABLE IF NOT EXISTS `books` (
    `id`               BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `author`           VARCHAR(255) NOT NULL,
    `title`            VARCHAR(255) NOT NULL,
    `pages`            INT NOT NULL,
    `isbn`             VARCHAR(20) NOT NULL UNIQUE,
    `price_cents`      BIGINT NOT NULL,
    `stock`            INT NOT NULL,
    `url_cover_image`  VARCHAR(500),
    `created_at`       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX IF NOT EXISTS `idx_books_isbn` ON `books` (`isbn`);

-- coupons
CREATE TABLE IF NOT EXISTS `coupons` (
    `id`                BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `code`              VARCHAR(64) NOT NULL UNIQUE,
    `discount_percent`  INT NOT NULL,
    `max_uses`          INT NOT NULL,
    `expires_at`        DATETIME(3) NOT NULL,
    `used_count`        INT NOT NULL DEFAULT 0,
    `created_at`        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX IF NOT EXISTS `idx_coupons_code` ON `coupons` (`code`);

-- orders
CREATE TABLE IF NOT EXISTS `orders` (
    `id`              BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `user_id`         BIGINT NOT NULL,
    `status`          VARCHAR(50) NOT NULL,
    `subtotal_cents`  BIGINT NOT NULL DEFAULT 0,
    `discount_cents`  BIGINT NOT NULL DEFAULT 0,
    `total_cents`     BIGINT NOT NULL DEFAULT 0,
    `coupon_id`       BIGINT NULL,
    `created_at`      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- The vocabulary of order states is closed. `completed` is what checkout
-- produces and what the review rule trusts as proof of purchase; `paid` was
-- dropped on purpose — two names for one state ends with a report that stops
-- counting without anyone noticing.
ALTER TABLE `orders` ADD CONSTRAINT `orders_status_check`
    CHECK (`status` IN ('pending','completed','shipped','delivered','cancelled','refunded'));

CREATE INDEX IF NOT EXISTS `idx_orders_user_id` ON `orders` (`user_id`);
CREATE INDEX IF NOT EXISTS `idx_orders_coupon_id` ON `orders` (`coupon_id`);

-- order_items
CREATE TABLE IF NOT EXISTS `order_items` (
    `id`                 BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `order_id`           BIGINT NOT NULL,
    `book_id`            BIGINT NOT NULL,
    `quantity`           INT NOT NULL,
    `unit_price_cents`   BIGINT NOT NULL,
    `created_at`         DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX IF NOT EXISTS `idx_order_items_order_id` ON `order_items` (`order_id`);
CREATE INDEX IF NOT EXISTS `idx_order_items_book_id` ON `order_items` (`book_id`);

-- reviews
CREATE TABLE IF NOT EXISTS `reviews` (
    `id`            BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `book_id`       BIGINT NOT NULL,
    `user_id`       BIGINT NOT NULL,
    `rating`        INT NOT NULL,
    `comment`       TEXT,
    `image_url`     VARCHAR(1000) NOT NULL DEFAULT '',
    `created_at`    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX IF NOT EXISTS `reviews_one_per_book_and_user` ON `reviews` (`book_id`, `user_id`);
ALTER TABLE `reviews` ADD CONSTRAINT `reviews_rating_range` CHECK (`rating` BETWEEN 1 AND 5);

-- order_items CHECK (added after table exists to avoid forward ref issues)
ALTER TABLE `order_items` ADD CONSTRAINT `order_items_quantity_range` CHECK (`quantity` BETWEEN 1 AND 10);

-- coupons CHECK
ALTER TABLE `coupons` ADD CONSTRAINT `coupons_percent_range` CHECK (`discount_percent` >= 0 AND `discount_percent` <= 100);
ALTER TABLE `coupons` ADD CONSTRAINT `coupons_max_uses_positive` CHECK (`max_uses` > 0);
ALTER TABLE `coupons` ADD CONSTRAINT `coupons_used_within_limit` CHECK (`used_count` >= 0 AND `used_count` <= `max_uses`);

-- coupon_redemptions
CREATE TABLE IF NOT EXISTS `coupon_redemptions` (
    `id`            BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `coupon_id`     BIGINT NOT NULL,
    `user_id`       BIGINT NOT NULL,
    `order_id`      BIGINT NULL,
    `redeemed_at`   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX IF NOT EXISTS `coupon_redemptions_one_per_user` ON `coupon_redemptions` (`coupon_id`, `user_id`);
CREATE INDEX IF NOT EXISTS `idx_coupon_redemptions_coupon_id` ON `coupon_redemptions` (`coupon_id`);
CREATE INDEX IF NOT EXISTS `idx_coupon_redemptions_user_id` ON `coupon_redemptions` (`user_id`);
CREATE INDEX IF NOT EXISTS `idx_coupon_redemptions_order_id` ON `coupon_redemptions` (`order_id`);

-- payment_methods (PCI-compliant: no PAN, no CVV)
CREATE TABLE IF NOT EXISTS `payment_methods` (
    `id`             BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `user_id`        BIGINT NOT NULL,
    `brand`          VARCHAR(32) NOT NULL DEFAULT 'unknown',
    `last4`          VARCHAR(4) NOT NULL DEFAULT '0000',
    `expiry_month`   SMALLINT NOT NULL DEFAULT 1,
    `expiry_year`    SMALLINT NOT NULL DEFAULT 2030,
    `created_at`     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX IF NOT EXISTS `idx_payment_methods_user_id` ON `payment_methods` (`user_id`);

-- Foreign keys (TiDB supports them; we define them but security still validates in service layer)
ALTER TABLE `addresses` ADD CONSTRAINT `fk_addresses_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE;
ALTER TABLE `orders` ADD CONSTRAINT `fk_orders_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE;
ALTER TABLE `orders` ADD CONSTRAINT `fk_orders_coupon` FOREIGN KEY (`coupon_id`) REFERENCES `coupons` (`id`) ON DELETE SET NULL;
ALTER TABLE `order_items` ADD CONSTRAINT `fk_order_items_order` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE CASCADE;
ALTER TABLE `order_items` ADD CONSTRAINT `fk_order_items_book` FOREIGN KEY (`book_id`) REFERENCES `books` (`id`) ON DELETE RESTRICT;
ALTER TABLE `reviews` ADD CONSTRAINT `fk_reviews_book` FOREIGN KEY (`book_id`) REFERENCES `books` (`id`) ON DELETE CASCADE;
ALTER TABLE `reviews` ADD CONSTRAINT `fk_reviews_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE;
ALTER TABLE `coupon_redemptions` ADD CONSTRAINT `fk_coupon_redemptions_coupon` FOREIGN KEY (`coupon_id`) REFERENCES `coupons` (`id`) ON DELETE CASCADE;
ALTER TABLE `coupon_redemptions` ADD CONSTRAINT `fk_coupon_redemptions_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE;
ALTER TABLE `coupon_redemptions` ADD CONSTRAINT `fk_coupon_redemptions_order` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`) ON DELETE SET NULL;
ALTER TABLE `payment_methods` ADD CONSTRAINT `fk_payment_methods_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE;

SET FOREIGN_KEY_CHECKS = 1;