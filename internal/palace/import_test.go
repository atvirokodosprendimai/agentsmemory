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
			Wing: "forumchat", Room: "backend", SourceFile: "notes.md", ChunkIndex: 2,
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

	n, err := svc.ImportDrawers(ctx, team, records)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported %d drawers, want 2", n)
	}

	// Provenance and dates are preserved verbatim, not re-derived.
	id := DrawerID(team, "forumchat", "backend", "notes.md", 2,
		"the websocket hub fans out messages to subscribers")
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

	// Imported drawers are searchable by this server's embedder.
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "websocket hub fan out messages"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected imported drawer to be searchable")
	}

	// Re-import upserts rather than duplicates (idempotent migration re-runs).
	if _, err := svc.ImportDrawers(ctx, team, records); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	list, err := svc.List(ctx, team, "forumchat", "", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("forumchat drawers after re-import = %d, want 1 (idempotent)", len(list))
	}
}

// TestImportDrawersSkipsUnaddressable verifies a blank wing/room/content record
// is skipped (not fatal) so one bad row cannot abort a large migration.
func TestImportDrawersSkipsUnaddressable(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	n, err := svc.ImportDrawers(ctx, "team-1", []ImportDrawer{
		{Wing: "", Room: "backend", Content: "no wing"},
		{Wing: "proj", Room: "", Content: "no room"},
		{Wing: "proj", Room: "backend", Content: "   "},
		{Wing: "proj", Room: "backend", Content: "this one is fine"},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported %d, want 1 (three unaddressable skipped)", n)
	}
}
