package tenant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestARevokedKeyStopsAuthenticating pins the one clause that makes revocation
// mean anything.
//
// ⚠ FOUND BY MUTATION (round 7): dropping `revoked_at IS NULL` from
// ResolveToken's lookup left the whole repository green. Revocation is the ONLY
// way to cut off a credential that has leaked, and every control that offers it —
// rotating a key, removing a member — is a soft revoke that writes revoked_at and
// then relies on this one WHERE clause to be honoured. Both of those paths have
// tests; both assert that the COLUMN is set. Nothing asserted that the column is
// read, so the whole revocation story rested on a clause no test could see.
//
// It drives ResolveToken rather than the SQL, because the property is "the token
// stops working", and it carries the control every refusal test needs: the same
// key, before revocation, must resolve. A lookup that refuses everything would
// otherwise pass.
func TestARevokedKeyStopsAuthenticating(t *testing.T) {
	gdb := newMigratedDB(t)
	repo := NewRepo(gdb)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	teamID, userID := uuid.NewString(), uuid.NewString()
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{"INSERT INTO teams (id, name, slug, kind, created_at) VALUES (?,?,?,?,?)",
			[]any{teamID, "Acme", "acme-" + teamID[:6], "personal", now}},
		{"INSERT INTO users (id, email, password_hash, display_name, created_at) VALUES (?,?,?,?,?)",
			[]any{userID, "agent@example.test", "x", "A", now}},
		{"INSERT INTO memberships (id, team_id, user_id, role, created_at) VALUES (?,?,?,?,?)",
			[]any{uuid.NewString(), teamID, userID, string(RoleMember), now}},
	} {
		if err := gdb.Exec(stmt.q, stmt.args...).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	key, cred, err := newAPIKey(teamID, userID, "default", now)
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	if err := gdb.Create(&key).Error; err != nil {
		t.Fatalf("store key: %v", err)
	}

	// The control: while the key is active it must resolve, or the refusal below
	// proves nothing — a lookup that refuses every token would pass without it.
	if tn, err := repo.ResolveToken(ctx, cred.Secret); err != nil {
		t.Fatalf("an active key failed to authenticate: %v", err)
	} else if tn.TeamID != teamID {
		t.Fatalf("an active key resolved to workspace %q, want %q", tn.TeamID, teamID)
	}

	if err := gdb.Model(&APIKey{}).Where("id = ?", key.ID).
		Update("revoked_at", now).Error; err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, err := repo.ResolveToken(ctx, cred.Secret); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("a REVOKED key still authenticated (err=%v, want ErrInvalidToken). Rotation and member "+
			"removal both revoke by writing revoked_at and then rely on ResolveToken to honour it, so a "+
			"lookup that ignores the column means no credential in this system can ever be cut off — "+
			"including one that has leaked", err)
	}
}
