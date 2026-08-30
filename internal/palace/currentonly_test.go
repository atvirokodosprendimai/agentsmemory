package palace

import (
	"context"
	"strings"
	"testing"
)

// ADR-038 T5. An ended record is unreachable by every DEFAULT route and reachable
// by one explicit one, and the live record names what it replaced and why.
//
// The default-route half is the ADR's own falsification and it has shipped broken
// once already: a multi-chunk correction left chunk 1 live with its own embedding,
// ranking ABOVE the correction that replaced it, with nothing marking it
// retracted. A memory store whose correction competes with the text it corrects is
// worse than one that refuses the edit.

// supersededFixture files a memory carrying a marker that appears NOWHERE else,
// then corrects it with text that does not carry the marker.
//
// The marker is the point. If both records answered the same query, `limit` could
// drop the ended one by ranking accident and the test would pass whether or not
// the filter ran — the same trap the ORPHAN-MARKER placement avoids one file over.
// A query for the marker has exactly one possible answer, and it is the record
// that must not come back.
func supersededFixture(t *testing.T, svc *Service, team, wing string) (oldID, newID, reason string) {
	t.Helper()
	ctx := context.Background()
	reason = "the zoetrope prototype was abandoned; the shipped design uses a fixed lens"
	first, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: "decisions",
		Content: "ZOETROPEMARKER the viewer spins the drum at twelve frames per second",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	oldID = first.Drawers[0].ID
	res, err := svc.Supersede(ctx, team, oldID, "the viewer uses a fixed lens at twelve frames per second", reason)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	return oldID, res.ID, reason
}

