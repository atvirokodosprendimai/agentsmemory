-- ADR-042 T2. Record that a workspace asked to buy a plan, so a contribution that
-- lands later can be attributed to it.
--
-- OpenCollective's checkout is a static hosted contribution page and it sends no
-- signed webhook, so nothing carries our workspace id through the payment the way
-- Stripe's client_reference_id does. Reconciliation reads orders back from the
-- GraphQL API and has to answer "who paid this?" from what the order carries: a
-- `tags` value we put on the checkout URL, or the contributor's email. Both are
-- matched against this table, and a tag with no matching row is NOT attribution —
-- the row is what stops a forged tag crediting someone else's workspace.
--
-- Deliberately not a subscription: this records an INTENTION, which may never be
-- paid, may be abandoned, or may be repeated. The durable payment relationship
-- still lives in `subscriptions`, written only by the plan-flip path.
--
-- Rows are small and cheap to keep; no expiry is defined here because a
-- contribution can arrive days after the click, and reconciliation matches on the
-- most recent intent for a (tag, plan) pair.

-- +goose Up
CREATE TABLE IF NOT EXISTS billing_checkout_intents (
    id         TEXT PRIMARY KEY,
    team_id    TEXT NOT NULL,
    plan_code  TEXT NOT NULL,
    tag        TEXT NOT NULL,
    email      TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- The two attribution lookups reconciliation performs, both scoped by plan_code so
-- a monthly click cannot claim an annual contribution. Partial on a non-empty key:
-- an order arriving with no tag (or no readable email) must match NOTHING rather
-- than colliding with every row whose column defaults to ''.
CREATE INDEX IF NOT EXISTS idx_checkout_intents_tag
    ON billing_checkout_intents (tag, plan_code)
    WHERE tag != '';

CREATE INDEX IF NOT EXISTS idx_checkout_intents_email
    ON billing_checkout_intents (email, plan_code)
    WHERE email != '';

-- +goose Down
DROP INDEX IF EXISTS idx_checkout_intents_email;
DROP INDEX IF EXISTS idx_checkout_intents_tag;
DROP TABLE IF EXISTS billing_checkout_intents;
