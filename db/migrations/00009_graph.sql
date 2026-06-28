-- 00009_graph.sql
-- The navigable graph: hallways (within-wing entity co-occurrence links) and
-- tunnels (cross-wing links). The frozen palace kept these in sibling JSON files
-- (hallways.json / tunnels.json); a multi-tenant server cannot — they become
-- team-scoped tables, the relational source of truth the graph tools read and
-- recompute_graph rebuilds. Both carry the L7 "living connection" fields
-- (strength/stability/last_activated/access_count) for wire-shape parity with the
-- frozen tools; the Hebbian/Ebbinghaus evolution of those fields is a later phase,
-- so they are stored at their defaults for now.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE hallways (
    team_id          TEXT NOT NULL,
    id               TEXT NOT NULL,
    wing             TEXT NOT NULL,
    entity_a         TEXT NOT NULL,
    entity_b         TEXT NOT NULL,
    co_occurrence    INTEGER NOT NULL,
    rooms            TEXT NOT NULL DEFAULT '', -- semicolon-joined rooms the pair met in
    label            TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    created_by       TEXT NOT NULL DEFAULT 'auto',
    strength         REAL NOT NULL DEFAULT 1.0,
    stability        REAL NOT NULL DEFAULT 1.0,
    last_activated   TEXT NOT NULL DEFAULT '',
    access_count     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (team_id, id)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_hallways_team_wing ON hallways (team_id, wing);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE tunnels (
    team_id          TEXT NOT NULL,
    id               TEXT NOT NULL,
    source_wing      TEXT NOT NULL,
    source_room      TEXT NOT NULL,
    source_drawer_id TEXT NOT NULL DEFAULT '',
    target_wing      TEXT NOT NULL,
    target_room      TEXT NOT NULL,
    target_drawer_id TEXT NOT NULL DEFAULT '',
    label            TEXT NOT NULL DEFAULT '',
    kind             TEXT NOT NULL DEFAULT 'explicit', -- explicit | topic | entity
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL DEFAULT '',
    strength         REAL NOT NULL DEFAULT 1.0,
    stability        REAL NOT NULL DEFAULT 1.0,
    last_activated   TEXT NOT NULL DEFAULT '',
    access_count     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (team_id, id)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_tunnels_team_source ON tunnels (team_id, source_wing);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_tunnels_team_target ON tunnels (team_id, target_wing);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_tunnels_team_target;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX idx_tunnels_team_source;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE tunnels;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX idx_hallways_team_wing;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE hallways;
-- +goose StatementEnd