func TestAnEndedRecordIsReturnedByNoDefaultRoute(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-current", "wing_alpha"
	oldID, newID, _ := supersededFixture(t, svc, team, wing)

	t.Run("search", func(t *testing.T) {
		hits, err := svc.Search(ctx, team, SearchQuery{Query: "ZOETROPEMARKER drum", Wing: wing, Limit: 50})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		for _, h := range hits {
			if h.Drawer.ID == oldID || strings.Contains(h.Drawer.Content, "ZOETROPEMARKER") {
				t.Errorf("the ended record came back from a default search. It keeps its vector, so "+
					"it competes with the correction that replaced it — which is how a retracted "+
					"claim outranks its own correction: %+v", h.Drawer)
			}
		}
	})

	t.Run("list", func(t *testing.T) {
		list, err := svc.List(ctx, team, wing, "", 100, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var sawNew bool
		for _, d := range list {
			if d.ID == oldID {
				t.Errorf("the ended record is still enumerated by am_list_drawers")
			}
			if d.ID == newID {
				sawNew = true
			}
		}
		if !sawNew {
			t.Error("the correction is missing from the listing, so the filter removed the wrong record")
		}
	})

	t.Run("get", func(t *testing.T) {
		_, err := svc.Get(ctx, team, oldID)
		if err == nil {
			t.Fatal("the ended record is still fetchable by id on the default route")
		}
		// The refusal has to name the way IN, because an agent reaches this id by
		// holding the `supersedes` the correction just handed it. A bare
		// "not found" for a row that plainly exists is a dead end.
		if !strings.Contains(err.Error(), "include_history") {
			t.Errorf("the refusal does not name include_history, so an agent holding a supersedes "+
				"id has nowhere to go: %v", err)
		}
	})
}

func TestIncludeHistoryReturnsItWithItsReason(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-history", "wing_alpha"
	oldID, _, reason := supersededFixture(t, svc, team, wing)

	got, err := svc.GetAnyVersion(ctx, team, oldID)
	if err != nil {
		t.Fatalf("the history route cannot reach the ended record: %v", err)
	}
	if !strings.Contains(got.Content, "ZOETROPEMARKER") {
		t.Errorf("the ended text is gone: %q", got.Content)
	}
	if got.EndedReason != reason {
		t.Errorf("EndedReason = %q; the history route returns the WHOLE reason, untruncated — "+
			"truncation is a recall-payload concern and this route is not recall", got.EndedReason)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{
		Query: "ZOETROPEMARKER drum", Wing: wing, Limit: 50, IncludeHistory: true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var found bool
	for _, h := range hits {
		if h.Drawer.ID == oldID {
			found = true
		}
	}
	if !found {
		t.Error("include_history did not reach the ended record through search, so history is " +
			"reachable by NO route rather than exactly one")
	}

	list, err := svc.ListAnyVersion(ctx, team, wing, "", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found = false
	for _, d := range list {
		if d.ID == oldID {
			found = true
		}
	}
	if !found {
		t.Error("include_history did not reach the ended record through the listing")
	}
}

func TestTheLiveRecordCarriesWhatItReplaced(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-carry", "wing_alpha"
	oldID, newID, reason := supersededFixture(t, svc, team, wing)

	// On the DEFAULT path, and that is the correction ADR-010 made to its own
	// first draft: hiding history behind a flag AND expecting retractions to stop
	// re-litigation cannot both hold, because a session about to redo a rejected
	// thing does not know to ask for history. So the CURRENT record carries it.
	live, err := svc.Get(ctx, team, newID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if live.Supersedes != oldID {
		t.Errorf("Supersedes = %q; want %q — a live record that does not name what it replaced "+
			"leaves the next session to rediscover the rejected version by doing it", live.Supersedes, oldID)
	}
	if live.SupersededReason != reason {
		t.Errorf("SupersededReason = %q; want %q", live.SupersededReason, reason)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "fixed lens twelve frames", Wing: wing, Limit: 50})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var checked bool
	for _, h := range hits {
		if h.Drawer.ID != newID {
			continue
		}
		checked = true
		if h.Drawer.Supersedes != oldID || h.Drawer.SupersededReason != reason {
			t.Errorf("a search hit on the live record carries supersedes=%q reason=%q; recall is "+
				"where a session meets this record, so it is the route that matters most",
				h.Drawer.Supersedes, h.Drawer.SupersededReason)
		}
	}
	if !checked {
		t.Fatal("the live record was not returned by its own text, so this asserts nothing")
	}

	// A record nothing replaced carries nothing. An always-populated field reads
	// as "everything here is a correction", which is worse than silence.
	fresh, _ := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: "an original memory"})
	plain, _ := svc.Get(ctx, team, fresh.Drawers[0].ID)
	if plain.Supersedes != "" || plain.SupersededReason != "" {
		t.Errorf("a record that replaced nothing claims to: %q / %q", plain.Supersedes, plain.SupersededReason)
	}
}

func TestTheCarriedReasonIsTruncatedTo200Chars(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-trunc", "wing_alpha"

	// The payload must not grow with the corpus. A reason is free text an agent
	// writes, and nothing bounds it at the write end — so the bound belongs where
	// it is carried onto every hit of every page.
	long := strings.TrimSpace(strings.Repeat("the measurement was taken against a corpus that no longer exists ", 12))
	first, _ := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: "the original claim"})
	res, err := svc.Supersede(ctx, team, first.Drawers[0].ID, "the corrected claim", long)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}

	live, err := svc.Get(ctx, team, res.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if n := len([]rune(live.SupersededReason)); n > maxCarriedReasonRunes {
		t.Errorf("the carried reason is %d runes; the cap is %d and it is what keeps a page from "+
			"growing with the corpus", n, maxCarriedReasonRunes)
	}
	if live.SupersededReason == long {
		t.Error("the reason was carried whole")
	}
	// Truncated on a boundary and MARKED. A fragment cut mid-word is a reason
	// nobody can read, and a reason nobody can read is a reason nobody acts on.
	if !strings.HasSuffix(live.SupersededReason, "…") {
		t.Errorf("the truncation is unmarked, so a reader cannot tell a short reason from a cut "+
			"one: %q", live.SupersededReason)
	}
	if trimmed := strings.TrimSuffix(live.SupersededReason, "…"); strings.HasSuffix(trimmed, " ") ||
		!strings.HasPrefix(long, strings.TrimSpace(trimmed)) {
		t.Errorf("the cut is not on a word boundary of the original: %q", live.SupersededReason)
	}

	// Whole under the history route: the cap is about recall payload, not about
	// what the store keeps.
	ended, err := svc.GetAnyVersion(ctx, team, first.Drawers[0].ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if ended.EndedReason != long {
		t.Errorf("the stored reason was truncated too; the cap belongs to the carried copy only")
	}
}

// TestNoMemoryEndsHalfway is the shape T4 is supposed to have made impossible,
// asserted rather than assumed: a multi-chunk memory with one chunk ended and the
// others current would put the retracted half back into recall through the very
// filter that is meant to remove it.
func TestNoMemoryEndsHalfway(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-halfway", "wing_alpha"

	long := strings.Repeat("HALFWAYMARKER the retention window is thirty days and this forces chunking. ", 40)
	res, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", SourceFile: "policy", Content: long})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(res.Drawers) < 2 {
		t.Fatalf("fixture is %d chunk(s); this needs several", len(res.Drawers))
	}
	if _, err := svc.Supersede(ctx, team, res.Drawers[0].ID, "the window is ninety days", "it was extended"); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	chunks, err := svc.repo.MemoryChunks(ctx, team, res.Drawers[0].ID)
	if err != nil {
		t.Fatalf("chunks: %v", err)
	}
	for _, c := range chunks {
		if c.ValidTo == "" {
			t.Errorf("chunk %d of a superseded memory is still current; recall would return the "+
				"retracted half and nothing would mark it", c.ChunkIndex)
		}
	}
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "HALFWAYMARKER retention window", Wing: wing, Limit: 50})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, h := range hits {
		if strings.Contains(h.Drawer.Content, "HALFWAYMARKER") {
			t.Errorf("a chunk of the superseded memory is still searchable: %s", short12(h.Drawer.ID))
		}
	}
}

