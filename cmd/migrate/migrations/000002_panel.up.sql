-- Panel de administración, fase de usuarios/auditoría/respaldos.
-- Idempotente por diseño (ver cmd/migrate): en una base ya creada, el ALTER
-- ADD COLUMN devuelve el error 1060 que el runner ignora (columna existente) y
-- el CREATE INDEX devuelve el 1061 (índice con el mismo nombre).

-- Dar de baja: la columna de alta la añade este ALTER solo a las bases viejas.
ALTER TABLE `users` ADD COLUMN `is_active` TINYINT(1) NOT NULL DEFAULT 1;

-- Operaciones de la interfaz de administración: altas, bajas, modificaciones,
-- consultas y respaldos, para las tres interfaces que exige la evaluación.
-- user_id/user_email son el actor (NULL cuando la petición era anónima, p.ej.
-- una consulta al catálogo desde la tienda); entity_id es el recurso tocado
-- (NULL para acciones como "crear respaldo" que no tienen fila propia).
CREATE TABLE IF NOT EXISTS `audit_log` (
    `id`         BIGINT NOT NULL AUTO_RANDOM PRIMARY KEY,
    `user_id`    BIGINT NULL,
    `user_email` VARCHAR(255) NULL,
    `entity`     VARCHAR(16) NOT NULL,
    `action`     VARCHAR(16) NOT NULL,
    `entity_id`  BIGINT NULL,
    `details`    VARCHAR(255) NULL,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX `idx_audit_log_entity_id` ON `audit_log` (`entity`, `id`);