-- 00008_closets.sql
-- Closets: the searchable topic/quote pointer index the miner builds over drawers.
-- A closet row holds a packed set of pointer lines (the `document`) that is
-- embedded and searched alongside drawers; a closet hit boosts the rank of drawers
-- from the same source_file. Closets are DERIVED — purged and rebuilt whenever
-- their source is re-mined — so this table is disposable relative to `drawers`.
-- Vectors live in the store seam under a per-team closet namespace; this table is
-- the relational source of truth and the closet-id -> source_file map search needs
-- for the boost. The (team_id, source_file) index backs the re-mine purge.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE closets (
    team_id     TEXT NOT NULL,
    id          TEXT NOT NULL,
    wing        TEXT NOT NULL,
    room        TEXT NOT NULL,
    source_file TEXT NOT NULL,
    document    TEXT NOT NULL,
    entities    TEXT NOT NULL DEFAULT '',
    filed_at    TEXT NOT NULL,
    PRIMARY KEY (team_id, id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_closets_team_source ON closets (team_id, source_file);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_closets_team_source;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE closets;
-- +goose StatementEnd
