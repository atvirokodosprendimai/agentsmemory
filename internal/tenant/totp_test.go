package tenant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
)

// newTOTPDB returns an in-memory SQLite with the users and totp_recovery_codes
// tables shaped to match migration 00017, so the TOTP methods run against the
// real schema (columns, NOT NULL defaults) rather than a gorm-guessed one.
func newTOTPDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		totp_secret TEXT NOT NULL DEFAULT '', totp_enabled INTEGER NOT NULL DEFAULT 0)`).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	if err := db.Exec(`CREATE TABLE totp_recovery_codes (
		id TEXT PRIMARY KEY, user_id TEXT NOT NULL, code_hash TEXT NOT NULL,
		used_at TEXT, created_at TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("create recovery codes: %v", err)
	}
	return db
}

// enrolAndEnable runs a fresh user through Begin+Confirm and returns the secret
// and the recovery codes, failing the test on any error — the common setup for
// the login/disable cases below.
func enrolAndEnable(t *testing.T, r *Repo, userID string) (secret string, recovery []string) {
	t.Helper()
	ctx := context.Background()
	secret, _, err := r.BeginTOTPEnrollment(ctx, userID)
	if err != nil {
		t.Fatalf("begin enrolment: %v", err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	recovery, err = r.ConfirmTOTP(ctx, userID, code)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return secret, recovery
}

// TestTOTPEnrolConfirmLogin walks the whole happy path: a pending secret is inert
// until confirmed, confirming enables 2FA and yields ten recovery codes, and a
// live code then passes the login second factor while a wrong one is rejected.
func TestTOTPEnrolConfirmLogin(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(newTOTPDB(t))
	u, err := r.CreateUserWithPassword(ctx, "a@example.com", "password123", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	secret, _, err := r.BeginTOTPEnrollment(ctx, u.ID)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// A pending secret must not yet demand a factor — enabled stays false.
	if got, _ := r.GetUserByID(ctx, u.ID); got.TOTPEnabled {
		t.Fatal("2FA enabled before confirmation")
	}

	code, _ := totp.GenerateCode(secret, time.Now())
	recovery, err := r.ConfirmTOTP(ctx, u.ID, code)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if len(recovery) != recoveryCodeCount {
		t.Fatalf("recovery codes = %d, want %d", len(recovery), recoveryCodeCount)
	}
	if got, _ := r.GetUserByID(ctx, u.ID); !got.TOTPEnabled {
		t.Fatal("2FA not enabled after confirmation")
	}

	fresh, _ := totp.GenerateCode(secret, time.Now())
	if err := r.VerifyTOTPLogin(ctx, u.ID, fresh); err != nil {
		t.Fatalf("verify valid code: %v", err)
	}
	if err := r.VerifyTOTPLogin(ctx, u.ID, "000000"); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatalf("verify bad code: got %v, want ErrTOTPInvalidCode", err)
	}
}

// TestTOTPRecoveryCodeOneTime confirms a recovery code passes login once, is
// burned, and is rejected on reuse — and that the display grouping (dashes) is
// tolerated because verification normalizes the input.
func TestTOTPRecoveryCodeOneTime(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(newTOTPDB(t))
	u, _ := r.CreateUserWithPassword(ctx, "b@example.com", "password123", "")
	_, recovery := enrolAndEnable(t, r, u.ID)

	// The grouped display form must work as typed by the user.
	code := recovery[0]
	if !strings.Contains(code, "-") {
		t.Fatalf("recovery code %q not grouped as expected", code)
	}
	if err := r.VerifyTOTPLogin(ctx, u.ID, code); err != nil {
		t.Fatalf("first recovery use: %v", err)
	}
	// Reuse of the same code must fail — it was burned.
	if err := r.VerifyTOTPLogin(ctx, u.ID, code); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatalf("recovery reuse: got %v, want ErrTOTPInvalidCode", err)
	}
	// A different, still-unused code works and tolerates being typed ungrouped.
	ungrouped := strings.ReplaceAll(recovery[1], "-", "")
	if err := r.VerifyTOTPLogin(ctx, u.ID, ungrouped); err != nil {
		t.Fatalf("second recovery (ungrouped): %v", err)
	}
}

// TestConfirmTOTPGuards checks the two confirm-time guards: no pending secret is
// ErrTOTPNotPending, and a wrong code is ErrTOTPInvalidCode that leaves the
// account disabled (never a half-enabled state).
func TestConfirmTOTPGuards(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(newTOTPDB(t))
	u, _ := r.CreateUserWithPassword(ctx, "c@example.com", "password123", "")

	if _, err := r.ConfirmTOTP(ctx, u.ID, "123456"); !errors.Is(err, ErrTOTPNotPending) {
		t.Fatalf("confirm without enrolment: got %v, want ErrTOTPNotPending", err)
	}

	if _, _, err := r.BeginTOTPEnrollment(ctx, u.ID); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := r.ConfirmTOTP(ctx, u.ID, "000000"); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatalf("confirm bad code: got %v, want ErrTOTPInvalidCode", err)
	}
	if got, _ := r.GetUserByID(ctx, u.ID); got.TOTPEnabled {
		t.Fatal("2FA enabled despite failed confirmation")
	}
}

// TestDisableTOTP confirms disabling requires proof of control: a wrong code is
// rejected and leaves 2FA on, while a valid code clears the secret and returns the
// account to the not-enabled state (so login stops demanding a factor).
func TestDisableTOTP(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(newTOTPDB(t))
	u, _ := r.CreateUserWithPassword(ctx, "d@example.com", "password123", "")
	secret, _ := enrolAndEnable(t, r, u.ID)

	if err := r.DisableTOTP(ctx, u.ID, "000000"); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatalf("disable with bad code: got %v, want ErrTOTPInvalidCode", err)
	}
	if got, _ := r.GetUserByID(ctx, u.ID); !got.TOTPEnabled {
		t.Fatal("2FA disabled by an invalid code")
	}

	code, _ := totp.GenerateCode(secret, time.Now())
	if err := r.DisableTOTP(ctx, u.ID, code); err != nil {
		t.Fatalf("disable with valid code: %v", err)
	}
	got, _ := r.GetUserByID(ctx, u.ID)
	if got.TOTPEnabled || got.TOTPSecret != "" {
		t.Fatalf("2FA not fully cleared: enabled=%v secret=%q", got.TOTPEnabled, got.TOTPSecret)
	}
	if err := r.VerifyTOTPLogin(ctx, u.ID, code); !errors.Is(err, ErrTOTPNotEnabled) {
		t.Fatalf("verify after disable: got %v, want ErrTOTPNotEnabled", err)
	}
}
