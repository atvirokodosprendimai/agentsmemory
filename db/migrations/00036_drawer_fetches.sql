-- +goose Up

-- One row per drawer a caller fetched while naming the recall that sent it there.
--
-- ADR-028 T3. `search_events` already records that a recall HAPPENED and how many
-- hits it returned; it records no drawer identity at all, so the palace has never
-- been able to answer "was anything on that page actually read". A fetch that
-- names a search_id is the closest thing to a relevance click this system can
-- observe, and it is the only usage signal that grows with usage rather than with
-- a labelling budget.
--
-- search_id is NOT a foreign key on purpose. It is whatever a client sent, it is
-- validated for SHAPE before it reaches here (palace.ValidSearchID), and a row
-- naming a recall this server never logged is itself the finding — SkipTelemetry
-- means some recalls write no search_events row at all, so a constraint would
-- silently drop exactly the fetches that came from an unlogged recall.
CREATE TABLE drawer_fetches (
    id         TEXT PRIMARY KEY,
    team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    search_id  TEXT NOT NULL,           -- the recall the caller says sent it here
    drawer_id  TEXT NOT NULL,           -- the drawer actually returned, never the one requested
    whole      INTEGER NOT NULL DEFAULT 0, -- 1 when the caller asked for every chunk
    created_at TEXT NOT NULL
);

-- Reads are "this team, this window" for the report, and "this recall" for the
-- join. Two indexes because those are two different access patterns, not one.
CREATE INDEX idx_drawer_fetches_team_time ON drawer_fetches(team_id, created_at);
CREATE INDEX idx_drawer_fetches_search ON drawer_fetches(team_id, search_id);

-- +goose Down
DROP INDEX IF EXISTS idx_drawer_fetches_search;
DROP INDEX IF EXISTS idx_drawer_fetches_team_time;
DROP TABLE drawer_fetches;
