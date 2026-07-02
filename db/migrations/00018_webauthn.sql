-- 00018_webauthn.sql
-- WebAuthn / passkey credentials. A passkey is a public-key credential held by a
-- device authenticator (Touch ID, Windows Hello, a security key). It is
-- passwordless-capable — the sign-in is device + biometric/PIN, so it stands in
-- for password+2FA — and can also serve as the second factor after a password.
--
-- A user may register several (laptop, phone, hardware key), so this is a child
-- table of users. Only PUBLIC keys are stored; the private key never leaves the
-- authenticator, so a database leak cannot impersonate a user.
--
-- data holds the JSON-marshalled webauthn.Credential (full fidelity: public key,
-- transports, flags, AAGUID). credential_id is duplicated out as an indexed,
-- unique column because login looks a credential up by its raw id. sign_count is
-- bumped from each assertion for authenticator clone detection.

-- +goose Up
CREATE TABLE webauthn_credentials (
    id            TEXT PRIMARY KEY,          -- UUID
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BLOB NOT NULL UNIQUE,      -- raw WebAuthn credential id (lookup key)
    name          TEXT NOT NULL DEFAULT '',  -- user-facing label, e.g. "MacBook"
    sign_count    INTEGER NOT NULL DEFAULT 0,
    data          TEXT NOT NULL,             -- JSON of webauthn.Credential (source of truth)
    created_at    TEXT NOT NULL,             -- RFC3339
    last_used_at  TEXT                       -- null until first login use
);
CREATE INDEX idx_webauthn_user ON webauthn_credentials(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_webauthn_user;
DROP TABLE IF EXISTS webauthn_credentials;
