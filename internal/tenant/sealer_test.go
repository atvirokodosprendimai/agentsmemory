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
	if got, err := r.RevealToken(ctx, "team-a", "u1"); err != nil || got != "plain-tok" {
		t.Fatalf("reveal = (%q, %v), want (plain-tok, nil)", got, err)
	}

	// A legacy key (minted before reveal) has an empty seal and is unavailable.
	if err := db.Create(&APIKey{
		ID: "k2", TeamID: "team-legacy", UserID: "u1", TokenHash: "h2",
		CreatedAt: "2026-06-28T00:00:00Z",
	}).Error; err != nil {
		t.Fatalf("seed legacy key: %v", err)
	}
	if _, err := r.RevealToken(ctx, "team-legacy", "u1"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("legacy reveal err = %v, want ErrTokenUnavailable", err)
	}

	// Unknown team and a reveal-disabled repo both fail closed.
	if _, err := r.RevealToken(ctx, "nope", "u1"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("unknown reveal err = %v, want ErrTokenUnavailable", err)
	}
	if _, err := NewRepo(db).RevealToken(ctx, "team-a", "u1"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("no-sealer reveal err = %v, want ErrTokenUnavailable", err)
	}
}

// TestRotateKeyRevokesOldAndRevealsNew confirms rotation revokes the prior
// (unrevealable, legacy) key and issues a fresh one that reveals to the returned
// secret — the recovery path for a key that can no longer be shown.
func TestRotateKeyRevokesOldAndRevealsNew(t *testing.T) {
	db := newAPIKeyDB(t)
	r := NewRepo(db, WithTokenSecret("token-key"))
	ctx := context.Background()

	// A legacy active key with no seal — unrevealable, exactly the stuck state.
	if err := db.Create(&APIKey{
		ID: "old", TeamID: "team-a", UserID: "u1", TokenHash: "oldhash",
		CreatedAt: "2026-06-01T00:00:00Z",
	}).Error; err != nil {
		t.Fatalf("seed legacy key: %v", err)
	}
	if _, err := r.RevealToken(ctx, "team-a", "u1"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("precondition: legacy key should be unrevealable, got %v", err)
	}

	cred, err := r.RotateKey(ctx, "team-a", "u1")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if cred.Secret == "" {
		t.Fatal("rotate returned an empty secret")
	}

	// The old key is now revoked.
	var old APIKey
	if err := db.Where("id = ?", "old").First(&old).Error; err != nil {
		t.Fatalf("reload old key: %v", err)
	}
	if old.RevokedAt == nil {
		t.Fatal("old key should be revoked after rotation")
	}

	// The new (active) key reveals to exactly the returned secret.
	got, err := r.RevealToken(ctx, "team-a", "u1")
	if err != nil || got != cred.Secret {
		t.Fatalf("reveal after rotate = (%q, %v), want (%q, nil)", got, err, cred.Secret)
	}
}

// TestRevealTokenIsPerMember confirms reveal is scoped to (team, user): one
// member's reveal returns their own key, never another member's, even in the same
// team. This is the property that lets any member reveal without escalation.
func TestRevealTokenIsPerMember(t *testing.T) {
	db := newAPIKeyDB(t)
	r := NewRepo(db, WithTokenSecret("token-key"))
	ctx := context.Background()

	encA, _ := r.sealToken("tok-a")
	encB, _ := r.sealToken("tok-b")
	for _, k := range []APIKey{
		{ID: "ka", TeamID: "team-a", UserID: "alice", TokenHash: "ha", TokenEnc: encA, CreatedAt: "2026-06-28T00:00:00Z"},
		{ID: "kb", TeamID: "team-a", UserID: "bob", TokenHash: "hb", TokenEnc: encB, CreatedAt: "2026-06-28T00:00:00Z"},
	} {
		if err := db.Create(&k).Error; err != nil {
			t.Fatalf("seed key %s: %v", k.ID, err)
		}
	}

	if got, err := r.RevealToken(ctx, "team-a", "alice"); err != nil || got != "tok-a" {
		t.Fatalf("alice reveal = (%q, %v), want (tok-a, nil)", got, err)
	}
	if got, err := r.RevealToken(ctx, "team-a", "bob"); err != nil || got != "tok-b" {
		t.Fatalf("bob reveal = (%q, %v), want (tok-b, nil)", got, err)
	}
	// A member with no key in the team reveals nothing (not another member's key).
	if _, err := r.RevealToken(ctx, "team-a", "carol"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("carol reveal err = %v, want ErrTokenUnavailable", err)
	}
}
