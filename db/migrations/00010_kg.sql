-- 00010_kg.sql
-- The temporal knowledge graph: typed subject -> predicate -> object facts, each
-- with a validity window. A fact is CURRENT while its valid_to is empty; setting
-- valid_to ends it (it becomes historical but is never deleted). Queries can ask
-- "as of" a point in time. This is pure relational state — no embeddings — kept in
-- its own team-scoped tables, separate from drawers. Ported from the frozen
-- knowledge_graph.py (entities + triples). Empty-string stands in for the frozen
-- NULL on the temporal columns so a Go string column never has to scan NULL.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE kg_entities (
    team_id    TEXT NOT NULL,
    id         TEXT NOT NULL, -- normalized: lowercased, spaces -> underscores, apostrophes dropped
    name       TEXT NOT NULL, -- the original display name as first seen
    created_at TEXT NOT NULL,
    PRIMARY KEY (team_id, id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE kg_triples (
    team_id          TEXT NOT NULL,
    id               TEXT NOT NULL,
    subject          TEXT NOT NULL, -- kg_entities.id
    predicate        TEXT NOT NULL,
    object           TEXT NOT NULL, -- kg_entities.id
    valid_from       TEXT NOT NULL DEFAULT '', -- '' = always-true from the beginning
    valid_to         TEXT NOT NULL DEFAULT '', -- '' = currently true (not yet ended)
    confidence       REAL NOT NULL DEFAULT 1.0,
    source_closet    TEXT NOT NULL DEFAULT '',
    source_file      TEXT NOT NULL DEFAULT '',
    source_drawer_id TEXT NOT NULL DEFAULT '',
    extracted_at     TEXT NOT NULL,
    PRIMARY KEY (team_id, id)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_kg_triples_team_subject ON kg_triples (team_id, subject);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_kg_triples_team_object ON kg_triples (team_id, object);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_kg_triples_team_predicate ON kg_triples (team_id, predicate);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_kg_triples_team_predicate;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX idx_kg_triples_team_object;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX idx_kg_triples_team_subject;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE kg_triples;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE kg_entities;
-- +goose StatementEnd
