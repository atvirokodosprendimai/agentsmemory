package main

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestTeamExistsSeparatesAnAssertedIdentityFromARealOne pins the read that lets
// `--team` say when it is not being recorded.
//
// The defect (#249): `--team <id>` mints a trusted local admin identity and
// creates no `teams` row. `search_events.team_id REFERENCES teams(id)` and the
// writer runs with `foreign_keys(1)` (ADR-052), so `recordSearch`'s insert fails
// the constraint — and it swallows the error on purpose, because a measurement
// that can break the thing it measures is worse than no measurement. The search
// then succeeds and records nothing, `am_recall_stats` under-counts that path,
// and nothing anywhere says why.
//
// Both directions are asserted, and the pair is the point: "reports false for an
// unknown team" alone is satisfied by a function that always says false, which
// would warn on every legitimate `--team` read and teach an operator to ignore the
// line. "Reports true for a real one" alone is satisfied by one that always says
// true, which restores the silence this exists to remove.
func TestTeamExistsSeparatesAnAssertedIdentityFromARealOne(t *testing.T) {
	gdb := newTestGormDB(t)
	repo := tenant.NewRepo(gdb)
	ctx := context.Background()

	if ok, err := repo.TeamExists(ctx, "nobody-minted-this"); err != nil || ok {
		t.Fatalf("TeamExists(unknown) = %v, %v; want false, nil.\n"+
			"  Reporting an unseeded team as present is what leaves --team recording nothing "+
			"and saying nothing — the whole of #249.", ok, err)
	}

	const id = "t-real"
	if err := gdb.Create(&tenant.Team{
		ID: id, Name: id, Slug: "real", Kind: "personal", CreatedAt: "2026-01-01T00:00:00Z",
	}).Error; err != nil {
		t.Fatalf("seed team: %v", err)
	}

	if ok, err := repo.TeamExists(ctx, id); err != nil || !ok {
		t.Fatalf("TeamExists(seeded) = %v, %v; want true, nil.\n"+
			"  A check that never finds a team warns on every legitimate --team read, which "+
			"teaches an operator to ignore the one line that matters.", ok, err)
	}
}

// TestTeamExistsWritesNothing holds the decision, not the behaviour.
//
// Issue #249 offered "ensure the row exists (insert-or-ignore)" as its first
// option, and this rejects it: minting tenancy from a READ path would turn a
// typo'd `--team` id into a new workspace, and the identity would then be real in
// the sense that matters least — a row exists — while still naming a palace nobody
// meant to open. A check that quietly acquired a write would pass every behavioural
// test above while doing exactly that, so the absence is asserted rather than
// assumed.
func TestTeamExistsWritesNothing(t *testing.T) {
	gdb := newTestGormDB(t)
	repo := tenant.NewRepo(gdb)
	ctx := context.Background()

	if _, err := repo.TeamExists(ctx, "definitely-not-a-team"); err != nil {
		t.Fatalf("TeamExists: %v", err)
	}

	var n int64
	if err := gdb.Model(&tenant.Team{}).Count(&n).Error; err != nil {
		t.Fatalf("count teams: %v", err)
	}
	if n != 0 {
		t.Errorf("asking whether a team exists created %d team row(s).\n"+
			"  This is the option #249 listed first and this change deliberately did not take: "+
			"a read path that mints tenancy makes a typo a workspace.", n)
	}
}
