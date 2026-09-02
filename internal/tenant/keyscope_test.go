package tenant

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedTeamMemberWithKey creates a workspace member holding one active API key and
// returns the member's id and the key's id, so a test can act as one member and
// read the OTHER member's credential back.
func seedTeamMemberWithKey(t *testing.T, r *Repo, teamID, email string) (userID, keyID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	userID = uuid.NewString()
	if err := r.db.Exec("INSERT INTO users (id, email, password_hash, display_name, created_at) VALUES (?,?,?,?,?)",
		userID, email, "x", "M", now).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := r.db.Exec("INSERT INTO memberships (id, team_id, user_id, role, created_at) VALUES (?,?,?,?,?)",
		uuid.NewString(), teamID, userID, string(RoleMember), now).Error; err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	key, _, err := newAPIKey(teamID, userID, "default", now)
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	if err := r.db.Create(&key).Error; err != nil {
		t.Fatalf("store key: %v", err)
	}
	return userID, key.ID
}

// TestRotatingAKeyLeavesEveryOtherMemberSKeyAlive pins the user scope on the
// revoke half of RotateKey.
//
// ⚠ FOUND BY MUTATION (round 6): widening that UPDATE's WHERE to the team left
// the whole repository green. Rotation is the one control every member may run on
// their own — no admin role, no confirmation — so an unscoped revoke turns a
// routine self-service action into a workspace-wide outage: every other member's
// agent stops authenticating at once, and the only way back is for each of them to
// rotate in turn. RotateKey's own doc comment states the scope; nothing proved it.
//
// The assertion counts ACTIVE keys per member rather than reading the rotating
// member's new credential, because the new credential is correct in both worlds —
// the damage is entirely to rows the caller never asked about.
func TestRotatingAKeyLeavesEveryOtherMemberSKeyAlive(t *testing.T) {
	gdb := newMigratedDB(t)
	repo := NewRepo(gdb)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	teamID := uuid.NewString()
	if err := gdb.Exec("INSERT INTO teams (id, name, slug, kind, created_at) VALUES (?,?,?,?,?)",
		teamID, "Acme", "acme-"+teamID[:6], "personal", now).Error; err != nil {
		t.Fatalf("seed team: %v", err)
	}
	rotator, _ := seedTeamMemberWithKey(t, repo, teamID, "rotator@example.test")
	bystander, bystanderKey := seedTeamMemberWithKey(t, repo, teamID, "bystander@example.test")

	if _, err := repo.RotateKey(ctx, teamID, rotator); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	var stillActive int64
	if err := gdb.Model(&APIKey{}).
		Where("id = ? AND revoked_at IS NULL", bystanderKey).Count(&stillActive).Error; err != nil {
		t.Fatalf("count bystander key: %v", err)
	}
	if stillActive != 1 {
		t.Errorf("one member rotating their own key revoked another member's key (%s). RotateKey's revoke "+
			"must be scoped to (team, user): unscoped, any member can log every other member's agent out of "+
			"the workspace by rotating their own credential", bystander)
	}

	var rotatorActive int64
	if err := gdb.Model(&APIKey{}).
		Where("team_id = ? AND user_id = ? AND revoked_at IS NULL", teamID, rotator).Count(&rotatorActive).Error; err != nil {
		t.Fatalf("count rotator keys: %v", err)
	}
	if rotatorActive != 1 {
		t.Errorf("the rotating member holds %d active keys, want exactly 1 — rotation must revoke the old "+
			"credential as well as mint the new one, or the key it replaced goes on authenticating", rotatorActive)
	}
}
