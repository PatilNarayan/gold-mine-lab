-- Prospective market-cost evidence: one row per DISTINCT live quote event
-- carrying bid/ask. Not a tick log — the recorder dedupes identical
-- observations, so row growth tracks genuine quote changes.
CREATE TABLE quote_observations (
    seq           BIGSERIAL PRIMARY KEY,
    provider      TEXT NOT NULL,
    symbol        TEXT NOT NULL,
    provider_time TIMESTAMPTZ,             -- provider's own timestamp (nullable: provider may not supply)
    bid           DOUBLE PRECISION,
    ask           DOUBLE PRECISION,
    mid           DOUBLE PRECISION,        -- (bid+ask)/2 when both legs exist, else provider's price
    spread_price  DOUBLE PRECISION,        -- ask - bid; NULL when bid/ask unavailable (missing, never coerced to zero)
    spread_bp     DOUBLE PRECISION,        -- spread_price / mid * 10_000
    stale         BOOLEAN NOT NULL DEFAULT false, -- provider_time lagged receipt beyond the staleness bound
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_quote_observations_symbol_time ON quote_observations (symbol, provider_time DESC);
