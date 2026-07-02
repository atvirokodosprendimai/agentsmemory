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

// newPlanDB returns an in-memory SQLite with the teams + plans tables (the shape
// PlanByCode/SetTeamPlan/PlanForTeam touch), seeded with the free plan, a Pro
// plan, and one team on the free plan — the starting state billing flips.
func newPlanDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE plans (
		id TEXT PRIMARY KEY, code TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, name TEXT NOT NULL,
		price_cents INTEGER NOT NULL DEFAULT 0, currency TEXT NOT NULL DEFAULT 'eur',
		monthly_request_cap INTEGER NOT NULL DEFAULT 0, billing_interval TEXT NOT NULL DEFAULT 'month',
		created_at TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("create plans: %v", err)
	}
	if err := db.Exec(`CREATE TABLE teams (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE, kind TEXT NOT NULL DEFAULT 'personal',
		plan_id TEXT, created_at TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("create teams: %v", err)
	}
	if err := db.Exec(`INSERT INTO plans (id, code, kind, name, price_cents, currency, monthly_request_cap, billing_interval, created_at) VALUES
		('plan_personal','personal','personal','Free',0,'eur',10000,'month','1970-01-01T00:00:00Z'),
		('plan_pro_monthly','pro_monthly','personal','Pro',5000,'eur',1000000,'month','1970-01-01T00:00:00Z'),
		('plan_unlimited','unlimited','enterprise','Unlimited',0,'eur',-1,'month','1970-01-01T00:00:00Z')`).Error; err != nil {
		t.Fatalf("seed plans: %v", err)
	}
	if err := db.Exec(`INSERT INTO teams (id, name, slug, kind, plan_id, created_at) VALUES
		('team-a','Acme','acme','personal','plan_personal','1970-01-01T00:00:00Z')`).Error; err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return db
}

// TestPlanByCode confirms a sellable plan is resolved by its stable code (with
// its billing interval), and that an unknown code is an error.
func TestPlanByCode(t *testing.T) {
	r := NewRepo(newPlanDB(t))
	ctx := context.Background()

	pro, err := r.PlanByCode(ctx, "pro_monthly")
	if err != nil {
		t.Fatalf("PlanByCode(pro_monthly): %v", err)
	}
	if pro.ID != "plan_pro_monthly" || pro.Name != "Pro" || pro.BillingInterval != "month" || pro.PriceCents != 5000 {
		t.Fatalf("unexpected plan: %+v", pro)
	}
	if _, err := r.PlanByCode(ctx, "nope"); err == nil {
		t.Fatal("expected error for unknown plan code")
	}
}

// TestSetTeamPlanFlipsEffectivePlan confirms SetTeamPlan changes teams.plan_id —
// the single column PlanForTeam and MonthlyCap read — so an upgrade is visible to
// the metering path with no other change.
func TestSetTeamPlanFlipsEffectivePlan(t *testing.T) {
	r := NewRepo(newPlanDB(t))
	ctx := context.Background()

	// Starts on Free (10k cap).
	if cap, err := r.MonthlyCap(ctx, "team-a"); err != nil || cap != 10000 {
		t.Fatalf("initial cap = (%d, %v), want (10000, nil)", cap, err)
	}
	if err := r.SetTeamPlan(ctx, "team-a", "plan_pro_monthly"); err != nil {
		t.Fatalf("SetTeamPlan: %v", err)
	}
	plan, err := r.PlanForTeam(ctx, "team-a")
	if err != nil || plan.ID != "plan_pro_monthly" {
		t.Fatalf("PlanForTeam after upgrade = (%+v, %v)", plan, err)
	}
	if cap, err := r.MonthlyCap(ctx, "team-a"); err != nil || cap != 1000000 {
		t.Fatalf("cap after upgrade = (%d, %v), want (1000000, nil)", cap, err)
	}
}

// TestSetTeamPlanUnlimitedUncapsRequests confirms the operator override behind the
// set-plan CLI: attaching the Unlimited plan (cap -1) resolves by code and makes
// MonthlyCap report -1, the sentinel usage.Allow treats as "no limit". This is the
// end-to-end tenant-side guarantee that a comped workspace runs uncapped.
func TestSetTeamPlanUnlimitedUncapsRequests(t *testing.T) {
	r := NewRepo(newPlanDB(t))
	ctx := context.Background()

	unlimited, err := r.PlanByCode(ctx, "unlimited")
	if err != nil {
		t.Fatalf("PlanByCode(unlimited): %v", err)
	}
	if unlimited.ID != "plan_unlimited" || unlimited.MonthlyRequestCap != -1 {
		t.Fatalf("unexpected unlimited plan: %+v", unlimited)
	}
	if err := r.SetTeamPlan(ctx, "team-a", unlimited.ID); err != nil {
		t.Fatalf("SetTeamPlan(unlimited): %v", err)
	}
	if cap, err := r.MonthlyCap(ctx, "team-a"); err != nil || cap != -1 {
		t.Fatalf("cap after unlimited = (%d, %v), want (-1, nil)", cap, err)
	}
}

// newMemberDB returns an in-memory SQLite with the users, memberships and
// api_keys tables — the shape the member-management methods touch — matching the
// goose migration (including the (team_id,user_id) uniqueness on memberships).
func newMemberDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, totp_secret TEXT NOT NULL DEFAULT '',
			totp_enabled INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE memberships (id TEXT PRIMARY KEY, team_id TEXT NOT NULL, user_id TEXT NOT NULL,
			role TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(team_id, user_id))`,
		`CREATE TABLE api_keys (id TEXT PRIMARY KEY, team_id TEXT, user_id TEXT, name TEXT, prefix TEXT,
			client_key TEXT, token_hash TEXT, token_enc TEXT NOT NULL DEFAULT '',
			created_at TEXT, last_used_at TEXT, revoked_at TEXT)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db
}

// seedUser inserts a user row so AddMemberByEmail can resolve them by email.
func seedUser(t *testing.T, db *gorm.DB, id, email string) {
	t.Helper()
	if err := db.Create(&User{
		ID: id, Email: email, DisplayName: email, CreatedAt: "2026-06-28T00:00:00Z",
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// TestAddMemberByEmail confirms adding an existing user creates the membership and
// mints them their own revealable key (case-insensitively on email), and that the
// three refusals — already a member, unknown email, invalid role — are distinct.
func TestAddMemberByEmail(t *testing.T) {
	db := newMemberDB(t)
	r := NewRepo(db, WithTokenSecret("token-key"))
	ctx := context.Background()
	seedUser(t, db, "u-bob", "bob@example.com")

	// Case-insensitive email match; role is honoured.
	m, err := r.AddMemberByEmail(ctx, "team-a", "BOB@example.com", RoleWriter)
	if err != nil {
		t.Fatalf("AddMemberByEmail: %v", err)
	}
	if m.UserID != "u-bob" || m.Role != RoleWriter {
		t.Fatalf("member = %+v, want u-bob/writer", m)
	}
	if role, err := r.MembershipRole(ctx, "u-bob", "team-a"); err != nil || role != RoleWriter {
		t.Fatalf("membership role = (%q, %v), want writer", role, err)
	}
	// The new member holds their own key and can reveal it themselves.
	if _, err := r.RevealToken(ctx, "team-a", "u-bob"); err != nil {
		t.Fatalf("new member should have a revealable key: %v", err)
	}
	// Re-adding the same person is a distinct, non-retryable condition.
	if _, err := r.AddMemberByEmail(ctx, "team-a", "bob@example.com", RoleMember); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("re-add err = %v, want ErrAlreadyMember", err)
	}
	// An unknown email is a dead end (must register first).
	if _, err := r.AddMemberByEmail(ctx, "team-a", "ghost@example.com", RoleMember); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown email err = %v, want ErrUserNotFound", err)
	}
	// A bogus role never persists.
	if _, err := r.AddMemberByEmail(ctx, "team-a", "bob@example.com", Role("root")); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("bad role err = %v, want ErrInvalidRole", err)
	}
}

// TestSetMemberRoleLastAdminGuard confirms role changes stick, that the sole admin
// cannot be demoted (locking everyone out), that demotion is allowed once a second
// admin exists, and that unknown-member / bad-role are refused.
func TestSetMemberRoleLastAdminGuard(t *testing.T) {
	db := newMemberDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	seed(t, db, "team-a", "alice", string(RoleAdmin))
	seed(t, db, "team-a", "bob", string(RoleMember))

	if err := r.SetMemberRole(ctx, "team-a", "bob", RoleWriter); err != nil {
		t.Fatalf("promote bob: %v", err)
	}
	if role, _ := r.MembershipRole(ctx, "bob", "team-a"); role != RoleWriter {
		t.Fatalf("bob role = %q, want writer", role)
	}
	// The only admin cannot be demoted.
	if err := r.SetMemberRole(ctx, "team-a", "alice", RoleMember); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote sole admin err = %v, want ErrLastAdmin", err)
	}
	// With a second admin, demotion is fine.
	if err := r.SetMemberRole(ctx, "team-a", "bob", RoleAdmin); err != nil {
		t.Fatalf("promote bob to admin: %v", err)
	}
	if err := r.SetMemberRole(ctx, "team-a", "alice", RoleMember); err != nil {
		t.Fatalf("demote alice with 2 admins: %v", err)
	}
	if err := r.SetMemberRole(ctx, "team-a", "ghost", RoleWriter); !errors.Is(err, ErrNotMember) {
		t.Fatalf("set role of non-member err = %v, want ErrNotMember", err)
	}
	if err := r.SetMemberRole(ctx, "team-a", "bob", Role("root")); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("bad role err = %v, want ErrInvalidRole", err)
	}
}

// TestRemoveMemberRevokesKeysAndGuardsLastAdmin confirms removal deletes the
// membership AND revokes every key the removed member held in that team (so they
// can no longer connect), leaves other members' keys untouched, refuses to remove
// the last admin, and reports ErrNotMember for a non-member.
func TestRemoveMemberRevokesKeysAndGuardsLastAdmin(t *testing.T) {
	db := newMemberDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	seed(t, db, "team-a", "alice", string(RoleAdmin))
	seed(t, db, "team-a", "bob", string(RoleWriter))
	// Bob holds two active keys; Alice holds one that must survive Bob's removal.
	for _, id := range []string{"kb1", "kb2"} {
		if err := db.Create(&APIKey{ID: id, TeamID: "team-a", UserID: "bob", TokenHash: id, CreatedAt: "2026-06-28T00:00:00Z"}).Error; err != nil {
			t.Fatalf("seed bob key: %v", err)
		}
	}
	if err := db.Create(&APIKey{ID: "ka", TeamID: "team-a", UserID: "alice", TokenHash: "ka", CreatedAt: "2026-06-28T00:00:00Z"}).Error; err != nil {
		t.Fatalf("seed alice key: %v", err)
	}

	if err := r.RemoveMember(ctx, "team-a", "bob"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := r.MembershipRole(ctx, "bob", "team-a"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("bob still a member after removal: %v", err)
	}
	var bobActive, aliceActive int64
	db.Model(&APIKey{}).Where("user_id = ? AND revoked_at IS NULL", "bob").Count(&bobActive)
	db.Model(&APIKey{}).Where("user_id = ? AND revoked_at IS NULL", "alice").Count(&aliceActive)
	if bobActive != 0 {
		t.Fatalf("bob active keys = %d, want 0 (revoked on removal)", bobActive)
	}
	if aliceActive != 1 {
		t.Fatalf("alice active keys = %d, want 1 (untouched)", aliceActive)
	}
	// The sole remaining admin cannot be removed.
	if err := r.RemoveMember(ctx, "team-a", "alice"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("remove sole admin err = %v, want ErrLastAdmin", err)
	}
	if err := r.RemoveMember(ctx, "team-a", "ghost"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("remove non-member err = %v, want ErrNotMember", err)
	}
}

// TestListMembers confirms the join returns each member with their identity and
// role, scoped to the team.
func TestListMembers(t *testing.T) {
	db := newMemberDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	seedUser(t, db, "u-alice", "alice@example.com")
	seedUser(t, db, "u-bob", "bob@example.com")
	seed(t, db, "team-a", "u-alice", string(RoleAdmin))
	seed(t, db, "team-a", "u-bob", string(RoleWriter))
	seed(t, db, "team-b", "u-bob", string(RoleAdmin)) // other team, must not leak

	members, err := r.ListMembers(ctx, "team-a")
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	for _, m := range members {
		if m.Email == "" || m.UserID == "" || m.Role == "" {
			t.Fatalf("member missing joined identity: %+v", m)
		}
	}
}
