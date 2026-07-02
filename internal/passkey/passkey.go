// Package passkey is the WebAuthn/passkey bounded context: it registers and
// verifies public-key credentials so a user can sign in with a device
// authenticator (Touch ID, Windows Hello, a security key) — either passwordless
// (a passkey is device + biometric, so it stands in for password+2FA) or as the
// second factor after a password.
//
// It is deliberately standalone: it owns only the credential store and the
// WebAuthn ceremonies, and never imports the tenant/user package. The caller
// (the web layer) resolves who the user is and passes their id/name/display in;
// passkey hands back and takes JSON blobs (the options to send the browser, the
// opaque ceremony session, and the browser's response) so the web layer never
// touches a go-webauthn type. This keeps the one heavy dependency contained here.
package passkey

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrNoCredential is returned when a credential lookup finds nothing the caller
// is allowed to act on (unknown id, or one owned by another user).
var ErrNoCredential = errors.New("passkey: no such credential")

// ErrClonedAuthenticator is returned when an assertion's signature counter went
// backwards relative to the stored value — go-webauthn's signal that two copies
// of the credential's private key may exist (a cloned authenticator, or a
// replayed assertion). We treat it as a hard login failure rather than an
// RP-optional warning: the safe response to a possible clone is to refuse.
var ErrClonedAuthenticator = errors.New("passkey: authenticator clone warning")

// credentialRow is the gorm model for one stored passkey. The full
// webauthn.Credential is kept JSON-encoded in Data (the source of truth);
// CredentialID and SignCount are projected out as columns for the login lookup
// and clone-detection update respectively.
type credentialRow struct {
	ID           string  `gorm:"primaryKey;column:id"`
	UserID       string  `gorm:"column:user_id"`
	CredentialID []byte  `gorm:"column:credential_id"`
	Name         string  `gorm:"column:name"`
	SignCount    uint32  `gorm:"column:sign_count"`
	Data         string  `gorm:"column:data"`
	CreatedAt    string  `gorm:"column:created_at"`
	LastUsedAt   *string `gorm:"column:last_used_at"`
}

// TableName pins the model to the goose-managed table.
func (credentialRow) TableName() string { return "webauthn_credentials" }

// CredentialInfo is the display-ready view of a registered passkey for the
// account page — metadata only, never the key material.
type CredentialInfo struct {
	ID         string // our row id (the delete handle), not the WebAuthn credential id
	Name       string
	CreatedAt  string
	LastUsedAt string // "" when never used to sign in
}

// Repo is the persistence boundary for passkey credentials over a *gorm.DB.
type Repo struct {
	db *gorm.DB
}

// NewRepo constructs a Repo over an open gorm connection.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// credentialsFor loads a user's credentials as webauthn.Credential values, used
// to build the WebAuthn user for a ceremony. A user with none yields an empty
// slice (not an error) — that is simply someone who hasn't enrolled a passkey.
func (r *Repo) credentialsFor(ctx context.Context, userID string) ([]webauthn.Credential, error) {
	var rows []credentialRow
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, row := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(row.Data), &c); err != nil {
			return nil, err
		}
		creds = append(creds, c)
	}
	return creds, nil
}

// add stores a freshly registered credential under a user, with a display label.
func (r *Repo) add(ctx context.Context, userID, name string, cred *webauthn.Credential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(&credentialRow{
		ID:           uuid.NewString(),
		UserID:       userID,
		CredentialID: cred.ID,
		Name:         name,
		SignCount:    cred.Authenticator.SignCount,
		Data:         string(data),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}).Error
}

// updateAfterLogin persists the post-assertion credential state (sign count for
// clone detection, and any updated flags) and stamps last_used_at. It matches on
// the raw credential id, so it updates the exact credential that just asserted.
func (r *Repo) updateAfterLogin(ctx context.Context, cred *webauthn.Credential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return r.db.WithContext(ctx).Model(&credentialRow{}).
		Where("credential_id = ?", cred.ID).
		Updates(map[string]any{
			"sign_count":   cred.Authenticator.SignCount,
			"data":         string(data),
			"last_used_at": now,
		}).Error
}

// List returns a user's registered passkeys as display models, newest first.
func (r *Repo) List(ctx context.Context, userID string) ([]CredentialInfo, error) {
	var rows []credentialRow
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]CredentialInfo, 0, len(rows))
	for _, row := range rows {
		info := CredentialInfo{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt}
		if row.LastUsedAt != nil {
			info.LastUsedAt = *row.LastUsedAt
		}
		out = append(out, info)
	}
	return out, nil
}

// Count reports how many passkeys a user has registered.
func (r *Repo) Count(ctx context.Context, userID string) (int, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&credentialRow{}).Where("user_id = ?", userID).Count(&n).Error
	return int(n), err
}

// Delete removes one passkey the user owns (by our row id). Scoping the delete to
// user_id means a user can never remove another user's credential by guessing an
// id. A no-op delete (wrong owner or unknown id) is reported as ErrNoCredential.
func (r *Repo) Delete(ctx context.Context, userID, id string) error {
	res := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).Delete(&credentialRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNoCredential
	}
	return nil
}

// user is the passkey package's implementation of webauthn.User. It is built two
// ways: fully (id+name+display) for registration where the authenticator shows
// the account, and minimally (id+credentials) for login where only the handle and
// the stored credentials matter.
type user struct {
	id      string
	name    string
	display string
	creds   []webauthn.Credential
}

// WebAuthnID is the opaque user handle stored in the authenticator. We use the
// account's UUID (36 bytes, well under the 64-byte cap) — not PII, and stable, so
// a discoverable login's returned handle maps straight back to the account.
func (u *user) WebAuthnID() []byte { return []byte(u.id) }

// WebAuthnName is the human-palatable account name shown by the authenticator.
func (u *user) WebAuthnName() string { return u.name }

// WebAuthnDisplayName is the display name shown by the authenticator.
func (u *user) WebAuthnDisplayName() string { return u.display }

// WebAuthnCredentials is the set of credentials already registered to the user —
// used to exclude re-registration and to scope a login to the right keys.
func (u *user) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// Config is the Relying Party configuration for the WebAuthn ceremonies.
type Config struct {
	RPID          string   // effective domain, no scheme/port (e.g. "aiagentmemory.dev", "localhost")
	RPDisplayName string   // human name shown by the authenticator
	RPOrigins     []string // full origins permitted (scheme+host+port)
}

// ConfigFromBaseURL derives the RP config from the server's public base URL (the
// same PUBLIC_BASE_URL the OAuth callbacks use). The RPID is the hostname WITHOUT
// the port (a WebAuthn requirement — the port lives only in the origin), while
// the origin keeps scheme+host+port so a localhost dev server on a non-standard
// port still validates. Getting this pair wrong is the classic passkey setup bug,
// so it is derived in one place and unit-tested.
func ConfigFromBaseURL(baseURL, displayName string) Config {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		// A malformed base URL falls back to localhost so local dev still works
		// rather than panicking; production must set PUBLIC_BASE_URL correctly.
		return Config{RPID: "localhost", RPDisplayName: displayName, RPOrigins: []string{"http://localhost:8080"}}
	}
	return Config{
		RPID:          u.Hostname(),
		RPDisplayName: displayName,
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	}
}
