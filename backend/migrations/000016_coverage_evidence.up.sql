-- Coverage evidence (audit correction): proving a window observable requires
-- more than "recorder was running and inserts succeeded":
--
--   last_feed_degraded_at  — a heartbeat sampled the managed provider NOT
--                            healthy (state≠connected or quote stale). A
--                            running recorder over a dead feed must not
--                            claim an observable window.
--   last_heartbeat_gap_at  — the heartbeat detected its own stall (>2×
--                            cadence between beats) — proves continuity
--                            was lost, not just start/end endpoints.
--   last_drop_at           — when a persistence batch failed (window-scoped
--                            evidence; row-lifetime dropped_batches was
--                            too coarse).
--   feed_state             — last sampled provider state (informational).
--
-- All are window-scoped timestamps: evidence must predate the window or be
-- absent for coverage to be complete — degradations BEFORE a window must
-- not permanently poison all later windows.
ALTER TABLE quote_coverage
    ADD COLUMN last_feed_degraded_at  TIMESTAMPTZ,
    ADD COLUMN last_heartbeat_gap_at  TIMESTAMPTZ,
    ADD COLUMN last_drop_at           TIMESTAMPTZ,
    ADD COLUMN feed_state             TEXT;
