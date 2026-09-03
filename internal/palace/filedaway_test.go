package palace

import (
	"context"
	"strings"
	"testing"
)

// TestFiledAwayCountsLiveMemoriesNotEveryRowEverWritten pins the two independent
// inflations the summary shipped with.
//
// Measured 2026-09-03 against this project's own palace: `am_memories_filed_away`
// answered "3460 memories filed across 19 wings and 46 rooms" where the live
// figures were 3292 current ROWS and 1142 memories — 168 ended rows counted as
// filed, and a 3.0x overstatement against the word the sentence actually uses.
// Both come from one query that filters nothing and collapses nothing
// (FiledAwaySummary), sitting two functions away from Repo.Wings, which already
// does both correctly.
//
// It matters because of WHERE the number is read. This is the tool an agent calls
// to ask what the team has stored, and its own sentence says "memories" — so a
// session is told the palace holds three times what a reader would find, and told
// that retracted records are still filed. A count that overstates recall is the
// one direction that stops a session writing.
func TestFiledAwayCountsLiveMemoriesNotEveryRowEverWritten(t *testing.T) {
	svc := newTestService(t)
	const testTeamID = "team-counting"
	ctx := context.Background()

	// One short memory: one row, one memory.
	if _, err := svc.Add(ctx, testTeamID, AddInput{
		Wing: "wing_acme", Room: "decisions", Content: "a single-chunk memory",
	}); err != nil {
		t.Fatalf("add short: %v", err)
	}

	// One memory that chunks: several rows, still ONE memory. This is the axis
	// Repo.Wings collapses with memoryKeyExpr and the summary did not.
	long := strings.Repeat("a chunking memory about retrieval and ranking. ", 120)
	chunked, err := svc.Add(ctx, testTeamID, AddInput{
		Wing: "wing_acme", Room: "decisions", Content: long,
	})
	if err != nil {
		t.Fatalf("add long: %v", err)
	}
	if len(chunked.Drawers) < 2 {
		t.Fatalf("fixture did not chunk (%d rows); it cannot exercise the collapse", len(chunked.Drawers))
	}

	// One memory that is then RETRACTED. An ended record is history: it is not
	// filed any more, and reporting it as filed is the second inflation.
	retracted, err := svc.Add(ctx, testTeamID, AddInput{
		Wing: "wing_acme", Room: "incidents", Content: "a memory that gets retracted",
	})
	if err != nil {
		t.Fatalf("add retracted: %v", err)
	}
	if err := svc.InvalidateDrawer(ctx, testTeamID, retracted.Drawers[0].ID, "superseded by the test"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	got, err := svc.MemoriesFiledAway(ctx, testTeamID)
	if err != nil {
		t.Fatalf("MemoriesFiledAway: %v", err)
	}

	// Two memories survive: the short one and the chunked one.
	if got.Count != 2 {
		t.Errorf("Count = %d, want 2 — one short memory plus one chunked memory, with the retracted one excluded", got.Count)
	}
	// The row count is reported too rather than hidden, which is the shape
	// am_kg_stats already uses and the shape the audit that found this asked for:
	// not "hide the dead" but "say which number you are giving".
	if got.Drawers < 3 {
		t.Errorf("Drawers = %d, want at least 3 live rows (1 short + >=2 chunks)", got.Drawers)
	}
	if got.Drawers <= got.Count {
		t.Errorf("Drawers (%d) must exceed Count (%d) here; if they are equal the chunk collapse is not happening", got.Drawers, got.Count)
	}
	// The retracted memory was the only occupant of its room, so a room count
	// that includes ended rows would still report it.
	if got.Rooms != 1 {
		t.Errorf("Rooms = %d, want 1 — the incidents room holds only a retracted memory and is not a place to read", got.Rooms)
	}

	// The sentence is what an agent actually reads, so it must not say "memories"
	// over a row count.
	if !strings.Contains(got.Message, "2 memories") {
		t.Errorf("Message = %q, want it to say 2 memories", got.Message)
	}
}
