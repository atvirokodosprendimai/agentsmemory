package palace

import (
	"context"
	"testing"
)

// TestImportDrawersVerbatim verifies the migration path preserves the source
// palace's provenance and dates (rather than re-deriving them like Add does),
// files diary entries through the same path, makes imported drawers searchable,
// and is idempotent on re-import.
func TestImportDrawersVerbatim(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	records := []ImportDrawer{
		{
			Wing: "acme", Room: "backend", SourceFile: "notes.md", ChunkIndex: 2,
			Content:     "the websocket hub fans out messages to subscribers",
			Entities:    []string{"websocket", "hub"},
			FiledAt:     "2026-01-02T03:04:05Z",
			ContentDate: "2026-01-01",
		},
		{
			// A diary entry: same path, distinguished by Room + Agent/Topic.
			Wing: "wing_claude", Room: "diary", SourceFile: "",
			Content: "SESSION:2026-01-02|built.the.import.path|*warm*",
			Agent:   "claude", Topic: "general", FiledAt: "2026-01-02T10:00:00Z",
		},
	}

	n, err := svc.AbsorbDrawers(ctx, team, records)
	if err != nil {
		t.Fatalf("absorb: %v", err)
	}
	if n != 2 {
		t.Fatalf("absorbed %d drawers, want 2", n)
	}

	// Absorb writes rows only (no vectors) — both are queued for background embedding.
	if pend, err := svc.PendingCount(ctx, team); err != nil || pend != 2 {
		t.Fatalf("pending after absorb = %d (err %v), want 2", pend, err)
	}

	// Provenance and dates are preserved verbatim, not re-derived.
	//
	// Looked up by CONTENT KEY rather than by a derived id: after ADR-038 the id is
	// opaque and computing it here would be the coupling that decision removes.
	// DrawerID is now the key recipe, which is why the same call still locates the
	// row — through a different column.
	id := mustIDByContentKey(t, svc, team, DrawerID(team, "acme", "backend", "notes.md", 2,
		"the websocket hub fans out messages to subscribers"))
	got, err := svc.Get(ctx, team, id)
	if err != nil {
		t.Fatalf("get imported drawer: %v", err)
	}
	if got.FiledAt != "2026-01-02T03:04:05Z" {
		t.Errorf("FiledAt = %q, want preserved 2026-01-02T03:04:05Z", got.FiledAt)
	}
	if got.ContentDate != "2026-01-01" {
		t.Errorf("ContentDate = %q, want preserved 2026-01-01", got.ContentDate)
	}
	if got.ChunkIndex != 2 {
		t.Errorf("ChunkIndex = %d, want preserved 2", got.ChunkIndex)
	}
	if len(got.Entities) != 2 {
		t.Errorf("Entities = %v, want the 2 source entities preserved", got.Entities)
	}

	// The diary entry is readable through diary_read, proving it landed on the
	// diary path (room=diary, agent scoped) without a parallel store.
	dr, err := svc.ReadDiary(ctx, team, "claude", "", 10)
	if err != nil {
		t.Fatalf("read diary: %v", err)
	}
	if dr.Total != 1 {
		t.Fatalf("diary total = %d, want 1", dr.Total)
	}

	// Draining the background embedding queue makes the absorbed drawers searchable
	// by this server's embedder, and leaves nothing pending.
	if _, err := svc.EmbedPendingForTeam(ctx, team, 100); err != nil {
		t.Fatalf("embed pending: %v", err)
	}
	if pend, err := svc.PendingCount(ctx, team); err != nil || pend != 0 {
		t.Fatalf("pending after embed = %d (err %v), want 0", pend, err)
	}
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "websocket hub fan out messages"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected embedded drawer to be searchable")
	}

	// Re-absorb upserts rather than duplicates (idempotent migration re-runs).
	if _, err := svc.AbsorbDrawers(ctx, team, records); err != nil {
		t.Fatalf("re-absorb: %v", err)
	}
	list, err := svc.List(ctx, team, "acme", "", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("acme drawers after re-import = %d, want 1 (idempotent)", len(list))
	}
}

// TestImportDrawersSkipsUnaddressable verifies a blank wing/room/content record
// is skipped (not fatal) so one bad row cannot abort a large migration.
func TestImportDrawersSkipsUnaddressable(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	n, err := svc.AbsorbDrawers(ctx, "team-1", []ImportDrawer{
		{Wing: "", Room: "backend", Content: "no wing"},
		{Wing: "proj", Room: "", Content: "no room"},
		{Wing: "proj", Room: "backend", Content: "   "},
		{Wing: "proj", Room: "backend", Content: "this one is fine"},
	})
	if err != nil {
		t.Fatalf("absorb: %v", err)
	}
	if n != 1 {
		t.Fatalf("absorbed %d, want 1 (three unaddressable skipped)", n)
	}
}

