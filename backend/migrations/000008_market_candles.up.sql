-- Provider-sourced market candles: the persistent historical store that
-- replaces bundled CSV datasets. bar_time is the canonical Kyiv civil-time
-- session label (naive broker wall time stored as UTC), identical to the
-- convention the historical CSV files and live books use — identical labels
-- always denote identical market intervals.

CREATE TABLE IF NOT EXISTS market_candles (
    asset_id    TEXT        NOT NULL,
    timeframe   TEXT        NOT NULL,             -- '1m' '5m' '15m' '1h' '4h' '1d'
    bar_time    TIMESTAMPTZ NOT NULL,             -- session label (Kyiv civil boundary)
    open        DOUBLE PRECISION NOT NULL,
    high        DOUBLE PRECISION NOT NULL,
    low         DOUBLE PRECISION NOT NULL,
    close       DOUBLE PRECISION NOT NULL,
    volume      DOUBLE PRECISION NOT NULL DEFAULT 0,
    provider    TEXT        NOT NULL,             -- 'tiingo' | 'twelvedata'
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, timeframe, bar_time)
);

CREATE INDEX IF NOT EXISTS market_candles_window
    ON market_candles (asset_id, timeframe, bar_time DESC);

-- Contiguous sync coverage per asset×timeframe: every candle in
-- [first_bar, last_bar] is present and provider-validated. Bars outside the
-- span may exist (partial writes from an interrupted sync are kept but not
-- claimed as coverage until the span is re-established).
CREATE TABLE IF NOT EXISTS candle_sync (
    asset_id   TEXT        NOT NULL,
    timeframe  TEXT        NOT NULL,
    provider   TEXT        NOT NULL,
    first_bar  TIMESTAMPTZ NOT NULL,
    last_bar   TIMESTAMPTZ NOT NULL,
    provider_floor TIMESTAMPTZ,                   -- earliest bar the provider can supply (e.g. Tiingo 2020-01-01)
    synced_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, timeframe)
);
