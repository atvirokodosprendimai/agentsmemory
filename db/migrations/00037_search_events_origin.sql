-- +goose Up
-- origin records WHO issued the search: '' for a person or an agent acting on its
-- own judgement, `hook:<script>` for the kit's automatic recalls (ADR-054).
--
-- A hook's recall and a person's question share one door, and nothing here could
-- tell them apart — so am_recall_stats' to-write list carried branch names and
-- commit subjects nobody will ever search for. The origin is supplied by the
-- CALLER through the channel the wing already travels (a header the kit sets from
-- the hook's environment), never a tool argument an agent could forget.
--
-- Additive, NOT NULL DEFAULT '': every row written before this column reads as a
-- person's, which is the honest default — nothing rewrites history, and the
-- report's window is what an operator narrows to post-deploy rows.
ALTER TABLE search_events ADD COLUMN origin TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE search_events DROP COLUMN origin;
