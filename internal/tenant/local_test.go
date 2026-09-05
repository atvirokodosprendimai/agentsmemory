package tenant

import (
	"context"
	"errors"
	"testing"

	appdb "github.com/atvirokodosprendimai/agentsmemory/db"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newMigratedDB returns a throwaway SQLite database with the real embedded
// schema applied. EnsureLocalWorkspace depends on rows migrations seed (the
// plan_unlimited catalog entry) and on the teams→plans foreign key, so a
// hand-rolled table subset would not exercise what production runs.
func newMigratedDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	// A single shared in-memory connection: SQLite drops the schema when the last
	// connection closes, so pin the pool for the test's lifetime.
	sqlDB.SetMaxOpenConns(1)
	goose.SetBaseFS(appdb.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Closed because the handle is the TEST's, not the process's. POSIX unlinks
	// an open file so this leaks invisibly here; Windows refuses, and t.TempDir
	// registers RemoveAll at call time, so every test using the helper fails in
	// cleanup with its assertions passing (#162). Cleanup is LIFO, so this runs
	// before TempDir's own.
	t.Cleanup(func() { _ = sqlDB.Close() })

	return gdb
}

// TestEnsureLocalWorkspaceProvisions covers the whole contract of self-hosted
// provisioning in one pass: the workspace lands with the expected slug, an admin
// member, the uncapped plan, and — the point of local mode — NO API key, since
// /mcp is unauthenticated there and a stored credential would be a secret nobody
// presents.
func TestEnsureLocalWorkspaceProvisions(t *testing.T) {
	gdb := newMigratedDB(t)
	r := NewRepo(gdb)
	ctx := context.Background()

	got, err := r.EnsureLocalWorkspace(ctx)
	if err != nil {
		t.Fatalf("EnsureLocalWorkspace: %v", err)
	}
	if got.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", got.Role)
	}

	var team Team
	if err := gdb.Where("id = ?", got.TeamID).First(&team).Error; err != nil {
		t.Fatalf("load team: %v", err)
	}
	if team.Slug != LocalSlug {
		t.Errorf("slug = %q, want %q", team.Slug, LocalSlug)
	}
	if team.PlanID == nil || *team.PlanID != UnlimitedPlanID {
		t.Errorf("plan = %v, want %q", team.PlanID, UnlimitedPlanID)
	}

	// The uncapped plan is what keeps a self-hosted server off the meter;
	// usage.Allow enforces only when the cap is > 0.
	if cap, err := r.MonthlyCap(ctx, got.TeamID); err != nil || cap != -1 {
		t.Errorf("MonthlyCap = (%d, %v), want (-1, nil)", cap, err)
	}

	var keys int64
	if err := gdb.Model(&APIKey{}).Count(&keys).Error; err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if keys != 0 {
		t.Errorf("api_keys = %d, want 0 (local mode mints no credential)", keys)
	}

	var members int64
	if err := gdb.Model(&Membership{}).Where("team_id = ? AND role = ?", got.TeamID, string(RoleAdmin)).Count(&members).Error; err != nil {
		t.Fatalf("count members: %v", err)
	}
	if members != 1 {
		t.Errorf("admin memberships = %d, want 1", members)
	}
}

// TestEnsureLocalWorkspaceIsIdempotent confirms a restart adopts the existing
// workspace rather than provisioning a second one — the server calls this on
// every boot against the same database file.
func TestEnsureLocalWorkspaceIsIdempotent(t *testing.T) {
	gdb := newMigratedDB(t)
	r := NewRepo(gdb)
	ctx := context.Background()

	first, err := r.EnsureLocalWorkspace(ctx)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := r.EnsureLocalWorkspace(ctx)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first != second {
		t.Errorf("second boot = %+v, want the same tenant as the first (%+v)", second, first)
	}

	var teams int64
	if err := gdb.Model(&Team{}).Count(&teams).Error; err != nil {
		t.Fatalf("count teams: %v", err)
	}
	if teams != 1 {
		t.Errorf("teams = %d, want 1", teams)
	}
}

// TestEnsureLocalWorkspaceRefusesForeignWorkspace is the safety property behind
// the mode: pointing --local at a database that already holds someone else's
// workspace (the demo seed, or a real multi-tenant file) must fail closed rather
// than expose that data through an unauthenticated endpoint.
func TestEnsureLocalWorkspaceRefusesForeignWorkspace(t *testing.T) {
	gdb := newMigratedDB(t)
	r := NewRepo(gdb)
	ctx := context.Background()

	for _, slug := range []string{"demo", "acme"} {
		t.Run(slug, func(t *testing.T) {
			gdb.Where("1 = 1").Delete(&Team{})
			if err := gdb.Create(&Team{
				ID: uuid.NewString(), Name: slug, Slug: slug, Kind: "personal",
				CreatedAt: "2026-08-16T00:00:00Z",
			}).Error; err != nil {
				t.Fatalf("seed foreign team: %v", err)
			}
			if _, err := r.EnsureLocalWorkspace(ctx); !errors.Is(err, ErrForeignWorkspace) {
				t.Fatalf("err = %v, want ErrForeignWorkspace", err)
			}
		})
	}
}

// TestEnsureLocalWorkspaceRejectsOrphan covers the hand-edited database: a local
// team with no admin leaves no user to act as, and silently minting a new one
// would paper over a database someone has already broken.
func TestEnsureLocalWorkspaceRejectsOrphan(t *testing.T) {
	gdb := newMigratedDB(t)
	r := NewRepo(gdb)

	if err := gdb.Create(&Team{
		ID: uuid.NewString(), Name: "Local", Slug: LocalSlug, Kind: "personal",
		CreatedAt: "2026-08-16T00:00:00Z",
	}).Error; err != nil {
		t.Fatalf("seed local team: %v", err)
	}
	if _, err := r.EnsureLocalWorkspace(context.Background()); !errors.Is(err, ErrLocalWorkspaceOrphaned) {
		t.Fatalf("err = %v, want ErrLocalWorkspaceOrphaned", err)
	}
}
