package palace

import (
	"context"
	"strings"
	"testing"
)

// TestMineFilesDrawersAndClosets is the end-to-end happy path: a structured
// document mines into multiple drawers plus at least one closet, stamps the
// detected content date, and records entities + author on the drawers.
func TestMineFilesDrawersAndClosets(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	content := "---\ndate: 2024-11-08\n---\n# Cache Design\n\n" +
		strings.Repeat("Redis powers the cache. Redis is fast. ", 30) +
		"\n\nWe built the cache layer and deployed it.\n\n" +
		strings.Repeat("Postgres stores the source of truth. Postgres is durable. ", 30)

	res, err := svc.Mine(ctx, team, MineInput{Content: content, Wing: "proj", Source: "notes.md", Agent: "Claude"})
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	if res.Drawers < 2 {
		t.Fatalf("structured content should produce multiple drawers, got %d", res.Drawers)
	}
	if res.Closets < 1 {
		t.Fatalf("mining should build at least one closet, got %d", res.Closets)
	}
	if res.Room != DefaultMineRoom {
		t.Fatalf("default room should be %q, got %q", DefaultMineRoom, res.Room)
	}
	if res.ContentDate != "2024-11-08" {
		t.Fatalf("content date from frontmatter should be 2024-11-08, got %q", res.ContentDate)
	}

	list, err := svc.List(ctx, team, "proj", DefaultMineRoom, 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != res.Drawers {
		t.Fatalf("listed %d drawers, mine reported %d", len(list), res.Drawers)
	}
	var sawEntity, sawAgent bool
	for _, d := range list {
		if d.Agent != "claude" {
			t.Fatalf("drawer agent should be lowercased author, got %q", d.Agent)
		}
		sawAgent = true
		if len(d.Entities) > 0 {
			sawEntity = true
		}
		if d.ContentDate != "2024-11-08" {
			t.Fatalf("drawer should carry the content date, got %q", d.ContentDate)
		}
	}
	if !sawAgent || !sawEntity {
		t.Fatalf("expected drawers with author and at least one with entities (agent=%v entity=%v)", sawAgent, sawEntity)
	}
}

// TestMineIdempotentReplacesSource confirms re-mining the same source replaces it:
// a shorter re-mine leaves only the new drawers, no stale chunks.
func TestMineIdempotentReplacesSource(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	long := strings.Repeat("alpha beta gamma delta. ", 200) // many chunks
	first, err := svc.Mine(ctx, team, MineInput{Content: long, Wing: "w", Room: "r", Source: "doc"})
	if err != nil {
		t.Fatalf("mine first: %v", err)
	}
	if first.Drawers < 2 {
		t.Fatalf("expected several drawers first pass, got %d", first.Drawers)
	}

	short := "a concise replacement note that still clears the fifty character floor easily"
	second, err := svc.Mine(ctx, team, MineInput{Content: short, Wing: "w", Room: "r", Source: "doc"})
	if err != nil {
		t.Fatalf("mine second: %v", err)
	}

	// CURRENT rows, not List. After ADR-038 T3 a re-mine ENDS the chunks the source
	// dropped instead of deleting them, and List does not filter by validity until
	// T5 composes current() into every read route — so until then List legitimately
	// returns the ended ones too. Asserting on List here would be asserting the
	// old contract.
	list, err := svc.repo.CurrentDrawers(ctx, team, "w")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != second.Drawers {
		t.Fatalf("re-mine should replace; listed %d but second pass reported %d", len(list), second.Drawers)
	}
	if len(list) >= first.Drawers {
		t.Fatalf("shorter re-mine should leave fewer drawers than the first pass (%d), got %d", first.Drawers, len(list))
	}
}

// TestMineValidates rejects a missing wing and a missing source.
func TestMineValidates(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	if _, err := svc.Mine(ctx, "team-1", MineInput{Content: "x of sufficient length to clear the floor here", Wing: "", Source: "s"}); err == nil {
		t.Fatal("expected error for empty wing")
	}
	if _, err := svc.Mine(ctx, "team-1", MineInput{Content: "x of sufficient length to clear the floor here", Wing: "w", Source: ""}); err == nil {
		t.Fatal("expected error for empty source")
	}
}

// recordingEmbedder records every text it was asked to embed, so a test can see
// what a second mine actually paid for rather than only what it returned.
// (embedbound_test.go's countingEmbedder counts EmbedOne, a different question.)
type recordingEmbedder struct {
	fakeEmbedder // for EmbedOne, which mining does not use
	calls        int
	texts        []string
}

func (c *recordingEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	c.calls++
	c.texts = append(c.texts, inputs...)
	return fakeEmbedder{}.Embed(ctx, inputs)
}

// TestReMiningUnchangedContentDoesNotReEmbed pins the difference between topping
// a corpus up and rebuilding it.
//
// ⚠ A RE-MINE USED TO COST THE FIRST MINE, EVERY TIME. The embed call was
// unconditional over every chunk, while the content-key lookup twelve lines above
// it — which already knew the text was byte-identical — was used only to keep ids
// stable. Measured by an operator 2026-08-31 on a CPU-only host: adding ONE new
// session re-embedded the whole corpus, about 2.5 hours, so in practice nobody
// tops up and the corpus goes stale.
//
// Safe because content is an INPUT to the content key: a key that is already
// filed and already embedded means identical text at the same address with a
// vector that still describes it.
func TestReMiningUnchangedContentDoesNotReEmbed(t *testing.T) {
	ctx := context.Background()
	embedder := &recordingEmbedder{}
	svc := newTestServiceWith(t, embedder)
	const team, wing, room, source = "team-remine", "wing_alpha", "sessions", "claude-session/proj/abc#p1"
	// ⚠ ABOVE MineChunkMin (50), or mineChunkText yields NOTHING and the whole
	// test asserts about an empty run. The first draft used a 17-rune body: Mine
	// returned drawers=0, the embedder was never called, and the assertions below
	// were vacuously satisfied.
	const body = "the deploy races the migration and the health check wins, so the pod is " +
		"marked ready while the schema it depends on is still half applied"

	first, err := svc.Mine(ctx, team, MineInput{Wing: wing, Room: room, Source: source, Content: body})
	if err != nil {
		t.Fatalf("first mine: %v", err)
	}
	if embedder.calls == 0 {
		t.Fatal("the first mine embedded nothing, so this test would prove nothing about the second")
	}
	firstTexts := len(embedder.texts)

	before := embedder.calls
	second, err := svc.Mine(ctx, team, MineInput{Wing: wing, Room: room, Source: source, Content: body})
	if err != nil {
		t.Fatalf("re-mine: %v", err)
	}
	for _, text := range embedder.texts[firstTexts:] {
		if text == body {
			t.Errorf("the re-mine embedded the unchanged drawer text again; a corpus that is "+
				"one session newer costs a full re-embed, which is why nobody tops one up "+
				"(embed calls: %d -> %d)", before, embedder.calls)
		}
	}
	if second.Drawers != first.Drawers {
		t.Errorf("re-mine filed %d drawer(s), first filed %d — skipping the embed must not "+
			"change what is filed", second.Drawers, first.Drawers)
	}
	// The row must still be there, under the same id: reuse is only sound because
	// the vector already stored under that id describes this exact text.
	var rows []drawerRow
	if err := svc.repo.db.Where("team_id = ? AND source_file = ? AND valid_to = ''", team, source).
		Find(&rows).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("re-mine left %d current row(s) for one source, want 1", len(rows))
	}
	if rows[0].EmbeddedAt == nil {
		t.Error("the reused row has no vector, so the skip made it permanently unsearchable")
	}
}

