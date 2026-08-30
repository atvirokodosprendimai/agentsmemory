-- +goose Up

-- derived separates an edge the SERVER inferred from one a writer authored.
--
-- ADR-036 T6 makes every newly filed drawer reachable by traversal, because
-- today almost none are: measured 2026-08-26 against the live palace, 57 of
-- 1,985 drawers carry any edge at all (2.9%), and **0 drawers are named as a
-- triple OBJECT** — so the pointer pattern the whole taxonomy rests on has zero
-- adoption in the workspace that invented it.
--
-- The fix attaches an edge at write time. That is useful and it is also an
-- opinion: the server chooses a subject and a predicate the writer did not, and
-- the derived hallway graph has so far produced nothing, so there is real reason
-- to doubt the inference is good. A marker is what keeps that measurable and
-- reversible — without it, derived noise and authored intent are one population
-- and neither can be counted or removed.
--
-- Nullable with no default on purpose. Every row that predates this migration is
-- NULL, which is honestly "nobody recorded whether this was derived" rather than
-- the false claim "authored" that a DEFAULT 0 would write across 196 existing
-- triples. An absent answer and a negative answer are different facts, and this
-- repository has already paid for conflating them once.
ALTER TABLE kg_triples ADD COLUMN derived INTEGER;

-- +goose Down
ALTER TABLE kg_triples DROP COLUMN derived;
