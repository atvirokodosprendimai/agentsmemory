-- 00007_diary.sql
-- Diary support. A diary entry is just a drawer in the `diary` room, carrying two
-- extra metadata fields the frozen Python palace stores on diary entries:
--   agent — whose journal this is (diary_read scopes by it; stored lowercased)
--   topic — a free tag grouping entries (default "general")
-- Normal (non-diary) drawers leave both empty. Adding columns keeps diary on the
-- same chunk/embed/store machinery as add_drawer rather than forking a parallel
-- store. The (team, room, agent) index is what diary_read filters on.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE drawers ADD COLUMN agent TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE drawers ADD COLUMN topic TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_drawers_team_room_agent ON drawers (team_id, room, agent);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_drawers_team_room_agent;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE drawers DROP COLUMN topic;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE drawers DROP COLUMN agent;
-- +goose StatementEnd
