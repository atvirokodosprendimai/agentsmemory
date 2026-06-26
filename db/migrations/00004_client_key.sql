-- +goose Up

-- An API key gains a public client identifier for the OAuth 2.1 flow that
-- claude.ai's remote connector requires. client_key (e.g. "mck_<hex>") is the
-- OAuth client_id a user pastes into the connector; the existing secret token
-- (token_hash) is the client_secret. The key is public and travels in the
-- /authorize URL; the secret is verified only at the /token endpoint (POST).
ALTER TABLE api_keys ADD COLUMN client_key TEXT NOT NULL DEFAULT '';

-- Unique among real client keys, but allow many legacy/blank rows (a partial
-- index ignores the empty default so pre-OAuth keys don't collide).
CREATE UNIQUE INDEX idx_api_keys_client_key ON api_keys(client_key) WHERE client_key <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_api_keys_client_key;
ALTER TABLE api_keys DROP COLUMN client_key;
