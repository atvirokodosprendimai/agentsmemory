package usage

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// fakeCaps is a fixed monthly cap, standing in for tenant.Repo.
type fakeCaps struct{ cap int }

func (f fakeCaps) MonthlyCap(_ context.Context, _ string) (int, error) { return f.cap, nil }

// newTestDB returns an in-memory SQLite with the usage_counters table, matching
// the goose migration's shape, so the Repo's RETURNING upsert is exercised for
// real against the glebarez/modernc driver.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE usage_counters (
		team_id TEXT NOT NULL, period TEXT NOT NULL,
		count INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL,
		PRIMARY KEY (team_id, period))`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// TestAllowEnforcesCap confirms requests count up to the cap, then are refused
// without being counted, and that Snapshot never increments.
func TestAllowEnforcesCap(t *testing.T) {
	svc := NewService(NewRepo(newTestDB(t)), fakeCaps{cap: 3})
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		st, err := svc.Allow(ctx, "team1")
		if err != nil {
			t.Fatalf("call %d error: %v", i, err)
		}
		if !st.Allowed {
			t.Fatalf("call %d should be allowed", i)
		}
		if st.Used != i {
			t.Fatalf("call %d used=%d want %d", i, st.Used, i)
		}
	}

	st, err := svc.Allow(ctx, "team1")
	if err != nil {
		t.Fatalf("blocked call error: %v", err)
	}
	if st.Allowed {
		t.Fatal("4th call should be blocked (cap reached)")
	}
	if st.Used != 3 {
		t.Fatalf("blocked call must not increment: used=%d want 3", st.Used)
	}

	snap, err := svc.Snapshot(ctx, "team1")
	if err != nil {
		t.Fatalf("snapshot error: %v", err)
	}
	if snap.Used != 3 || snap.Cap != 3 || snap.Remaining() != 0 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}

// TestUnlimitedCapAlwaysAllows confirms a non-positive cap means no enforcement:
// both 0 (no plan / planless) and -1 (the Unlimited-plan sentinel the set-plan CLI
// attaches) are treated as "no limit", so Allow admits every request.
func TestUnlimitedCapAlwaysAllows(t *testing.T) {
	for _, cap := range []int{0, -1} {
		svc := NewService(NewRepo(newTestDB(t)), fakeCaps{cap: cap})
		st, err := svc.Allow(context.Background(), "t")
		if err != nil {
			t.Fatalf("cap %d: error: %v", cap, err)
		}
		if !st.Allowed || st.Cap != cap {
			t.Fatalf("cap %d: unlimited should allow: %+v", cap, st)
		}
	}
}
