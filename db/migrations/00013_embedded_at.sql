-- 00013_embedded_at.sql
-- Async embedding: a migration import now ABSORBS drawer/closet rows instantly
-- (text + metadata, no vector) and a background worker embeds them afterwards, so
-- a huge palace upload finishes in seconds instead of blocking on the embedder
-- per batch (and tripping the CDN read timeout). embedded_at is the durable queue:
-- NULL means "row exists, vector not built yet"; a timestamp means "indexed".
--
-- Existing rows were written by the synchronous path, so they already have a
-- vector — backfill embedded_at to filed_at so the worker never re-embeds them.
-- The partial indexes keep the worker's "what's still pending?" scan cheap even
-- when almost everything is already indexed.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE drawers ADD COLUMN embedded_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE closets ADD COLUMN embedded_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE drawers SET embedded_at = filed_at WHERE embedded_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE closets SET embedded_at = filed_at WHERE embedded_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_drawers_pending ON drawers (team_id) WHERE embedded_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_closets_pending ON closets (team_id) WHERE embedded_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_closets_pending;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_drawers_pending;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE closets DROP COLUMN embedded_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE drawers DROP COLUMN embedded_at;
-- +goose StatementEnd