// TestReMiningAChangedChunkDoesReEmbedIt is the falsifiability half: "skip
// everything" would pass the test above and quietly stop indexing new text.
func TestReMiningAChangedChunkDoesReEmbedIt(t *testing.T) {
	ctx := context.Background()
	embedder := &recordingEmbedder{}
	svc := newTestServiceWith(t, embedder)
	const team, wing, room, source = "team-remine-changed", "wing_alpha", "sessions", "claude-session/proj/abc#p1"

	const original = "the original text, long enough to survive MineChunkMin, which the " +
		"first draft of this test was not and so proved nothing at all"
	if _, err := svc.Mine(ctx, team, MineInput{Wing: wing, Room: room, Source: source, Content: original}); err != nil {
		t.Fatalf("first mine: %v", err)
	}
	mark := len(embedder.texts)

	const changed = "the text after an edit, still long enough to survive MineChunkMin, and " +
		"different from the original in a way the content key must notice"
	if _, err := svc.Mine(ctx, team, MineInput{Wing: wing, Room: room, Source: source, Content: changed}); err != nil {
		t.Fatalf("re-mine: %v", err)
	}
	var sawChanged bool
	for _, text := range embedder.texts[mark:] {
		if text == changed {
			sawChanged = true
		}
	}
	if !sawChanged {
		t.Errorf("changed text was NOT embedded — the reuse rule is skipping on the source "+
			"rather than on the content, so an edited session would never become searchable.\n"+
			"texts embedded by the second mine: %q", embedder.texts[mark:])
	}
}