// TestNoDefaultRouteReturnsEndedText is the route MATRIX the first version of
// this file was missing, and every case in it is a leak a Codex review found
// while `go test ./...` was green.
//
// The lesson is the one this repository keeps relearning: three routes tested is
// not "no default route", it is three routes. Each of these reaches drawer TEXT
// by a different query, and each had to be filtered separately — a predicate
// composed into search, list and get says nothing about the fourth door.
func TestNoDefaultRouteReturnsEndedText(t *testing.T) {
	ctx := context.Background()

	t.Run("a re-file that drops a chunk leaves no ended sibling in the reassembled memory", func(t *testing.T) {
		svc := newTestService(t)
		const team, wing = "team-sib", "wing_alpha"
		// The mixed state is ROUTINE: purgeSource ends only the chunks whose content
		// key left the source, so shortening a document leaves a current root with
		// ended children. A supersede ends a memory whole; this does not — which is
		// why survivorsFrom's filter on the RETRIEVED chunk is not enough, since the
		// expansion re-reads every sibling under the root.
		//
		// ⚠ The shared prefix must exceed ChunkSize in BOTH versions, or chunk 0's
		// own content changes, its key leaves the source too, and the whole memory
		// ends — no mixed state, and this test passes with the filter deleted. That
		// is what the first draft of this fixture did.
		prefix := "the queue worker drains before restart. " + strings.Repeat("stable prefix text that does not move. ", 60)
		long := prefix + "DROPPEDMARKER the appendix explains the retry budget. " + strings.Repeat("appendix filler. ", 80)
		short := prefix + "the appendix was removed. " + strings.Repeat("kept filler. ", 20)

		first, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", SourceFile: "doc.md", Content: long})
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		if len(first.Drawers) < 2 {
			t.Fatalf("fixture is %d chunk(s); this needs several", len(first.Drawers))
		}
		if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", SourceFile: "doc.md", Content: short}); err != nil {
			t.Fatalf("re-file: %v", err)
		}

		// The fixture must actually be mixed, or the assertion below is vacuous.
		chunks, err := svc.repo.MemoryChunks(ctx, team, first.Drawers[0].ID)
		if err != nil {
			t.Fatalf("chunks: %v", err)
		}
		var current, ended int
		for _, c := range chunks {
			if c.ValidTo == "" {
				current++
			} else {
				ended++
			}
		}
		if current == 0 || ended == 0 {
			t.Fatalf("the fixture is %d current / %d ended; this test needs BOTH under one root, "+
				"or it passes with the filter deleted", current, ended)
		}

		hits, err := svc.Search(ctx, team, SearchQuery{Query: "appendix retry budget", Wing: wing, Limit: 20})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		for _, h := range hits {
			if strings.Contains(h.MemoryContent, "DROPPEDMARKER") {
				t.Errorf("the reassembled memory carries text from a chunk the re-file ENDED. "+
					"MemoryContent is what BM25 and the cross-encoder score and what the tool "+
					"returns, so a dropped chunk is still competing with what replaced it:\n%s",
					h.MemoryContent)
			}
		}
	})

	t.Run("check_duplicate does not answer with a retracted record", func(t *testing.T) {
		svc := newTestService(t)
		const team, wing = "team-dup", "wing_alpha"
		const text = "the deploy drains the scheduler before it restarts"
		first, _ := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: text})
		if err := svc.InvalidateDrawer(ctx, team, first.Drawers[0].ID, "the drain step was removed"); err != nil {
			t.Fatalf("invalidate: %v", err)
		}
		res, err := svc.CheckDuplicate(ctx, team, text, 0.9)
		if err != nil {
			t.Fatalf("check duplicate: %v", err)
		}
		if res.Drawer != nil && res.Drawer.ID == first.Drawers[0].ID {
			t.Error("check_duplicate reported the RETRACTED record as the duplicate. The team " +
				"stopped asserting that text, so re-filing it is not a duplicate — and the answer " +
				"talks an agent out of filing something the palace no longer holds")
		}
	})

	t.Run("diary_read hides a retracted entry from both the page and the total", func(t *testing.T) {
		svc := newTestService(t)
		const team, wing = "team-diaryread", "wing_alpha"
		var ids []string
		for _, entry := range []string{"DIARYMARKER the first reflection", "the second reflection"} {
			res, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Wing: wing, Agent: "claude", Topic: "general", Entry: entry})
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			ids = append(ids, res.EntryID)
		}
		if err := svc.InvalidateDrawer(ctx, team, ids[0], "that reflection was wrong"); err != nil {
			t.Fatalf("invalidate: %v", err)
		}
		read, err := svc.ReadDiary(ctx, team, "claude", wing, 50)
		if err != nil {
			t.Fatalf("read diary: %v", err)
		}
		for _, e := range read.Entries {
			if strings.Contains(e.Content, "DIARYMARKER") {
				t.Error("the retracted diary entry is still on the default read")
			}
		}
		if read.Total != int64(len(read.Entries)) {
			t.Errorf("total=%d but the page holds %d; a total counting retracted entries tells an "+
				"agent its journal is larger than anything it can read", read.Total, len(read.Entries))
		}
	})
}

