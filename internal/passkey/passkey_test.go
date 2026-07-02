package passkey

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"
)

// newDB returns an in-memory SQLite with the webauthn_credentials table shaped to
// match migration 00018, so the Repo runs against the real schema.
func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE webauthn_credentials (
		id TEXT PRIMARY KEY, user_id TEXT NOT NULL, credential_id BLOB NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '', sign_count INTEGER NOT NULL DEFAULT 0,
		data TEXT NOT NULL, created_at TEXT NOT NULL, last_used_at TEXT)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// TestRepoRoundTrip covers the full credential lifecycle the ceremonies rely on:
// add → load back verbatim → list/count → bump sign count on login → delete with
// ownership scoping.
func TestRepoRoundTrip(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(newDB(t))
	cred := &webauthn.Credential{ID: []byte("cred-abc"), PublicKey: []byte("pk-bytes")}
	cred.Authenticator.SignCount = 0

	if err := r.add(ctx, "user-1", "MacBook", cred); err != nil {
		t.Fatalf("add: %v", err)
	}

	// credentialsFor must reconstruct the stored credential faithfully — the
	// ceremonies verify signatures against exactly these bytes.
	got, err := r.credentialsFor(ctx, "user-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("credentialsFor: got %d creds, err %v", len(got), err)
	}
	if string(got[0].ID) != "cred-abc" || string(got[0].PublicKey) != "pk-bytes" {
		t.Fatalf("credential not round-tripped: %+v", got[0])
	}

	if n, _ := r.Count(ctx, "user-1"); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
	list, _ := r.List(ctx, "user-1")
	if len(list) != 1 || list[0].Name != "MacBook" || list[0].LastUsedAt != "" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// A login bumps the sign count and stamps last_used_at.
	cred.Authenticator.SignCount = 5
	if err := r.updateAfterLogin(ctx, cred); err != nil {
		t.Fatalf("updateAfterLogin: %v", err)
	}
	list, _ = r.List(ctx, "user-1")
	if list[0].LastUsedAt == "" {
		t.Fatal("last_used_at not stamped after login")
	}

	// Delete is ownership-scoped: another user cannot remove this credential.
	if err := r.Delete(ctx, "someone-else", list[0].ID); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("cross-user delete: got %v, want ErrNoCredential", err)
	}
	if err := r.Delete(ctx, "user-1", list[0].ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if n, _ := r.Count(ctx, "user-1"); n != 0 {
		t.Fatalf("count after delete = %d, want 0", n)
	}
}

// TestConfigFromBaseURL locks the RPID/origin split — the classic passkey setup
// bug. RPID must drop the port; the origin must keep it.
func TestConfigFromBaseURL(t *testing.T) {
	cases := []struct {
		base, wantRPID, wantOrigin string
	}{
		{"http://localhost:8899", "localhost", "http://localhost:8899"},
		{"https://aiagentmemory.dev", "aiagentmemory.dev", "https://aiagentmemory.dev"},
		{"https://app.example.com:8443", "app.example.com", "https://app.example.com:8443"},
	}
	for _, c := range cases {
		cfg := ConfigFromBaseURL(c.base, "AI Agent Memory")
		if cfg.RPID != c.wantRPID {
			t.Errorf("%s: RPID = %q, want %q", c.base, cfg.RPID, c.wantRPID)
		}
		if len(cfg.RPOrigins) != 1 || cfg.RPOrigins[0] != c.wantOrigin {
			t.Errorf("%s: origins = %v, want [%q]", c.base, cfg.RPOrigins, c.wantOrigin)
		}
	}
}

// TestNewServiceValidConfig confirms the WebAuthn instance builds from a derived
// config (a bad RP config is a startup error, not a runtime surprise).
func TestNewServiceValidConfig(t *testing.T) {
	svc, err := NewService(ConfigFromBaseURL("https://aiagentmemory.dev", "AI Agent Memory"), NewRepo(newDB(t)))
	if err != nil || svc == nil {
		t.Fatalf("NewService: %v", err)
	}
}
