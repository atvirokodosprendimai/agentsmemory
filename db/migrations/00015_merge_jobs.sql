-- 00015_merge_jobs.sql
-- Background wing-merge queue. Merging a wing is two fast in-place UPDATEs
-- (relabel the `wing` of every drawer + closet — see palace.MergeWing), but the
-- derived-graph rebuild that must follow (recompute_graph) can be slow on a large
-- workspace. So the dashboard does not merge in the request: it enqueues a job
-- here and a background worker (like the embed worker) runs merge + graph rebuild
-- off the request path. This table is that durable queue — it survives restarts,
-- so a job mid-flight resumes rather than being lost.
--
-- status: 'queued' (waiting for the worker), 'running' (claimed), 'done' (merge +
-- graph rebuild finished), or 'failed' (error captured). sources is a JSON array
-- of source wing names folded into target. drawers/closets record what the merge
-- relabeled; error holds the failure message when status='failed'.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE merge_jobs (
    id           TEXT PRIMARY KEY,
    team_id      TEXT NOT NULL,            -- workspace the merge runs in
    sources      TEXT NOT NULL,            -- JSON array of source wing names
    target       TEXT NOT NULL,            -- wing they are folded into
    status       TEXT NOT NULL DEFAULT 'queued',
    requested_by TEXT NOT NULL,            -- user id who queued it
    drawers      INTEGER NOT NULL DEFAULT 0, -- drawers relabeled (set on done)
    closets      INTEGER NOT NULL DEFAULT 0, -- closets relabeled (set on done)
    error        TEXT NOT NULL DEFAULT '',   -- failure message when status='failed'
    created_at   TEXT NOT NULL,
    started_at   TEXT,                     -- when the worker claimed it
    finished_at  TEXT                      -- when it reached done/failed
);
-- +goose StatementEnd

-- Worker pickup: "oldest queued job" rides this index.
-- +goose StatementBegin
CREATE INDEX idx_merge_jobs_status_created ON merge_jobs (status, created_at);
-- +goose StatementEnd

-- Per-team recent-jobs list (the dashboard panel) rides this index.
-- +goose StatementBegin
CREATE INDEX idx_merge_jobs_team_created ON merge_jobs (team_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_merge_jobs_team_created;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_merge_jobs_status_created;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE merge_jobs;
-- +goose StatementEnd
