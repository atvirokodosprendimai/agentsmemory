-- 00011_api_key_token_enc.sql
-- Lets the dashboard re-show an API key after creation. Until now api_keys stored
-- only token_hash (a one-way SHA-256), so a token was shown exactly once and was
-- never retrievable. token_enc holds the SAME secret sealed with AES-256-GCM
-- under a server-held key (AGENTSMEMORY_TOKEN_KEY), so an authorized admin can
-- reveal it again while a database-only leak stays useless without the key.
-- token_hash remains the authentication path; token_enc is reveal-only.
-- Existing rows keep the empty default and read as "created before reveal" —
-- they cannot be backfilled (the plaintext was never stored), only rotated.

-- +goose Up
ALTER TABLE api_keys ADD COLUMN token_enc TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE api_keys DROP COLUMN token_enc;
