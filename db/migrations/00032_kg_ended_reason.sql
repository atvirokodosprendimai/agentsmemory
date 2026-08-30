-- +goose Up

-- ADR-038 T4: an invalidation records WHY, not only that something ended.
--
-- 00010 gave a fact valid_from/valid_to and no column for a reason, so the half
-- of the store that keeps its history kept only THAT a fact ended. That is the
-- rediscovery tax wearing a different hat: a session finding an ended record with
-- no reason is in the same position as one that finds nothing — it re-derives,
-- reaches the same idea, and re-litigates a decision the team already took.
--
-- An invalidation is not an absence. It is a decision.
ALTER TABLE kg_triples ADD COLUMN ended_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE kg_triples DROP COLUMN ended_reason;
