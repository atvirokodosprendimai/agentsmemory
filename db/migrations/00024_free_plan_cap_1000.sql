-- +goose Up

-- RENUMBERED 00021 -> 00024 on 2026-08-23. This file was authored on 2026-07-03
-- and never merged; while it sat here, main gave version 00021 to a DIFFERENT
-- migration (00021_search_events.sql, 2026-08-18). Two files sharing a version
-- is not a merge conflict -- the filenames differ, so git merges them cleanly --
-- and goose does not tolerate the result: migrate.go PANICS with "duplicate
-- version 21 detected", which means merging this branch would have taken the
-- server down at startup rather than failing anywhere a test would look.
--
-- 00022 and 00023 are also taken on main now, so this claims 00024. Renumbering
-- is safe precisely because this migration has never been applied anywhere: no
-- database carries a goose row for it under either number.

-- Lower the Free plan's monthly request cap from 10,000 (set in 00003) to 1,000.
-- Product decision: the free tier is a trial-sized allowance, not a production
-- quota — 1,000 metered requests/month is enough to evaluate agent memory, and
-- teams running agents in production upgrade to Pro. Migrations are append-only,
-- so rather than rewrite 00003 this migration adjusts the same column: a fresh
-- database applies 00003 (10,000) then this (1,000) and converges to 1,000, and
-- an already-migrated database is brought to the new value in place. Only the
-- Free plan is touched (code = 'personal'); Pro (1,000,000) and the operator-only
-- Unlimited tier (-1) keep their caps.
UPDATE plans SET monthly_request_cap = 1000 WHERE code = 'personal';

-- +goose Down

-- Restore the original 10,000 cap so Up -> Down returns 00003's state exactly
-- (migrations_test cycles Up -> DownTo(0) -> Up and expects reversibility).
UPDATE plans SET monthly_request_cap = 10000 WHERE code = 'personal';
