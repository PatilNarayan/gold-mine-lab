ALTER TABLE shadow_orders DROP CONSTRAINT shadow_orders_status_check;
ALTER TABLE shadow_orders ADD CONSTRAINT shadow_orders_status_check
    CHECK (status IN ('pending','filled','rejected','expired'));
ALTER TABLE shadow_positions DROP COLUMN IF EXISTS session_id;
ALTER TABLE shadow_orders DROP COLUMN IF EXISTS session_id;
DROP TABLE IF EXISTS shadow_sessions;
