-- 00014_share_requests.sql
-- GUI wing-share handshake. The CLI `agentsmemory share` copies a wing across
-- tenants immediately, but exposing that over HTTP would let any caller push
-- memory into a workspace by guessing its slug. So the dashboard path is a
-- two-step handshake instead: the source side files a PENDING request naming the
-- destination (by slug, resolved to to_team_id) and the wing, and the
-- destination's admin must ACCEPT before any copy runs. This table is that
-- queue — one row per request, carrying who asked and how it resolved.
--
-- status: 'pending' (awaiting the destination admin), 'accepted' (the copy ran),
-- or 'declined'. resolved_at/resolved_by are NULL while pending and stamped when
-- an admin acts. The copy itself reuses palace.CopyWing, so nothing about the
-- copied data lives here — only the consent record.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE share_requests (
    id           TEXT PRIMARY KEY,
    from_team_id TEXT NOT NULL,            -- source workspace (the wing's current home)
    to_team_id   TEXT NOT NULL,            -- destination workspace (resolved from the typed slug)
    wing         TEXT NOT NULL,            -- the wing to copy
    requested_by TEXT NOT NULL,            -- user id who initiated the push
    status       TEXT NOT NULL DEFAULT 'pending',
    created_at   TEXT NOT NULL,
    resolved_at  TEXT,                     -- when an admin accepted/declined (NULL while pending)
    resolved_by  TEXT                      -- user id who accepted/declined (NULL while pending)
);
-- +goose StatementEnd

-- Inbox query: "what is pending for this destination?" rides this index.
-- +goose StatementBegin
CREATE INDEX idx_share_requests_to_status ON share_requests (to_team_id, status);
-- +goose StatementEnd

-- A given wing should have at most ONE open request per (source -> destination)
-- pair, so a double-submit or an impatient re-click reuses the pending row rather
-- than stacking duplicates. The partial index lets accepted/declined history
-- accumulate freely while keeping pending unique.
-- +goose StatementBegin
CREATE UNIQUE INDEX ux_share_requests_pending
    ON share_requests (from_team_id, to_team_id, wing)
    WHERE status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX ux_share_requests_pending;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_share_requests_to_status;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE share_requests;
-- +goose StatementEnd
