package tenant

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestSealerRoundTrip confirms a token seals and opens to itself, and that a
// wrong key or a tampered blob both fail closed as ErrTokenUnavailable (never
// leaking which check failed).
func TestSealerRoundTrip(t *testing.T) {
	s, err := newSealer("a-secret")
	if err != nil {
		t.Fatalf("newSealer: %v", err)
	}
	blob, err := s.seal("super-token")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if got, err := s.open(blob); err != nil || got != "super-token" {
		t.Fatalf("round trip = (%q, %v), want (super-token, nil)", got, err)
	}

	// A different key cannot open the blob (GCM auth fails).
	other, _ := newSealer("other-secret")
	if _, err := other.open(blob); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("cross-key open err = %v, want ErrTokenUnavailable", err)
	}

	// A tampered blob fails authentication.
	if _, err := s.open(blob + "AA"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("tampered open err = %v, want ErrTokenUnavailable", err)
	}
}

// newAPIKeyDB returns an in-memory SQLite with the api_keys table (token_enc
// included), matching the goose migration shape.
func newAPIKeyDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE api_keys (
		id TEXT PRIMARY KEY, team_id TEXT, user_id TEXT, name TEXT, prefix TEXT,
		client_key TEXT, token_hash TEXT, token_enc TEXT NOT NULL DEFAULT '',
		created_at TEXT, last_used_at TEXT, revoked_at TEXT)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// TestRevealToken confirms a sealed key reveals its plaintext, and that every
// gap — a legacy key with no seal, an unknown team, and a repo with reveal
// disabled — fails closed as ErrTokenUnavailable.
func TestRevealToken(t *testing.T) {
	db := newAPIKeyDB(t)
	r := NewRepo(db, WithTokenSecret("token-key"))
	ctx := context.Background()

	enc, err := r.sealToken("plain-tok")
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}
	if err := db.Create(&APIKey{
		ID: "k1", TeamID: "team-a", UserID: "u1", TokenHash: "h", TokenEnc: enc,
		CreatedAt: "2026-06-28T00:00:00Z",
	}).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	if got, err := r.RevealToken(ctx, "team-a"); err != nil || got != "plain-tok" {
		t.Fatalf("reveal = (%q, %v), want (plain-tok, nil)", got, err)
	}

	// A legacy key (minted before reveal) has an empty seal and is unavailable.
	if err := db.Create(&APIKey{
		ID: "k2", TeamID: "team-legacy", UserID: "u1", TokenHash: "h2",
		CreatedAt: "2026-06-28T00:00:00Z",
	}).Error; err != nil {
		t.Fatalf("seed legacy key: %v", err)
	}
	if _, err := r.RevealToken(ctx, "team-legacy"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("legacy reveal err = %v, want ErrTokenUnavailable", err)
	}

	// Unknown team and a reveal-disabled repo both fail closed.
	if _, err := r.RevealToken(ctx, "nope"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("unknown reveal err = %v, want ErrTokenUnavailable", err)
	}
	if _, err := NewRepo(db).RevealToken(ctx, "team-a"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("no-sealer reveal err = %v, want ErrTokenUnavailable", err)
	}
}
