-- ADR-038 T5. An index for the question every default read route now asks:
-- "what did this record replace?"
--
-- 00030 added superseded_by without one, which was right at the time — it was
-- written by an ending and read by nobody. T5 makes it a per-page key: every
-- search, list and get resolves the predecessors of the records it is about to
-- return, so an unindexed scan of the drawers table would ride on the recall path
-- of every recall.
--
-- Partial on `superseded_by != ''`, matching idx_drawers_current's shape and for
-- the same reason: the column is empty on nearly every row (only a superseded
-- record carries one), so a full index would be almost entirely dead entries.
-- The comparison is exact against the empty-string sentinel this schema uses in
-- place of NULL, so no temporal comparison enters the predicate.

-- +goose Up
CREATE INDEX IF NOT EXISTS idx_drawers_superseded_by
    ON drawers (team_id, superseded_by)
    WHERE superseded_by != '';

-- +goose Down
DROP INDEX IF EXISTS idx_drawers_superseded_by;
