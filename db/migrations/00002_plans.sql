-- +goose Up

-- A plan is a purchasable tier with a price. A workspace subscribes to exactly
-- one. Personal-use plans and enterprise plans differ in kind and price, which
-- is how one user can run a couple of cheap personal workspaces alongside one
-- or more pricier enterprise workspaces — each its own isolated tenant.
CREATE TABLE plans (
    id          TEXT PRIMARY KEY,            -- UUID or stable seed id
    code        TEXT NOT NULL UNIQUE,        -- stable slug, e.g. personal, enterprise
    kind        TEXT NOT NULL,               -- personal | enterprise
    name        TEXT NOT NULL,
    price_cents INTEGER NOT NULL DEFAULT 0,  -- monthly price; 0 = free
    currency    TEXT NOT NULL DEFAULT 'usd',
    created_at  TEXT NOT NULL
);

-- A team row IS a workspace = the unit of tenancy and the unit of billing.
-- kind separates a single-user personal workspace from a shared enterprise one;
-- plan_id is its current subscription (nullable so the column can be added to
-- existing rows, backfilled by the app). One workspace = one Qdrant collection.
ALTER TABLE teams ADD COLUMN kind TEXT NOT NULL DEFAULT 'personal';
ALTER TABLE teams ADD COLUMN plan_id TEXT REFERENCES plans(id);

-- Reference plans so a fresh database can place a workspace on a plan without
-- the app first having to create the catalog. Fixed timestamp keeps the seed
-- deterministic (migrations run with no wall-clock dependency).
INSERT INTO plans (id, code, kind, name, price_cents, currency, created_at) VALUES
    ('plan_personal',   'personal',   'personal',   'Personal',   0,    'usd', '1970-01-01T00:00:00Z'),
    ('plan_enterprise', 'enterprise', 'enterprise', 'Enterprise', 5000, 'usd', '1970-01-01T00:00:00Z');

-- +goose Down
ALTER TABLE teams DROP COLUMN plan_id;
ALTER TABLE teams DROP COLUMN kind;
DROP TABLE plans;
