package tenant

// This file owns per-user TOTP two-factor authentication: enrolment, the
// login-time second factor, and the recovery-code escape hatch. It lives beside
// the password auth it augments (Authenticate) because 2FA is a property of the
// same User aggregate — a factor layered on the local-password sign-in, never on
// the social (goth) path, which trusts the provider's own MFA.
//
// The flow mirrors what shipped in the sibling vvs project (reviewed there):
// enrol → confirm a code → enabled; at login a valid TOTP code OR a one-time
// recovery code lets the session through. The recovery codes are the lost-device
// escape hatch and are stored one-way (sha256, reusing HashToken) exactly like
// api_keys stores its token — high-entropy input, so a hash is enough.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
)

// totpIssuer is the label authenticator apps show above the account entry (e.g.
// "AI Agent Memory (you@example.com)"). It is the human brand, not the Go module
// name, so the code the user reads is filed under the product they signed up for.
const totpIssuer = "AI Agent Memory"

// recoveryCodeCount is how many one-time recovery codes are minted when 2FA is
// enabled. Ten is the de-facto standard (GitHub, Google) — enough to survive
// several device losses before the user must regenerate.
const recoveryCodeCount = 10

// ErrTOTPNotPending is returned when confirming an enrolment that was never
// begun — there is no pending secret to validate a code against. It guards
// against a confirm call racing or replaying without a prior BeginTOTPEnrollment.
var ErrTOTPNotPending = errors.New("tenant: no pending TOTP enrolment")

// ErrTOTPInvalidCode is returned when a submitted code matches neither the live
// TOTP secret nor an unused recovery code. It is deliberately the single error
// for both, so a caller (and the UI) cannot tell which kind of code was tried —
// the same non-enumeration stance ErrInvalidCredentials takes for login.
var ErrTOTPInvalidCode = errors.New("tenant: invalid TOTP or recovery code")

// ErrTOTPNotEnabled is returned when a login-time verification is attempted for a
// user who has not enabled 2FA. Callers should never reach the second factor for
// such a user; it is a defensive guard, not a normal path.
var ErrTOTPNotEnabled = errors.New("tenant: TOTP is not enabled for this user")

// totpRecoveryCode is the gorm model for one one-time recovery code. Only the
// hash is stored; used_at burns the code so it can never be replayed. It is
// unexported because recovery codes are an internal mechanism of this package —
// callers deal in plaintext codes at mint time and never touch the rows.
type totpRecoveryCode struct {
	ID        string  `gorm:"primaryKey;column:id"`
	UserID    string  `gorm:"column:user_id"`
	CodeHash  string  `gorm:"column:code_hash"`
	UsedAt    *string `gorm:"column:used_at"` // nil = unused
	CreatedAt string  `gorm:"column:created_at"`
}

// TableName pins the model to the goose-managed table.
func (totpRecoveryCode) TableName() string { return "totp_recovery_codes" }

// BeginTOTPEnrollment mints a fresh TOTP secret for a user and stores it as
// pending (totp_enabled stays false, so login ignores it until confirmed). It
// returns the base32 secret (for manual entry) and the otpauth:// URL (which the
// web layer renders as a QR code). Calling it again overwrites any earlier
// pending secret, so a user who abandons setup and restarts simply gets a new
// secret — the last QR shown is the one that will confirm. The account label is
// the user's email so the entry in their authenticator names the right account.
func (r *Repo) BeginTOTPEnrollment(ctx context.Context, userID string) (secret, otpauthURL string, err error) {
	u, err := r.GetUserByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: u.Email,
	})
	if err != nil {
		return "", "", err
	}
	// Persist the secret pending confirmation. enabled is left untouched (false),
	// so the login path keeps ignoring this account's 2FA until ConfirmTOTP flips it.
	if err := r.db.WithContext(ctx).Model(&User{}).
		Where("id = ?", userID).
		Update("totp_secret", key.Secret()).Error; err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ConfirmTOTP verifies the first code from the user's authenticator against the
