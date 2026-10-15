-- Strategy graphs: named containers + immutable revisions of the declarative
-- node/edge definition. Backtest runs reference a graph revision through the
-- strategies row keyed `graph:<graph_uuid>:v<revision>` (see service layer),
-- which keeps the existing strategies FK and provenance chain intact.
CREATE TABLE IF NOT EXISTS strategy_graphs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'active',     -- active | archived
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS strategy_graph_revisions (
    graph_id        UUID NOT NULL REFERENCES strategy_graphs(id) ON DELETE CASCADE,
    revision        INT NOT NULL,
    definition      JSONB NOT NULL,                 -- {nodes:[], edges:[]}
    definition_hash TEXT NOT NULL,                  -- sha256 of canonical definition
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (graph_id, revision)
);

CREATE INDEX IF NOT EXISTS idx_graph_rev_hash ON strategy_graph_revisions (definition_hash);