// TestBootstrapAndTunnelPreviewsHideEndedText is the other half of the route
// matrix: two doors that return drawer TEXT without going through search, list or
// get, and that a predicate composed into those three says nothing about.
//
// Both were found by an independent review while the suite was green, and both
// are structurally the same mistake — a derived pointer (an entry edge, a tunnel
// endpoint) outlives the record it names, so following it reaches a retracted
// memory by a path the recall filter does not watch.
func TestBootstrapAndTunnelPreviewsHideEndedText(t *testing.T) {
	ctx := context.Background()

	t.Run("bootstrap does not inline a retracted record", func(t *testing.T) {
		svc := newTestService(t)
		const team, wing = "team-boot", "wing_alpha"
		// llm_init is the entry room: what bootstrap inlines is the FIRST thing a
		// waking session reads, which makes a retracted record here the most
		// expensive place in the palace to leave one.
		first, err := svc.Add(ctx, team, AddInput{
			Wing: wing, Room: "llm_init",
			Content: "BOOTMARKER read this before doing anything else in this wing",
		})
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		if err := svc.InvalidateDrawer(ctx, team, first.Drawers[0].ID, "the protocol changed"); err != nil {
			t.Fatalf("invalidate: %v", err)
		}
		res, err := svc.Bootstrap(ctx, team, wing)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		for _, d := range res.Eager {
			if strings.Contains(d.Content, "BOOTMARKER") {
				t.Error("bootstrap inlined a RETRACTED record as the thing to read first. An entry " +
					"edge is written when a drawer is written and outlives an ending, so nothing " +
					"upstream of the hydration removes it")
			}
		}
	})

	t.Run("a tunnel preview does not show a retracted endpoint's text", func(t *testing.T) {
		svc := newTestService(t)
		const team = "team-tun"
		near, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", Content: "the near endpoint"})
		if err != nil {
			t.Fatalf("add near: %v", err)
		}
		far, err := svc.Add(ctx, team, AddInput{
			Wing: "wing_beta", Room: "decisions", Content: "TUNNELMARKER the far endpoint explains the deploy",
		})
		if err != nil {
			t.Fatalf("add far: %v", err)
		}
		if _, err := svc.CreateTunnel(ctx, team, TunnelInput{
			SourceWing: "wing_alpha", SourceRoom: "decisions", SourceDrawerID: near.Drawers[0].ID,
			TargetWing: "wing_beta", TargetRoom: "decisions", TargetDrawerID: far.Drawers[0].ID,
			Label: "the deploy behaviour is explained over there",
		}, "2026-08-27T00:00:00Z"); err != nil {
			t.Fatalf("tunnel: %v", err)
		}
		if err := svc.InvalidateDrawer(ctx, team, far.Drawers[0].ID, "that explanation was wrong"); err != nil {
			t.Fatalf("invalidate: %v", err)
		}

		conns, err := svc.FollowTunnels(ctx, team, "wing_alpha", "decisions")
		if err != nil {
			t.Fatalf("follow: %v", err)
		}
		if len(conns) == 0 {
			t.Fatal("no connections returned, so this asserts nothing — a tunnel outlives the " +
				"drawer it names and must still be listed")
		}
		for _, c := range conns {
			if strings.Contains(c.DrawerPreview, "TUNNELMARKER") {
				t.Error("the tunnel preview shows the RETRACTED endpoint's text. The tunnel itself " +
					"is fine to list — what must not come back is the withdrawn claim")
			}
		}
	})
}