// pending secret and, on success, enables 2FA and mints a fresh set of one-time
// recovery codes — returned in plaintext exactly once for the user to save. It
// replaces any prior recovery codes in the same transaction, so re-confirming
// (e.g. after re-enrolling) never leaves stale codes valid. Requiring a valid
// code to enable proves the authenticator was actually seeded before the account
// starts demanding it, avoiding a self-inflicted lockout at first login.
func (r *Repo) ConfirmTOTP(ctx context.Context, userID, code string) ([]string, error) {
	u, err := r.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.TOTPSecret == "" {
		return nil, ErrTOTPNotPending
	}
	if !totp.Validate(strings.TrimSpace(code), u.TOTPSecret) {
		return nil, ErrTOTPInvalidCode
	}

	// Mint the recovery codes up front so a mint failure aborts before we flip the
	// account into a 2FA-required state it has no escape hatch for.
	display, rows, err := newRecoveryCodes(userID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", userID).
			Update("totp_enabled", true).Error; err != nil {
			return err
		}
		// Drop any prior codes so exactly this freshly shown set is valid.
		if err := tx.Where("user_id = ?", userID).Delete(&totpRecoveryCode{}).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].CreatedAt = now
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	return display, nil
}

// VerifyTOTPLogin checks a code at the login second-factor step. It accepts a
// live TOTP code or, failing that, burns a matching one-time recovery code. It
// returns nil on success and ErrTOTPInvalidCode when neither matches, so the
// login handler treats "wrong code" and "no code left" identically. A user
// without 2FA enabled is an ErrTOTPNotEnabled programmer error, never a normal
// login outcome.
func (r *Repo) VerifyTOTPLogin(ctx context.Context, userID, code string) error {
	u, err := r.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return ErrTOTPNotEnabled
	}
	code = strings.TrimSpace(code)
	if totp.Validate(code, u.TOTPSecret) {
		return nil
	}
	return r.consumeRecoveryCode(ctx, userID, code)
}

// DisableTOTP turns 2FA off, but only after the user proves current control with
// a valid TOTP code or recovery code — so a hijacked but un-stepped-up session
// cannot silently remove the second factor. It clears the secret, flips the flag
// and deletes all recovery codes in one transaction, returning the account to the
// password-only state.
func (r *Repo) DisableTOTP(ctx context.Context, userID, code string) error {
	if err := r.VerifyTOTPLogin(ctx, userID, code); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", userID).
			Updates(map[string]any{"totp_secret": "", "totp_enabled": false}).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&totpRecoveryCode{}).Error
	})
}

// consumeRecoveryCode burns the one unused recovery code whose hash matches, in a
// single guarded UPDATE. Matching on used_at IS NULL inside the write means two
// concurrent attempts with the same code cannot both succeed — the second updates
// zero rows. A zero-row result is an invalid/spent code, reported as
// ErrTOTPInvalidCode (never distinguished from a wrong TOTP code).
func (r *Repo) consumeRecoveryCode(ctx context.Context, userID, code string) error {
	norm := normalizeRecoveryCode(code)
	if norm == "" {
		return ErrTOTPInvalidCode
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res := r.db.WithContext(ctx).Model(&totpRecoveryCode{}).
		Where("user_id = ? AND code_hash = ? AND used_at IS NULL", userID, HashToken(norm)).
		Update("used_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTOTPInvalidCode
	}
	return nil
}

// newRecoveryCodes generates recoveryCodeCount codes, returning the plaintext
// display forms (grouped for readability, shown once) alongside the rows to
// persist (holding only the sha256 hash of each code's normalized form). The
// display and stored forms differ only by grouping dashes, which normalization
// strips — so a user typing "abcd-efgh-..." or "abcdefgh..." both match.
func newRecoveryCodes(userID string) (display []string, rows []totpRecoveryCode, err error) {
	display = make([]string, 0, recoveryCodeCount)
	rows = make([]totpRecoveryCode, 0, recoveryCodeCount)
	for range recoveryCodeCount {
		shown, norm, err := generateRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		display = append(display, shown)
		rows = append(rows, totpRecoveryCode{
			ID:       uuid.NewString(),
			UserID:   userID,
			CodeHash: HashToken(norm),
		})
	}
	return display, rows, nil
}

// generateRecoveryCode mints one recovery code: 8 crypto-random bytes (64 bits)
// as 16 hex chars, shown grouped "xxxx-xxxx-xxxx-xxxx" for legibility. It returns
// the display form and the normalized (ungrouped, lowercase) form that is hashed
// and later matched against user input.
func generateRecoveryCode() (display, normalized string, err error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	normalized = hex.EncodeToString(buf) // 16 lowercase hex chars
	display = normalized[0:4] + "-" + normalized[4:8] + "-" + normalized[8:12] + "-" + normalized[12:16]
	return display, normalized, nil
}

// normalizeRecoveryCode strips the readability grouping (dashes, spaces) and
// lower-cases the input, so what the user types is compared on the same footing
// the code was hashed on regardless of how they copied it.
func normalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if (r >= 'a' && r <= 'f') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
