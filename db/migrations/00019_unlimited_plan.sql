-- +goose Up

-- The Unlimited tier: an operator-granted plan with no monthly request cap, for
-- comped or internal workspaces that must never hit the meter. A cap of -1 is the
-- sentinel the metering path already honours — migration 00003 reserved exactly
-- this meaning ("-1 would mean unlimited"), usage.Allow enforces only when the cap
-- is > 0, and the dashboard renders a cap <= 0 as ∞ — so this row activates plumbing
-- that already exists rather than adding new behaviour.
--
-- It is deliberately NOT part of the sold ladder (decision 2026-06-29: "Free + Pro
-- only"): price_cents is 0, it carries no provider price id, and the pricing page's
-- catalog is hardcoded (Free + Pro), so it never appears to customers. The only way
-- onto it is the `set-plan` superadmin CLI, which possesses direct database access —
-- the same trust model as the `share` command. kind 'enterprise' marks it as the
-- elevated internal tier; currency 'eur' matches the rest of the catalog. The fixed
-- timestamp keeps the seed deterministic (migrations have no wall clock).
INSERT INTO plans (id, code, kind, name, price_cents, currency, monthly_request_cap, billing_interval, created_at) VALUES
    ('plan_unlimited', 'unlimited', 'enterprise', 'Unlimited', 0, 'eur', -1, 'month', '1970-01-01T00:00:00Z');

-- +goose Down

-- Move any workspace granted Unlimited back to the free plan first so the
-- teams -> plans foreign key never dangles, then drop the catalog row (mirrors
-- 00016's enterprise retirement). On a fresh DB nothing references it, so this is
-- a no-op there.
UPDATE teams SET plan_id = 'plan_personal' WHERE plan_id = 'plan_unlimited';
DELETE FROM plans WHERE code = 'unlimited';
