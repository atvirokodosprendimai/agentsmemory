-- +goose Up
-- profile_id records WHICH RANKING produced a recall: the same string
-- am_status publishes and the `am.profile_id` span attribute carries, e.g.
-- "fusion=rrf lex-weight=n/a lex-norm=n/a closet-boost=0.00 rerank=on(...)".
--
-- ADR-028 T3 landed the fetch join and deliberately published two RAW counts
-- rather than a rate, because "38% of recalls were followed by a fetch" is
-- uninterpretable without knowing which ranking produced them — a change to the
-- blend moves the number, and averaged across a knob change the figure describes
-- no configuration anyone ran. This column is what makes the ratio quotable, and
-- ADR-007 is the rule it implements: no number without its population.
--
-- ⚠ ADDITIVE AND NULLABLE, WITH NO DEFAULT, WHICH IS THE WHOLE DESIGN. Every row
-- written before this migration carries NULL, and NULL here means "taken under a
-- ranking nobody recorded" — not "taken under the empty profile". A DEFAULT ''
-- (the shape 00037 correctly used for origin, where an empty origin has a real
-- meaning) would be wrong here: it would fold every historical recall into one
-- phantom group and publish a rate for a configuration that never existed. The
-- aggregate EXCLUDES NULL instead, so the report covers post-deploy rows only
-- and says so by omission rather than by inventing a population.
ALTER TABLE search_events ADD COLUMN profile_id TEXT;

-- +goose Down
ALTER TABLE search_events DROP COLUMN profile_id;
