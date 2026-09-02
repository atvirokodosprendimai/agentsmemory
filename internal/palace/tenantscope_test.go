package palace

import (
	"context"
	"strings"
	"testing"
)

// TestMemoryChunkQueriesRefuseToCrossTenants is the gate for the isolation
// control itself.
//
// The memory-level read path resolves a memory by asking for every row whose id
// OR whose parent_id names one of the requested roots. The second half is the
// dangerous one: parent_id is ordinary column data, so a row belonging to
// ANOTHER tenant that happens to carry this tenant's root as its parent matches
// the predicate on content alone. Only `team_id = ?` keeps it out — and it has
// to be on BOTH branches of the UNION, because either branch alone returns rows.
//
// What that leak would look like is not abstract: MemoryChunksByRoots feeds
// reassembleMemory, which feeds the hit's whole-memory content, which goes onto
// the wire. A tenant would receive another tenant's prose inside a memory it
// legitimately owns, with nothing in the response marking it foreign.
//
// The queries were correct when this test was written. That is exactly why the
// test exists: AGENTS.md's rule is that a test for "X holds" must fail when X is
// removed, and before this, deleting either predicate left the whole suite
// green. Mutation-proven — delete `team_id = ?` from either branch of
// memoryChunkQuery and this goes red naming the leaked content.
func TestMemoryChunkQueriesRefuseToCrossTenants(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	const (
		victim    = "team-victim"
		attacker  = "team-attacker"
		secret    = "ATTACKER-TENANT-SECRET-CONTENT"
		rootText  = "the victim's own memory, long enough to be worth reassembling"
		childText = "a second chunk that legitimately belongs to the victim"
	)

	// The victim owns a two-chunk memory, filed the ordinary way so the root and
	// its child carry the real parent_id relationship rather than a hand-built one.
	added, err := svc.Add(ctx, victim, AddInput{
		Wing: "wing_alpha", Room: "decisions", SourceFile: "victim",
		Content: strings.Repeat(rootText+" "+childText+" ", 40),
	})
	if err != nil {
		t.Fatalf("seed victim memory: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture produced %d chunks; it needs a root AND a child", len(added.Drawers))
	}
	root := added.Drawers[0].ID

	// The hostile row: another tenant's drawer whose parent_id points at the
	// victim's root. Written through the repo directly because no tool would let
	// a caller forge this — the point is what the QUERY does if such a row exists,
	// however it got there (a restore, an import, a bug, a hostile write).
	if err := svc.repo.Save(ctx, []Drawer{{
		TeamID: attacker, ID: "attacker-chunk-1", Wing: "wing_alpha", Room: "decisions",
		SourceFile: "attacker", ChunkIndex: 1, Content: secret, ParentID: root,
	}}); err != nil {
		t.Fatalf("seed hostile row: %v", err)
	}
	// And a hostile ROOT sharing the victim's id would be caught by the other
	// branch, so cover it too: same id, different tenant.
	if err := svc.repo.Save(ctx, []Drawer{{
		TeamID: attacker, ID: root, Wing: "wing_alpha", Room: "decisions",
		SourceFile: "attacker", ChunkIndex: 0, Content: secret,
	}}); err != nil {
		t.Fatalf("seed hostile root: %v", err)
	}

	t.Run("MemoryChunksByRoots", func(t *testing.T) {
		byRoot, err := svc.repo.MemoryChunksByRoots(ctx, victim, []string{root})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(byRoot[root]) == 0 {
			t.Fatal("victim cannot read its own memory; the fixture proves nothing")
		}
		for _, d := range byRoot[root] {
			if d.TeamID != victim {
				t.Errorf("returned a %s row to %s (id %s)", d.TeamID, victim, d.ID)
			}
			if strings.Contains(d.Content, secret) {
				t.Errorf("CROSS-TENANT LEAK: another tenant's content reached %s and would be "+
					"reassembled into its memory and returned on the wire (id %s)", victim, d.ID)
			}
		}
	})

	t.Run("MemoryChunkIDsByRoots", func(t *testing.T) {
		byRoot, err := svc.repo.MemoryChunkIDsByRoots(ctx, victim, []string{root})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(byRoot[root]) == 0 {
			t.Fatal("victim cannot resolve its own memory's chunk ids; the fixture proves nothing")
		}
		for _, id := range byRoot[root] {
			if id == "attacker-chunk-1" {
				t.Errorf("CROSS-TENANT LEAK: %s resolved another tenant's chunk id, which "+
					"AnchorsForMemories would then attach anchors from", victim)
			}
		}
	})

	// The attacker must not read the victim either — the predicate has to scope in
	// both directions, not merely exclude one known id.
	t.Run("the other direction", func(t *testing.T) {
		byRoot, err := svc.repo.MemoryChunksByRoots(ctx, attacker, []string{root})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		for _, d := range byRoot[root] {
			if d.TeamID != attacker {
				t.Errorf("CROSS-TENANT LEAK: %s read a %s row (id %s)", attacker, d.TeamID, d.ID)
			}
		}
	})
}

// TestGetManyRefusesToCrossTenants is the same control on the other id-driven
// read, and it is here because mutation found it unguarded.
//
// ⚠ DELETING `team_id = ?` FROM GetMany LEFT internal/palace AND internal/mcpserver
// GREEN. The sibling above was mutation-proven when it was written; this one had
// no such test, so the isolation predicate on the hydration path was resting on
// nobody having removed it yet.
//
// Today both callers pass ids that came from a scoped search — memory_search's
// hydration of missing rows and eval's context fetch — so the practical exposure
// is small. That is an argument for asserting it now rather than later: the
// filter is what KEEPS those callers safe, and the next caller to pass ids from
// anywhere else inherits the guarantee only if it is still there. A control whose
// safety depends on the habits of its current callers is one refactor from being
// a leak, and nothing would have said so.
func TestGetManyRefusesToCrossTenants(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	const (
		victim   = "team-getmany-victim"
		attacker = "team-getmany-attacker"
		secret   = "ATTACKER-ONLY-CONTENT-THAT-MUST-NOT-HYDRATE"
	)

	theirs, err := svc.Add(ctx, attacker, AddInput{
		Wing: "wing_beta", Room: "decisions", Content: secret + ", filed by another tenant entirely",
	})
	if err != nil {
		t.Fatalf("add attacker drawer: %v", err)
	}
	mine, err := svc.Add(ctx, victim, AddInput{
		Wing: "wing_alpha", Room: "decisions", Content: "the victim's own memory, which it may of course read",
	})
	if err != nil {
		t.Fatalf("add victim drawer: %v", err)
	}
	foreignID := theirs.Drawers[0].ID
	ownID := mine.Drawers[0].ID

	// The victim asks for both ids — the shape a caller produces when an id
	// reaches it from anywhere other than its own scoped search.
	got, err := svc.repo.GetMany(ctx, victim, []string{ownID, foreignID})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if _, leaked := got[foreignID]; leaked {
		t.Errorf("GetMany returned %s to %s, a drawer owned by %s. team_id is the only predicate "+
			"separating the two, and an id is not a secret: content: %q",
			foreignID, victim, attacker, got[foreignID].Content)
	}
	if _, ok := got[ownID]; !ok {
		t.Error("GetMany withheld the victim's OWN drawer; a filter that returns nothing is not " +
			"isolation, it is a broken read path")
	}
}