// TestAbsorbPreservesEmbeddedOnReabsorb verifies the re-run contract: re-absorbing
// an already-embedded record must NOT re-queue it (embedded_at preserved, so a
// re-run doesn't needlessly re-embed valid vectors), yet must refresh mutable
// metadata the source may have re-derived.
func TestAbsorbPreservesEmbeddedOnReabsorb(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-x"

	rec := ImportDrawer{
		Wing: "w", Room: "r", SourceFile: "s", ChunkIndex: 0,
		Content: "stable content", Entities: []string{"alpha"}, FiledAt: "2026-01-01T00:00:00Z",
	}
	if _, err := svc.AbsorbDrawers(ctx, team, []ImportDrawer{rec}); err != nil {
		t.Fatalf("absorb: %v", err)
	}
	if _, err := svc.EmbedPendingForTeam(ctx, team, 10); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if p, _ := svc.PendingCount(ctx, team); p != 0 {
		t.Fatalf("pending after embed = %d, want 0", p)
	}

	// Re-absorb the same id (content unchanged -> same hash) with changed metadata.
	rec.Entities = []string{"alpha", "beta"}
	if _, err := svc.AbsorbDrawers(ctx, team, []ImportDrawer{rec}); err != nil {
		t.Fatalf("re-absorb: %v", err)
	}

	// Must NOT be re-queued: embedded_at was preserved.
	if p, _ := svc.PendingCount(ctx, team); p != 0 {
		t.Errorf("pending after re-absorb = %d, want 0 (embedded_at must be preserved)", p)
	}
	// Metadata must be refreshed. By content key — the id is opaque after ADR-038.
	id := mustIDByContentKey(t, svc, team, DrawerID(team, "w", "r", "s", 0, "stable content"))
	got, err := svc.Get(ctx, team, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Entities) != 2 {
		t.Errorf("entities = %v, want refreshed to 2 on re-absorb", got.Entities)
	}
}

// TestAbsorbDerivesEntitiesOnlyWhenSourceGaveNone pins both halves of the import
// rule, because each half is a way to get this wrong.
//
// A source palace that already extracted entities is preserved VERBATIM — the
// migration's whole contract is that it replays what the source held, and
// re-deriving would silently rewrite another palace's data with this build's
// extractor.
//
// A source that supplied NONE is derived from the content instead. That state is
// not hypothetical: it is exactly what an export from any palace predating
// ADR-016 contains, and this repository's own palace held 0 entity-bearing
// drawers out of 359 the day before that ADR landed. Absorbing such an export
// used to file every memory permanently outside the derived graph, because
// RecomputeGraph reads the entities column and never re-extracts — so nothing
// downstream could ever repair it.
func TestAbsorbDerivesEntitiesOnlyWhenSourceGaveNone(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// Each name appears twice: extractEntities has a freq >= 2 threshold
	// (TestExtractEntitiesFrequencyThreshold), so a fixture naming them once
	// would go red whether or not the derive path runs.
	const supplied = "Redis beat Postgres for the session cache. Redis on latency, Postgres on durability."
	const derived = "Kafka beat Mongo for the event log. Kafka on ordering, Mongo on shape."

	n, err := svc.AbsorbDrawers(ctx, team, []ImportDrawer{
		{
			Wing: "wing_acme", Room: "decisions", SourceFile: "a.md",
			Content:  supplied,
			Entities: []string{"SourcesOwnWord"}, // deliberately not in the text
		},
		{
			Wing: "wing_acme", Room: "decisions", SourceFile: "b.md",
			Content: derived, // no Entities: an export from before they were stamped
		},
	})
	if err != nil {
		t.Fatalf("absorb: %v", err)
	}
	if n != 2 {
		t.Fatalf("absorbed %d, want 2", n)
	}

	stored, err := svc.List(ctx, team, "wing_acme", "decisions", 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byFile := map[string]Drawer{}
	for _, d := range stored {
		byFile[d.SourceFile] = d
	}

	a, ok := byFile["a.md"]
	if !ok {
		t.Fatalf("a.md was not stored: %+v", stored)
	}
	if !has(a.Entities, "SourcesOwnWord") {
		t.Errorf("the source supplied its own entities and they must survive verbatim: %v", a.Entities)
	}
	if has(a.Entities, "Redis") {
		t.Errorf("the source supplied entities, so this build's extractor must not add to them: %v", a.Entities)
	}

	b, ok := byFile["b.md"]
	if !ok {
		t.Fatalf("b.md was not stored: %+v", stored)
	}
	for _, want := range []string{"Kafka", "Mongo"} {
		if !has(b.Entities, want) {
			t.Errorf("the source gave no entities, so they must be derived from the content: %v", b.Entities)
		}
	}
}

// mustIDByContentKey resolves the row holding a content key. After ADR-038 a test
// cannot derive a drawer's id, and that is the point — the key is the only thing
// a caller may compute, and the id is the only thing it may point at.
func mustIDByContentKey(t *testing.T, svc *Service, team, key string) string {
	t.Helper()
	got, err := svc.repo.IDsByContentKeys(context.Background(), team, []string{key})
	if err != nil {
		t.Fatalf("look up by content key: %v", err)
	}
	id, ok := got[key]
	if !ok {
		t.Fatalf("no current row holds content key %s", short12(key))
	}
	return id
}
