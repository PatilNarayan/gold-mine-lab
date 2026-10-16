CREATE TABLE research_studies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'frozen', 'exploratory_complete', 'holdout_consumed')),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    hypothesis TEXT NOT NULL,
    asset_id TEXT NOT NULL REFERENCES assets(id),
    timeframe TEXT NOT NULL,
    dataset_fingerprint TEXT NOT NULL,
    dev_start DATE NOT NULL,
    dev_end DATE NOT NULL,
    holdout_start DATE NOT NULL,
    holdout_end DATE NOT NULL,
    spec JSONB NOT NULL,
    spec_hash TEXT NOT NULL,
    research_counts JSONB,
    executions JSONB NOT NULL DEFAULT '[]'::jsonb,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    frozen_at TIMESTAMPTZ,
    exploratory_completed_at TIMESTAMPTZ,
    holdout_consumed_at TIMESTAMPTZ,
    CHECK (dev_start <= dev_end),
    CHECK (holdout_start <= holdout_end),
    CHECK (holdout_start > dev_end)
);

CREATE INDEX research_studies_reservations_idx
    ON research_studies (asset_id, timeframe, status);
