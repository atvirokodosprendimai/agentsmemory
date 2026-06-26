-- +goose Up
-- +goose StatementBegin

-- Schema for the SaaS relational source-of-truth. Qdrant holds vectors and is
-- rebuildable from these rows; everything tenant/auth/skill related lives here.
-- SQLite (no-cgo) for now; the DDL is kept portable so a later Postgres move is
-- a driver swap, not a rewrite. Goose owns the schema — gorm never AutoMigrates.

-- A team is the unit of tenancy. Every drawer, wing and skill is owned by a
-- team, and a team maps 1:1 to a dedicated Qdrant collection (collection-per-
-- tenant isolation, decided 2026-06-26).
CREATE TABLE teams (
    id         TEXT PRIMARY KEY,            -- UUID
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,        -- url-safe handle
    created_at TEXT NOT NULL                -- RFC3339
);

-- A human account. Authenticates to the web dashboard (goth) to mint and manage
-- the API keys their agents use. Users join teams via memberships.
CREATE TABLE users (
    id            TEXT PRIMARY KEY,         -- UUID
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL DEFAULT '', -- bcrypt; empty when only OAuth-linked
    display_name  TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

-- Membership ties a user to a team with a role. Role gates write access to
-- shared resources (e.g. only writer/admin may update a centralised skill).
CREATE TABLE memberships (
    id         TEXT PRIMARY KEY,            -- UUID
    team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member', -- member | writer | admin
    created_at TEXT NOT NULL,
    UNIQUE (team_id, user_id)
);

-- An API key is the bearer credential an agent presents to the remote MCP
-- server. We store only a SHA-256 hash of the token (never the plaintext); the
-- token's team_id IS the tenant the MCP session operates within. prefix is the
-- short non-secret head shown in the dashboard so a user can recognise a key.
CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY,          -- UUID
    team_id      TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',  -- human label, e.g. "ci-agent"
    prefix       TEXT NOT NULL,             -- non-secret first chars of the token
    token_hash   TEXT NOT NULL UNIQUE,      -- sha256(token) hex
    created_at   TEXT NOT NULL,
    last_used_at TEXT,                       -- null until first use
    revoked_at   TEXT                        -- null = active
);
CREATE INDEX idx_api_keys_team ON api_keys(team_id);

-- A skill is a centralised, versioned, team-shared authored artifact (a SKILL.md
-- body + metadata) that a team's agents pull via the load_skill MCP tool. It is
-- mutable CRUD, NOT an append-only memory drawer — hence a relational table.
-- name is unique within a team so load_skill(name) is unambiguous.
CREATE TABLE skills (
    id          TEXT PRIMARY KEY,           -- UUID
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,              -- slug, unique per team
    description TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',   -- the skill body served to agents
    version     INTEGER NOT NULL DEFAULT 1, -- bumped on every update_skill
    updated_by  TEXT NOT NULL DEFAULT '',   -- user id of last editor
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (team_id, name)
);
CREATE INDEX idx_skills_team ON skills(team_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS skills;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS memberships;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS teams;
-- +goose StatementEnd
