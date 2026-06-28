package palace

import (
	"context"
	"strings"
	"testing"
)

// TestDiaryWriteAndReadNewestFirst is the core round-trip: two entries for one
// agent come back newest-first, with the agent normalized to lowercase and the
// default topic applied.
func TestDiaryWriteAndReadNewestFirst(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	first, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "Claude", Entry: "SESSION:1|scaffold.built|★"})
	if err != nil {
		t.Fatalf("write first: %v", err)
	}
	if first.Agent != "claude" {
		t.Fatalf("agent not lowercased: %q", first.Agent)
	}
	if first.Topic != DefaultDiaryTopic {
		t.Fatalf("default topic not applied: %q", first.Topic)
	}
	if _, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "Claude", Entry: "SESSION:2|diary.added|★★"}); err != nil {
		t.Fatalf("write second: %v", err)
	}

	// Read with a differently-cased agent name to prove case-insensitive recall.
	res, err := svc.ReadDiary(ctx, team, "CLAUDE", "", 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res.Agent != "claude" {
		t.Fatalf("read agent not lowercased: %q", res.Agent)
	}
	if res.Total != 2 || res.Showing != 2 || len(res.Entries) != 2 {
		t.Fatalf("want total=2 showing=2 entries=2, got total=%d showing=%d entries=%d", res.Total, res.Showing, len(res.Entries))
	}
	// Newest first: the second write must lead, and timestamps must be
	// non-increasing (the fixed-width layout makes the string sort chronological).
	if res.Entries[0].Content != "SESSION:2|diary.added|★★" {
		t.Fatalf("newest entry should lead, got %q", res.Entries[0].Content)
	}
	if res.Entries[0].Timestamp < res.Entries[1].Timestamp {
		t.Fatalf("entries not sorted newest-first: %q then %q", res.Entries[0].Timestamp, res.Entries[1].Timestamp)
	}
}

// TestDiaryAppendsIdenticalEntries proves a journal is append-only: writing the
// same text twice yields two distinct entries (timestamp-seeded ids), unlike
// add_drawer's content-hashed idempotent ids which would collapse them into one.
func TestDiaryAppendsIdenticalEntries(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	a, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "claude", Entry: "the same reflection"})
	if err != nil {
		t.Fatalf("write a: %v", err)
	}
	b, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "claude", Entry: "the same reflection"})
	if err != nil {
		t.Fatalf("write b: %v", err)
	}
	if a.EntryID == b.EntryID {
		t.Fatal("identical entries collapsed to one id; a journal must keep both")
	}
	res, err := svc.ReadDiary(ctx, team, "claude", "", 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("want 2 distinct entries, got %d", res.Total)
	}
}

// TestDiaryWingScope checks the wing default (wing_<agent>) and the read contract:
// an explicit wing narrows the read, while an empty wing spans every wing the
// agent has journaled in (so hook-written project-wing entries are not siloed).
func TestDiaryWingScope(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// One in the agent's default wing, one in an explicit project wing.
	if _, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "claude", Entry: "default-wing entry"}); err != nil {
		t.Fatalf("write default: %v", err)
	}
	if _, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "claude", Entry: "project-wing entry", Wing: "wing_proj"}); err != nil {
		t.Fatalf("write project: %v", err)
	}

	all, err := svc.ReadDiary(ctx, team, "claude", "", 10)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if all.Total != 2 {
		t.Fatalf("empty wing should span all wings; want 2, got %d", all.Total)
	}

	scoped, err := svc.ReadDiary(ctx, team, "claude", "wing_proj", 10)
	if err != nil {
		t.Fatalf("read scoped: %v", err)
	}
	if scoped.Total != 1 || scoped.Entries[0].Content != "project-wing entry" {
		t.Fatalf("wing filter wrong: total=%d entries=%+v", scoped.Total, scoped.Entries)
	}
}

// TestDiaryReadClampsLastN verifies last_n bounds the returned page while Total
// still reports the full count, so a caller can tell its journal is larger.
func TestDiaryReadClampsLastN(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	for _, e := range []string{"one", "two", "three"} {
		if _, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "claude", Entry: e}); err != nil {
			t.Fatalf("write %q: %v", e, err)
		}
	}
	res, err := svc.ReadDiary(ctx, team, "claude", "", 2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res.Showing != 2 || len(res.Entries) != 2 {
		t.Fatalf("last_n should bound the page to 2, got %d", res.Showing)
	}
	if res.Total != 3 {
		t.Fatalf("total should report the full count 3, got %d", res.Total)
	}
}

// TestDiaryOversizedEntryChunksVerbatim proves the diary splitter preserves the
// entry byte-for-byte: an oversized entry (with surrounding spaces) becomes
// multiple chunks that reassemble, in chunk-id order, to exactly the original —
// no trimming, no overlap — matching the frozen Python stride.
func TestDiaryOversizedEntryChunksVerbatim(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	entry := "  " + strings.Repeat("abcdefghij", 200) + "  " // ~2004 runes incl. edge spaces
	res, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "claude", Entry: entry})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if res.Chunks < 2 {
		t.Fatalf("oversized entry should chunk into >1, got %d", res.Chunks)
	}
	if len(res.ChunkIDs) != res.Chunks {
		t.Fatalf("chunk_ids should list all %d chunks, got %d", res.Chunks, len(res.ChunkIDs))
	}

	var sb strings.Builder
	for _, id := range res.ChunkIDs {
		d, err := svc.Get(ctx, team, id)
		if err != nil {
			t.Fatalf("get chunk %s: %v", id, err)
		}
		sb.WriteString(d.Content)
	}
	if sb.String() != entry {
		t.Fatal("chunks did not reassemble to the verbatim entry — no-trim/no-overlap broken")
	}
}

// TestDiaryWriteRejectsBadInput confirms the service refuses input the frozen
// sanitizers reject — an empty entry and a path-traversal agent name.
func TestDiaryWriteRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "claude", Entry: "   "}); err == nil {
		t.Fatal("expected error for blank entry")
	}
	if _, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "../etc", Entry: "x"}); err == nil {
		t.Fatal("expected error for path-traversal agent name")
	}
}
