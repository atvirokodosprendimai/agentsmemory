-- +goose Up

-- ADR-038: a memory is ENDED, not overwritten, and retraction is not erasure.
--
-- The temporal model already covers FACTS and not REASONING, which is backwards.
-- 00010_kg.sql says of a knowledge-graph fact that it "becomes historical but is
-- never deleted"; a drawer had no valid_to, no superseded_by and no revision
-- table, so `Repo.Update` was an in-place Updates() and the prior content was
-- gone the moment a correction landed. A fact — "service X deploys to host Y" —
-- is cheap to re-derive from the running system. Why an approach was abandoned,
-- what was tried first, what the constraint was: that exists nowhere else, and
-- it was the half the store let an agent overwrite or delete outright.
--
-- EMPTY valid_to MEANS CURRENT, and that is what makes this migration free:
-- every existing row is already correct, so there is no backfill and no window
-- during which the corpus is half-converted. It is also why the DEFAULT is ''
-- rather than NULL — 00010 made the same choice for kg_triples so a Go string
-- column never has to scan NULL, and using two sentinels for one concept across
-- two tables is how a temporal store becomes unreadable.
--
-- ended_reason is separate from valid_to on purpose. An invalidation that
-- records only THAT something ended destroys the only thing worth keeping about
-- the ending: a session finding an ended record with no reason is in the same
-- position as one that finds nothing — it re-derives, reaches the same idea, and
-- re-litigates a decision the team already took.
--
-- superseded_by carries the lineage a tombstone cannot: "Kafka until March, then
-- NATS, because rebalancing" needs the LINK, and a deleted_at has nowhere to put
-- it. It names the successor record, and is empty for a standalone retraction
-- that replaces nothing.
--
-- ⚠ This lands BEFORE the content-key migration deliberately (ADR-038 T1 before
-- T2). The content key's unique index is scoped to CURRENT rows —
-- `WHERE content_key != '' AND valid_to = ''` — because a superseded row that
-- keeps competing for uniqueness on content it no longer asserts would make text
-- that was once superseded impossible to file again. Creating that index needs
-- valid_to to already exist, so the ordering removes a window where the schema
-- would otherwise be briefly wrong.
ALTER TABLE drawers ADD COLUMN valid_to      TEXT NOT NULL DEFAULT '';
ALTER TABLE drawers ADD COLUMN superseded_by TEXT NOT NULL DEFAULT '';
ALTER TABLE drawers ADD COLUMN ended_reason  TEXT NOT NULL DEFAULT '';
ALTER TABLE drawers ADD COLUMN ended_at      TEXT NOT NULL DEFAULT '';

-- Recall filters on "is this current" on every default route once T5 lands, and
-- an empty-string comparison is exact rather than a range, so it is safe to
-- index — the same reasoning 00010 records for kg_triples' valid_to.
CREATE INDEX IF NOT EXISTS idx_drawers_current ON drawers (team_id, wing) WHERE valid_to = '';

-- +goose Down
DROP INDEX IF EXISTS idx_drawers_current;
ALTER TABLE drawers DROP COLUMN ended_at;
ALTER TABLE drawers DROP COLUMN ended_reason;
ALTER TABLE drawers DROP COLUMN superseded_by;
ALTER TABLE drawers DROP COLUMN valid_to;
