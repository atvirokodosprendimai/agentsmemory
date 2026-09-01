package palace

import (
	"context"
	"strings"
	"testing"
)

// TestBootstrapServesEveryChunkOfAnEntryRecord is ADR-046's central claim.
//
// Measured 2026-09-01 before this change, over a 3,600-rune entry record:
//
//	eager[0]: 1600 runes of 3600 total; ends "ENTRY: load the craft tier first and the"
//	truncation.omitted=0 reason=""
//
// The eager tier carried chunk 0 and stopped, and the response's own loss accounting
// said nothing had been withheld. A front door that serves 44% of the entry protocol
// and reports no omission is worse than one that fails: the session proceeds.
//
// ⚠ THE FIXTURE MOVES THE RECORD IN, and that is not a convenience. prepareWrite still
// refuses a chunking entry record until T2 deletes it, while moveMemory patches rows
// directly and never routes through prepareWrite — so the refusal guards the write path
// only. ADR-045 opened that hole; this test walks through it deliberately, because it
// is how the truncation above was reproduced and because T2 is only safe once this
// passes.
func TestBootstrapServesEveryChunkOfAnEntryRecord(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-boot-whole", "wing_alpha"

	// TrimSpace'd because ChunkText trims each piece as it stores it, so a fixture
	// with a trailing space is not what the store holds and the byte assertion below
	// would fail by exactly one rune — a real discrepancy, but at CREATION rather
	// than in reassembly, which is not what this test is about.
	long := strings.TrimSpace(strings.Repeat("ENTRY: load the craft tier first and then the project root. ", 60))
	added, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", SourceFile: "root", Content: long})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture produced %d chunk(s); this test is about a record of several rows", len(added.Drawers))
	}
	entry := EntryRoom
	if _, err := svc.Update(ctx, team, added.Drawers[0].ID, DrawerPatch{Room: &entry}); err != nil {
		t.Fatalf("move into the entry room: %v", err)
	}

	got, err := svc.Bootstrap(ctx, team, wing)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(got.Eager) == 0 {
		t.Fatal("the eager tier is empty, so this test is asserting nothing about what it serves")
	}

	var found bool
	for _, d := range got.Eager {
		if d.ID != added.Drawers[0].ID {
			continue
		}
		found = true
		// BYTE equality, not a length check. The failure this is written against is
		// the ChunkOverlap seam: a reassembly that duplicates or drops 320 runes can
		// still produce a plausible length, and a length assertion would pass over
		// text the reader cannot trust.
		if d.Content != long {
			t.Errorf("the eager tier served %d runes of %d. A session is told the entry protocol "+
				"and given part of it; the response's omitted count does not mention the rest.\n"+
				"  served ends: %q\n  filed  ends: %q",
				len([]rune(d.Content)), len([]rune(long)),
				lastRunes(d.Content, 40), lastRunes(long, 40))
		}
	}
	if !found {
		t.Fatalf("the entry record is not in the eager tier at all; %d record(s) came back", len(got.Eager))
	}
	if got.Truncation.Omitted != 0 {
		t.Errorf("truncation.omitted is %d for a record that was served whole; the count must "+
			"describe what was actually withheld or it is worse than absent", got.Truncation.Omitted)
	}
}

// TestBootstrapLeavesAShortEntryRecordUnchanged is the regression guard: reassembly
// must not change the common case, which is every entry record in the corpus today.
func TestBootstrapLeavesAShortEntryRecordUnchanged(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-boot-short", "wing_alpha"
	const short = "WHAT MUST I LOAD AT THE START OF A SESSION? The craft tier, then this project's root."

	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom, SourceFile: "root", Content: short}); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := svc.Bootstrap(ctx, team, wing)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(got.Eager) == 0 {
		t.Fatal("the eager tier is empty; a one-chunk entry record must still be served")
	}
	for _, d := range got.Eager {
		if d.Content != short {
			t.Errorf("a one-chunk record came back changed by reassembly:\n  want %q\n  got  %q", short, d.Content)
		}
	}
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return string(r)
	}
	return string(r[len(r)-n:])
}
