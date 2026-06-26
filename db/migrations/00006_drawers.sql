-- 00006_drawers.sql
-- The drawer metadata source-of-truth table. A drawer is one verbatim memory
-- chunk; this table holds everything ABOUT it (where it lives, what it is, when
-- it was filed) while its embedding lives in the generic `vectors` table (00005)
-- keyed by the same id. Splitting them keeps the vector seam domain-free: the
-- palace owns drawer semantics here, the store owns vectors there, and a search
-- joins the two by id. Like `vectors`, this is one shared table partitioned by
-- team_id so tenants are isolated by row in a single, backupable file.
--
-- Aggregations (list_wings / list_rooms / get_taxonomy) GROUP BY wing/room, so
-- those columns are indexed per tenant to keep them cheap — the same fix the
-- frozen Python palace needed when status aggregation went O(N^2) over Qdrant.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE drawers (
    team_id      TEXT    NOT NULL,            -- owning tenant (also the vector namespace)
    id           TEXT    NOT NULL,            -- deterministic hash(team,wing,room,source,chunk) — idempotency key
    wing         TEXT    NOT NULL,            -- project namespace
    room         TEXT    NOT NULL,            -- aspect within the wing
    source_file  TEXT    NOT NULL DEFAULT '', -- provenance of the chunk
    chunk_index  INTEGER NOT NULL DEFAULT 0,  -- position of this chunk within its source
    content      TEXT    NOT NULL,            -- verbatim text — the memory itself, never summarised
    entities     TEXT    NOT NULL DEFAULT '', -- semicolon-joined proper nouns (populated by mining later)
    parent_id    TEXT    NOT NULL DEFAULT '', -- links chunks of one oversized add_drawer back to the first
    filed_at     TEXT    NOT NULL,            -- RFC3339 ingestion time
    content_date TEXT    NOT NULL DEFAULT '', -- the date the memory is ABOUT (optional)
    PRIMARY KEY (team_id, id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_drawers_team_wing ON drawers (team_id, wing);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_drawers_team_room ON drawers (team_id, wing, room);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE drawers;
-- +goose StatementEnd
