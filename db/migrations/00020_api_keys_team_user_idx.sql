-- 00020_api_keys_team_user_idx.sql
-- Web team/member management makes API keys per-MEMBER, not per-workspace: each
-- member has their own key, revealed/rotated per (team, user), and revoked when
-- the member is removed. The schema already carried api_keys.user_id (00001), so
-- no column changes are needed — only an access path. Every per-member operation
-- (reveal newest active key for a user, rotate that user's own key, revoke a
-- removed member's keys) filters on (team_id, user_id); the existing
-- idx_api_keys_team covers team_id alone, leaving a user_id scan on top. This
-- composite index makes those lookups a direct hit. It is additive and
-- non-destructive: no data moves, keys keep working across the migration.

-- +goose Up
CREATE INDEX idx_api_keys_team_user ON api_keys(team_id, user_id);

-- +goose Down
DROP INDEX idx_api_keys_team_user;
