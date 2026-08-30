package main

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"
)

// seededPlaybook runs the real seed against a fresh database and returns the row
// an agent would actually receive from am_skillset.
//
// Through the seed, not off the constant. A strings.Contains against
// skillset.DefaultPlaybook passes just as happily when seedGlobalSkillset stops
// running, which is this repository's signature defect — the component exercised
// instead of the selection — and it is the shape that let the whole seed path
// ship with no test at all.
func seededPlaybook(t *testing.T) skillset.Skillset {
	t.Helper()
	svc, err := buildServices(directMCPConfig(t))
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	sqlDB, err := svc.gdb.DB()
	if err != nil {
		t.Fatalf("SQL handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := skillset.NewRepo(svc.gdb)
	if err := seedGlobalSkillset(t.Context(), repo); err != nil {
		t.Fatalf("seed the global skillset: %v", err)
	}
	stored, err := repo.Get(t.Context())
	if err != nil {
		t.Fatalf("read the seeded playbook back: %v", err)
	}
	return stored
}

// TestSeededPlaybookRoutesToTheEntryProtocol pins the one thing a waking agent
// cannot work out for itself: that a team's own entry protocol is conventionally a
// skill named start-here, and that it should be loaded before the rest.
//
// Measured 2026-08-27: the live hosted preamble carried this routing and NO
// shipped artifact did — not skillset.DefaultPlaybook, not the client bootstrap
// protocol. The routing therefore existed only in an edited database row, which no
// test pinned, no seed restored, and no MCP tool could write. A fresh or restored
// database woke every agent with no way to find the entry protocol at all.
func TestSeededPlaybookRoutesToTheEntryProtocol(t *testing.T) {
	stored := seededPlaybook(t)

	if !strings.Contains(stored.Content, "start-here") {
		t.Errorf("the seeded wakeup playbook never names the start-here skill, so an agent "+
			"waking against a fresh database is told to list skills but not which one is "+
			"the way in. That routing lived only in an edited hosted row until now:\n\n%s",
			stored.Content)
	}
	// The name is useless without the call that fetches it: routing to start-here
	// while never naming am_load_skill would tell an agent what to look for and not
	// how to get it.
	if !strings.Contains(stored.Content, "am_load_skill") {
		t.Errorf("the seeded playbook routes to a skill but never names am_load_skill, so the "+
			"routing has no call behind it:\n\n%s", stored.Content)
	}
}

// TestSeedingNeverOverwritesAnAuthoredPlaybook pins the guarantee the routing
// change puts at risk.
//
// The obvious way to propagate a fix to this text is to make the seed write every
// boot. That would silently discard a superadmin's edits on the next restart, on
// every workspace, and the loss would be invisible — the tool keeps answering with
// a plausible playbook. Seeding only when unset is what makes the constant a
// STARTING point rather than the live document.
func TestSeedingNeverOverwritesAnAuthoredPlaybook(t *testing.T) {
	svc, err := buildServices(directMCPConfig(t))
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	sqlDB, err := svc.gdb.DB()
	if err != nil {
		t.Fatalf("SQL handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := skillset.NewRepo(svc.gdb)
	const authored = "# a superadmin wrote this and it must survive a restart"
	if _, err := repo.Set(t.Context(), authored, "superadmin@example.test"); err != nil {
		t.Fatalf("author a playbook: %v", err)
	}

	// A restart: the seed runs again against a database that already holds one.
	if err := seedGlobalSkillset(t.Context(), repo); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	stored, err := repo.Get(t.Context())
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Content != authored {
		t.Errorf("seeding overwrote an authored playbook — a superadmin's edits are lost on "+
			"every restart, invisibly, because the tool keeps answering with a plausible "+
			"document.\nwant: %q\ngot:  %q", authored, stored.Content)
	}
}
