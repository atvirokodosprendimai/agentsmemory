package tenant

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newMembershipDB returns an in-memory SQLite with just the memberships table —
// the only table MembershipRole reads — shaped to match the goose migration.
func newMembershipDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE memberships (
		id TEXT PRIMARY KEY, team_id TEXT NOT NULL, user_id TEXT NOT NULL,
		role TEXT NOT NULL, created_at TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// seed inserts one membership row directly so the test exercises the real query.
func seed(t *testing.T, db *gorm.DB, teamID, userID, role string) {
	t.Helper()
	if err := db.Create(&Membership{
		ID: userID + "-" + teamID, TeamID: teamID, UserID: userID,
		Role: role, CreatedAt: "2026-06-28T00:00:00Z",
	}).Error; err != nil {
		t.Fatalf("seed membership: %v", err)
	}
}

// TestMembershipRoleResolvesAndGates confirms a member's role is returned, that a
// non-member gets ErrNotMember (the authz gate the dashboard depends on), and
// that the lookup is scoped to the (team, user) pair — a role in one team must
// not leak to another.
func TestMembershipRoleResolvesAndGates(t *testing.T) {
	db := newMembershipDB(t)
	r := NewRepo(db)
	ctx := context.Background()

	seed(t, db, "team-a", "alice", string(RoleAdmin))
	seed(t, db, "team-b", "bob", string(RoleWriter))

	// A member resolves to their stored role.
	if got, err := r.MembershipRole(ctx, "alice", "team-a"); err != nil || got != RoleAdmin {
		t.Fatalf("alice@team-a = (%q, %v), want (admin, nil)", got, err)
	}

	// A non-member of the team is refused, even though they are a member elsewhere.
	if _, err := r.MembershipRole(ctx, "bob", "team-a"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("bob@team-a err = %v, want ErrNotMember", err)
	}

	// An unknown user is refused.
	if _, err := r.MembershipRole(ctx, "carol", "team-a"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("carol@team-a err = %v, want ErrNotMember", err)
	}
}

// TestMembershipRoleEmptyRoleIsMember confirms a blank stored role degrades to the
// least-privileged member, so a malformed row can never imply write access.
func TestMembershipRoleEmptyRoleIsMember(t *testing.T) {
	db := newMembershipDB(t)
	r := NewRepo(db)
	seed(t, db, "team-a", "dan", "")

	got, err := r.MembershipRole(context.Background(), "dan", "team-a")
	if err != nil || got != RoleMember {
		t.Fatalf("dan@team-a = (%q, %v), want (member, nil)", got, err)
	}
}