// TestReMiningAFiledButUnembeddedRowStillEmbedsIt covers the guard that separates
// "already filed" from "already searchable".
//
// ⚠ SEVERING THE embedded_at CONDITION BROKE NOTHING, which is how this test came
// to exist: the whole suite passed with the guard removed. A drawer row can exist
// with no vector — absorb writes rows and leaves embedding to the background
// worker — so reusing on filed-ness alone would leave exactly those rows
// permanently unembedded, since the re-mine that would have fixed them is what
// got skipped.
func TestReMiningAFiledButUnembeddedRowStillEmbedsIt(t *testing.T) {
	ctx := context.Background()
	embedder := &recordingEmbedder{}
	svc := newTestServiceWith(t, embedder)
	const team, wing, room, source = "team-unembedded", "wing_alpha", "sessions", "claude-session/proj/abc#p1"
	const body = "a mined session long enough to clear MineChunkMin, filed once and then " +
		"stripped of its vector to imitate a row absorb wrote and nothing embedded"

	if _, err := svc.Mine(ctx, team, MineInput{Wing: wing, Room: room, Source: source, Content: body}); err != nil {
		t.Fatalf("first mine: %v", err)
	}
	// Imitate the absorb path: the row is filed, the vector is not there yet.
	//
	// ⚠ RAW SQL, BECAUSE gorm's Update("embedded_at", nil) IS A NO-OP HERE and the
	// first version of this test was green with the guard REMOVED — it never
	// actually cleared the column, so both variants took the same branch.
	if err := svc.repo.db.Exec(
		"UPDATE drawers SET embedded_at = NULL WHERE team_id = ? AND source_file = ?",
		team, source).Error; err != nil {
		t.Fatalf("strip the vector: %v", err)
	}
	var check []drawerRow
	if err := svc.repo.db.Where("team_id = ? AND source_file = ?", team, source).Find(&check).Error; err != nil {
		t.Fatalf("confirm the fixture: %v", err)
	}
	if len(check) == 0 {
		t.Fatal("no row to strip, so this test would prove nothing")
	}
	for _, row := range check {
		if row.EmbeddedAt != nil {
			t.Fatalf("the fixture did not clear embedded_at, so both the guarded and unguarded " +
				"code take the same branch and this test pins nothing")
		}
	}
	mark := len(embedder.texts)

	if _, err := svc.Mine(ctx, team, MineInput{Wing: wing, Room: room, Source: source, Content: body}); err != nil {
		t.Fatalf("re-mine: %v", err)
	}
	// ⚠ THE DRAWER TEXT, NOT THE COUNT. Every mine re-embeds the source's CLOSET
	// documents unconditionally, so len(texts) grows whatever the drawer path
	// did — the first version of this assertion counted that and was green with
	// the guard removed.
	var sawBody bool
	for _, text := range embedder.texts[mark:] {
		if text == body {
			sawBody = true
		}
	}
	if !sawBody {
		t.Errorf("a row that is filed but NOT embedded was skipped, so it stays unsearchable "+
			"forever — the re-mine that would have embedded it is what the reuse rule skipped.\n"+
			"texts embedded by the second mine: %q", embedder.texts[mark:])
	}
}
