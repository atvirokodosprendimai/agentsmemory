-- 00012_skillset.sql
-- The global "wakeup" skillset: a single, platform-owned document that teaches an
-- agent how to drive the am_* tools — which to call, in what order, and which
-- centralised skills to load. It is the remote twin of a local /M-style bootstrap.
--
-- Unlike `skills` (00001 et al.), which is per-team and member-authored, this table
-- holds at MOST ONE row (id = 'global'): the platform has exactly one wakeup
-- playbook, identical for every tenant, editable only by a platform superadmin
-- (the SUPERADMIN_EMAILS allowlist — process config, not a row here). The
-- am_skillset MCP tool reads it; the dashboard superadmin editor writes it. The
-- singleton shape is why there is no team_id column and no per-tenant index.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE skillset (
    id         TEXT    NOT NULL PRIMARY KEY,  -- always 'global' (the singleton row)
    content    TEXT    NOT NULL,              -- the superadmin-authored wakeup playbook (verbatim)
    version    INTEGER NOT NULL DEFAULT 1,    -- bumped on every edit, like a skill version
    updated_by TEXT    NOT NULL DEFAULT '',   -- user id of the last editor (provenance)
    created_at TEXT    NOT NULL,              -- RFC3339 first-write time
    updated_at TEXT    NOT NULL               -- RFC3339 last-write time
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE skillset;
-- +goose StatementEnd
