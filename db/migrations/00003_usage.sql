-- +goose Up

-- Each plan carries a monthly request cap. The Free plan (what a "free project"
-- subscribes to) allows 10,000 metered requests per calendar month; Enterprise
-- gets a much higher ceiling. -1 would mean unlimited (not used yet).
ALTER TABLE plans ADD COLUMN monthly_request_cap INTEGER NOT NULL DEFAULT 0;

-- Relabel the seeded personal plan as the user-facing "Free" plan and set caps.
UPDATE plans SET name = 'Free',       monthly_request_cap = 10000   WHERE code = 'personal';
UPDATE plans SET                       monthly_request_cap = 1000000 WHERE code = 'enterprise';

-- Per-workspace, per-month request counter. period is 'YYYY-MM' (calendar month
-- in UTC); count is the number of metered MCP requests in that window. The
-- composite primary key makes the upsert ("increment this month") trivial and
-- race-light, and keeps history per month for later billing/analytics.
CREATE TABLE usage_counters (
    team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    period     TEXT NOT NULL,             -- 'YYYY-MM' UTC
    count      INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (team_id, period)
);

-- +goose Down
DROP TABLE usage_counters;
ALTER TABLE plans DROP COLUMN monthly_request_cap;
UPDATE plans SET name = 'Personal' WHERE code = 'personal';
