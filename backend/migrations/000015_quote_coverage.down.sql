ALTER TABLE shadow_orders DROP CONSTRAINT shadow_orders_status_check;
ALTER TABLE shadow_orders ADD CONSTRAINT shadow_orders_status_check
    CHECK (status IN ('pending','filled','rejected','expired','missed'));
DROP TABLE IF EXISTS quote_coverage;
