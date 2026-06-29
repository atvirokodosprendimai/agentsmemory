-- +goose Up

-- Billing lands: the catalog moves from the seed's USD "Personal / Enterprise"
-- pair to the product's real ladder — a free entry tier plus a single paid "Pro"
-- tier sold in two billing intervals (monthly and annual, the annual priced at
-- ten months for twelve = two months free). Currency switches to EUR to match
-- the pricing the product actually charges.

-- price_cents now means "amount charged per billing_interval" (not always
-- monthly): Pro Annual stores the once-a-year charge. billing_interval records
-- which it is so the dashboard can render "/ month" vs "/ year" correctly and
-- the Stripe price is unambiguous.
ALTER TABLE plans ADD COLUMN billing_interval TEXT NOT NULL DEFAULT 'month';

-- The free tier keeps its id and cap; only its currency moves to EUR so the whole
-- catalog is one currency.
UPDATE plans SET currency = 'eur' WHERE code = 'personal';

-- Retire the legacy Enterprise tier (decision 2026-06-29: "Free + Pro only").
-- Reassign any workspace that was on it back to the free plan first so the
-- plans FK never dangles, then drop the row. On a fresh DB nothing references it
-- (seedIfEmpty places new workspaces on plan_personal), so this is a no-op there.
UPDATE teams SET plan_id = 'plan_personal' WHERE plan_id = 'plan_enterprise';
DELETE FROM plans WHERE code = 'enterprise';

-- The paid tier, as two catalog rows (decision 2026-06-29: "two plan rows").
-- Both carry the same generous monthly request cap; they differ only in price
-- and billing interval. Stripe price ids are environment-specific (test vs live)
-- so they are NOT stored here — billing resolves them from config by plan code.
-- Fixed timestamp keeps the seed deterministic (migrations have no wall clock).
INSERT INTO plans (id, code, kind, name, price_cents, currency, monthly_request_cap, billing_interval, created_at) VALUES
    ('plan_pro_monthly', 'pro_monthly', 'personal', 'Pro', 5000,  'eur', 1000000, 'month', '1970-01-01T00:00:00Z'),
    ('plan_pro_annual',  'pro_annual',  'personal', 'Pro', 50000, 'eur', 1000000, 'year',  '1970-01-01T00:00:00Z');

-- A subscription is the durable record of a workspace's relationship with Stripe.
-- teams.plan_id stays the *effective* plan the metering path reads (PlanForTeam /
-- MonthlyCap are untouched); this table records who is paying, on what Stripe
-- objects, and the current billing period — the audit + lifecycle source the
-- webhook upserts on. team_id is unique: a workspace has at most one live
-- subscription at a time (a new checkout updates the same row). status mirrors
-- Stripe (active | past_due | canceled | incomplete). The evolution note from the
-- scaffold ("a subscriptions table is the right next step when billing lands")
-- realised here.
CREATE TABLE subscriptions (
    id                     TEXT PRIMARY KEY,                 -- UUID
    team_id                TEXT NOT NULL UNIQUE REFERENCES teams(id) ON DELETE CASCADE,
    plan_id                TEXT NOT NULL REFERENCES plans(id),
    status                 TEXT NOT NULL,                    -- active|past_due|canceled|incomplete
    stripe_customer_id     TEXT NOT NULL DEFAULT '',
    stripe_subscription_id TEXT NOT NULL DEFAULT '',
    current_period_end     TEXT NOT NULL DEFAULT '',         -- RFC3339; end of the paid window
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL
);

-- Webhook handling looks subscriptions up by the Stripe subscription id (the
-- stable key across lifecycle events), so index it. Partial-ish: the empty
-- default rows (pre-Stripe) collide on '' but are never queried by it.
CREATE INDEX idx_subscriptions_stripe_sub ON subscriptions(stripe_subscription_id);

-- +goose Down
DROP INDEX idx_subscriptions_stripe_sub;
DROP TABLE subscriptions;
DELETE FROM plans WHERE code IN ('pro_monthly', 'pro_annual');
UPDATE plans SET currency = 'usd' WHERE code = 'personal';
INSERT INTO plans (id, code, kind, name, price_cents, currency, monthly_request_cap, created_at) VALUES
    ('plan_enterprise', 'enterprise', 'enterprise', 'Enterprise', 5000, 'usd', 1000000, '1970-01-01T00:00:00Z');
ALTER TABLE plans DROP COLUMN billing_interval;
