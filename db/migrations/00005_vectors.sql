-- 00005_vectors.sql
-- The vector source-of-truth table. SQLite durably holds every embedding and its
-- payload (decision 2026-06-26: "sqlite as source of truth"); the Qdrant search
-- index is derived from this and is rebuildable, so this table — not Qdrant — is
-- what must never be lost. One shared table partitioned by namespace (team ID)
-- keeps tenants isolated by row while staying a single, litestream-backupable file.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE vectors (
    namespace TEXT    NOT NULL,            -- per-tenant partition (team ID)
    id        TEXT    NOT NULL,            -- caller's stable point ID (drawer ID)
    dim       INTEGER NOT NULL,            -- vector dimension (e.g. 1024 for bge-m3)
    vector    BLOB    NOT NULL,            -- float32 values, little-endian
    payload   TEXT    NOT NULL DEFAULT '{}', -- opaque JSON metadata
    PRIMARY KEY (namespace, id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE vectors;
-- +goose StatementEnd
