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
		('plan_pro_monthly','pro_monthly','personal','Pro',5000,'eur',1000000,'month','1970-01-01T00:00:00Z')`).Error; err != nil {
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
