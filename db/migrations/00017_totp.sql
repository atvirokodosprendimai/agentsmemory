-- 00017_totp.sql
-- Per-user TOTP two-factor authentication (Google Authenticator / Authy / etc.).
-- Opt-in: a user enrols on the account page, and once enabled their email+password
-- sign-in requires a 6-digit code. Social (goth) logins are trusted to the
-- provider's own MFA and skip this step, so these columns are meaningful only for
-- the local-password path.
--
-- totp_secret holds the base32 shared secret the authenticator app is seeded with.
-- It is set (pending) at enrolment and only becomes live once totp_enabled flips —
-- an unconfirmed secret is inert, so login never consults it until enabled=1.
-- The secret is a bearer of full 2FA authority, so a DB-only leak weakens the
-- second factor; it is stored plaintext here to match the app's threat model
-- (the same DB already holds password bcrypt hashes and sealed API keys), and can
-- be upgraded to at-rest encryption later without a schema change.
--
-- Recovery codes are the lost-device escape hatch: 10 one-time codes minted on
-- enable, each stored only as a sha256 hash (the same one-way scheme api_keys use
-- for token_hash — high-entropy input, so a hash is sufficient and constant-time
-- to check). A code satisfies the login second factor OR disables 2FA, then is
-- burned (used_at set) so it can never be replayed.

-- +goose Up
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;

CREATE TABLE totp_recovery_codes (
    id         TEXT PRIMARY KEY,          -- UUID
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,             -- sha256(code) hex; never the plaintext
    used_at    TEXT,                      -- null = unused; set to RFC3339 when burned
    created_at TEXT NOT NULL
);
CREATE INDEX idx_totp_recovery_user ON totp_recovery_codes(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_totp_recovery_user;
DROP TABLE IF EXISTS totp_recovery_codes;
ALTER TABLE users DROP COLUMN totp_enabled;
ALTER TABLE users DROP COLUMN totp_secret;
