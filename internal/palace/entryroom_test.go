package palace

import (
	"context"
	"strings"
	"testing"
)

// TestALongEntryRecordIsAcceptedAndServedWhole is ADR-046 T2's central claim, and it
// asserts BOTH halves in one fixture on purpose.
//
// prepareWrite refused a chunking entry record because am_bootstrap served the eager
// tier one chunk at a time — the refusal's own error message named that as its reason,
// which is what made it a workaround rather than a rule. T1 fixed the serving; this
// deletes the refusal.
//
// Accepting the write WITHOUT serving it whole is the exact state the refusal existed
// to prevent, so a test that only checked the write would go green on the worst
// possible outcome. The two assertions belong together.
//
// ⚠ The refusal was ALREADY reachable around: it lives in prepareWrite, while
// moveMemory patches rows directly, so ADR-045 made it possible to file a long record
// elsewhere and move it in. Deleting it removes a guarantee that had already stopped
// being one.
func TestALongEntryRecordIsAcceptedAndServedWhole(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-entry-long", "wing_alpha"

	long := strings.TrimSpace(strings.Repeat("ENTRY: load the craft tier, then this project's root. ", 60))
	added, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom, SourceFile: "root", Content: long})
	if err != nil {
		t.Fatalf("filing a long entry record was refused: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture produced %d chunk(s); this test is about a record that chunks", len(added.Drawers))
	}

	got, err := svc.Bootstrap(ctx, team, wing)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	var served string
	for _, d := range got.Eager {
		if d.ID == added.Drawers[0].ID {
			served = d.Content
		}
	}
	if served == "" {
		t.Fatalf("the entry record is not in the eager tier; %d record(s) came back", len(got.Eager))
	}
	if served != long {
		t.Errorf("accepted the write but served %d runes of %d — the state the refusal existed to "+
			"prevent, now reachable by the front door instead of around it",
			len([]rune(served)), len([]rune(long)))
	}
}

// TestAWingRootIsMintedFromAChunkedEntryRecord covers the one reader T2's class audit
// flagged.
//
// Filing into the entry room mints the wing's by-name root, in a branch keyed on
// `d.Room == EntryRoom` inside attachDerivedEdgeTo. That branch has never been given a
// chunked record, because prepareWrite refused one until this task. It edges the ROOT
// chunk only and guards once per wing per batch, so it should be indifferent — but "it
// should be" is what the audit exists to stop anyone writing, and a wing whose root was
// never minted answers `unknown_term` to the first call this team's protocol prescribes.
func TestAWingRootIsMintedFromAChunkedEntryRecord(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-entry-root", "wing_alpha"

	long := strings.TrimSpace(strings.Repeat("ENTRY: load the craft tier, then this project's root. ", 60))
	added, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom, SourceFile: "root", Content: long})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture produced %d chunk(s); the point is a record that chunks", len(added.Drawers))
	}

	q, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: WingRootSubject(wing), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("resolve %s: %v", WingRootSubject(wing), err)
	}
	if q.Resolution == "unknown_term" {
		t.Fatalf("%s is unknown to the graph after a chunked entry record was filed; the by-name "+
			"root is the first call this team's protocol makes, and it now resolves to nothing",
			WingRootSubject(wing))
	}
	if len(q.Facts) != 1 {
		t.Errorf("the wing root has %d outgoing edge(s), want exactly 1 — a chunked record must "+
			"mint one root, not one per chunk", len(q.Facts))
	}
}
