-- +goose Up

-- ADR-038: refer by the id, dedupe on the content.
--
-- drawers.id is a SHA-256 of (team, wing, room, source_file, chunk_index,
-- content) AND it is what code_anchors, tunnels, kg_triples.source_drawer_id,
-- parent_id, search_events and every Qdrant point (a UUID5 of it) refer to. One
-- value, two jobs. Three shipped paths already mutate the hashed tuple in place
-- and keep the id — Service.Update, MergeWing, and WriteDiary's seeded mint — so
-- the store had already decided an id is a reference rather than a description.
-- It just never said so, and nothing could tell a real ending from a miss.
--
-- Measured 2026-08-27 by recomputing every id in the live palace: 1,705 non-diary
-- rows checked, 27 whose id no longer describes them.
--
-- content_key carries what the id used to promise. The id becomes opaque BY
-- CONTRACT: never recomputed, never compared to a hash, never used to infer
-- anything about a row's content. No existing id changes, so nothing is
-- re-pointed and no vector is re-keyed — which is why the rollback is a dropped
-- column rather than a cross-store repair.
ALTER TABLE drawers ADD COLUMN content_key TEXT NOT NULL DEFAULT '';

-- ⚠ BOTH CONJUNCTS OF THIS PREDICATE ARE LOAD-BEARING AND EACH FAILS DIFFERENTLY.
--
--   content_key != ''  Without it every keyless row shares ONE index entry, and
--                      once the upsert points here, filing any keyless drawer
--                      OVERWRITES an unrelated memory. The only failure in this
--                      decision that destroys rather than duplicates, and the
--                      only silent one. Diary rows carry an empty key on purpose
--                      — a journal must never dedupe — and this is what keeps
--                      them out.
--
--   valid_to = ''      Without it a SUPERSEDED row keeps competing for uniqueness
--                      on content it no longer asserts, so text that was once
--                      superseded could never be filed again. Neither half of
--                      ADR-038 could see this alone: the identity half does not
--                      know what "current" means, and the lineage half does not
--                      know there is an index. It is why they are one record.
--
-- This is also why 00030 (the validity window) lands FIRST: the predicate cannot
-- reference valid_to before the column exists, and creating the index narrow and
-- widening it a migration later would leave a window where the schema is wrong.
CREATE UNIQUE INDEX IF NOT EXISTS idx_drawers_content_key
    ON drawers (team_id, content_key)
    WHERE content_key != '' AND valid_to = '';

-- +goose Down
DROP INDEX IF EXISTS idx_drawers_content_key;
ALTER TABLE drawers DROP COLUMN content_key;