// TestTransferPathsCarryOnlyWhatIsStillAsserted pins the pair a wing bundle and a
// cross-workspace copy depend on.
//
// Neither format carries a validity window — no valid_to, no superseded_by, no
// ended_reason — so an exported ended row arrives in the destination as CURRENT:
// the text a team retracted, asserted again, with the reason it was retracted
// gone. Dropping history from a transfer loses the account of why; copying it
// loses the account AND re-asserts the claim, which is worse.
//
// Repo.List stays history-inclusive on purpose, for a future versioned format and
// for the diagnostic surfaces. ListCurrent is what the transfers use, and this
// asserts the two genuinely differ — a ListCurrent that quietly aliased List would
// make every caller's choice meaningless.
func TestTransferPathsCarryOnlyWhatIsStillAsserted(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-transfer", "wing_alpha"

	kept, _ := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: "the claim that still holds"})
	gone, _ := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: "TRANSFERMARKER the claim that was withdrawn"})
	if err := svc.InvalidateDrawer(ctx, team, gone.Drawers[0].ID, "measured wrong"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	all, err := svc.repo.List(ctx, team, wing, "", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var sawEnded bool
	for _, d := range all {
		if d.ID == gone.Drawers[0].ID {
			sawEnded = true
		}
	}
	if !sawEnded {
		t.Error("Repo.List dropped the ended row. It is history-inclusive by contract, and the " +
			"diagnostic and future-format callers depend on that — if this changes, say so at " +
			"the declaration rather than here")
	}

	current, err := svc.repo.ListCurrent(ctx, team, wing, "", 100, 0)
	if err != nil {
		t.Fatalf("list current: %v", err)
	}
	var ids []string
	for _, d := range current {
		ids = append(ids, d.ID)
		if strings.Contains(d.Content, "TRANSFERMARKER") {
			t.Error("ListCurrent carried the retracted row, so a bundle built from it would " +
				"re-assert the claim in the destination with no window to say otherwise")
		}
	}
	if len(ids) != 1 || ids[0] != kept.Drawers[0].ID {
		t.Errorf("ListCurrent returned %v; want exactly the current row %s", shortAll(ids), short12(kept.Drawers[0].ID))
	}
}

// TestAChildChunkOfACorrectionCarriesTheReason is the case a surviving mutant
// exposed: the lineage fix was real and nothing pinned it.
//
// supersedeInto stamps every ended chunk's superseded_by with the SUCCESSOR'S
// ROOT, while a search page's representative — and any chunk a caller fetches —
// is whichever chunk matched. Keyed on the row's own id, the predecessor lookup
// therefore finds nothing for exactly the multi-chunk memories a correction most
// often replaces, and the record comes back with no lineage while the invariant
// says it always carries one.
//
// The earlier lineage tests all used single-chunk memories, where root and row id
// are the same value — so they passed under both keyings. That is the shape of a
// test that cannot fail: correct about a case that cannot distinguish the fix.
func TestAChildChunkOfACorrectionCarriesTheReason(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-childlineage", "wing_alpha"
	const reason = "the retention window was extended to ninety days"

	old := strings.Repeat("the retention window is thirty days and this sentence forces chunking. ", 40)
	first, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", SourceFile: "policy", Content: old})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	corrected := strings.Repeat("CHILDMARKER the retention window is ninety days and this forces chunking. ", 40)
	res, err := svc.Supersede(ctx, team, first.Drawers[0].ID, corrected, reason)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}

	chunks, err := svc.GetMemory(ctx, team, res.ID)
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("the correction is %d chunk(s); this test needs several, or root and row id are "+
			"the same value and it cannot distinguish the two keyings", len(chunks))
	}
	for _, c := range chunks {
		if c.Supersedes != first.Drawers[0].ID {
			t.Errorf("chunk %d carries Supersedes=%q; want %q. A reader who lands on a child chunk "+
				"is the same reader the carried reason exists for",
				c.ChunkIndex, c.Supersedes, first.Drawers[0].ID)
		}
		if c.SupersededReason != reason {
			t.Errorf("chunk %d carries no reason (%q)", c.ChunkIndex, c.SupersededReason)
		}
	}

	// And through recall, where a page's representative is whichever chunk matched.
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "CHILDMARKER retention ninety", Wing: wing, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var checked bool
	for _, h := range hits {
		if memoryOf(h.Drawer) != res.ID {
			continue
		}
		checked = true
		if h.Drawer.SupersededReason != reason {
			t.Errorf("a recall hit on chunk %d of the correction carries reason=%q; recall is where "+
				"a session meets this record", h.Drawer.ChunkIndex, h.Drawer.SupersededReason)
		}
	}
	if !checked {
		t.Fatal("the correction was not returned by its own text, so the recall half asserts nothing")
	}
}
