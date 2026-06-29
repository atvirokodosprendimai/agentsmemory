package palace

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// TestCopyWing verifies a wing snapshot across tenants: only the named wing moves,
// vectors are reused (copied rows are immediately searchable, nothing pending),
// the source is untouched, closets come along, and a re-run is idempotent.
func TestCopyWing(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const from, to = "team-from", "team-to"

	// Source has two wings; only "alpha" is shared.
	if _, err := svc.AbsorbDrawers(ctx, from, []ImportDrawer{
		{Wing: "alpha", Room: "r", SourceFile: "a.md", ChunkIndex: 0, Content: "alpha knowledge about widgets"},
		{Wing: "alpha", Room: "r", SourceFile: "a.md", ChunkIndex: 1, Content: "more alpha widget detail"},
		{Wing: "beta", Room: "r", SourceFile: "b.md", ChunkIndex: 0, Content: "beta private stuff, not shared"},
	}); err != nil {
		t.Fatalf("absorb: %v", err)
	}
	if _, err := svc.EmbedPendingForTeam(ctx, from, 100); err != nil {
		t.Fatalf("embed: %v", err)
	}

	// Seed a closet in the source alpha wing (row + vector) to exercise the closet
	// copy path without the full mining setup.
	closet := Closet{
		ID: importClosetID(from, "alpha", "r", "a.md", "widgets|alpha|->d1"), TeamID: from,
		Wing: "alpha", Room: "r", SourceFile: "a.md", Document: "widgets|alpha|->d1", FiledAt: "2026-01-01T00:00:00Z",
	}
	if err := svc.repo.SaveClosets(ctx, []Closet{closet}); err != nil {
		t.Fatalf("seed closet: %v", err)
	}
	cvecs, err := svc.embed.Embed(ctx, []string{closet.Document})
	if err != nil {
		t.Fatalf("embed closet: %v", err)
	}
	if err := svc.vectors.Upsert(ctx, closetNamespace(from), []store.Point{{ID: closet.ID, Vector: cvecs[0]}}); err != nil {
		t.Fatalf("seed closet vector: %v", err)
	}

	// --- the copy ---
	res, err := svc.CopyWing(ctx, from, to, "alpha")
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if res.Drawers != 2 || res.Closets != 1 || res.Skipped != 0 {
		t.Errorf("result = %+v, want drawers=2 closets=1 skipped=0", res)
	}

	// Destination got alpha (2 drawers + 1 closet), NOT beta.
	if a, _ := svc.List(ctx, to, "alpha", "", 100, 0); len(a) != 2 {
		t.Errorf("dest alpha drawers = %d, want 2", len(a))
	}
	if b, _ := svc.List(ctx, to, "beta", "", 100, 0); len(b) != 0 {
		t.Errorf("dest beta drawers = %d, want 0 (beta not shared)", len(b))
	}
	if dc, _ := svc.repo.ClosetsByWing(ctx, to, "alpha"); len(dc) != 1 {
		t.Errorf("dest alpha closets = %d, want 1", len(dc))
	}

	// Copied drawers are searchable immediately — vectors were reused, none pending.
	if p, _ := svc.PendingCount(ctx, to); p != 0 {
		t.Errorf("dest pending = %d, want 0 (vectors copied, not re-embedded)", p)
	}
	if hits, err := svc.Search(ctx, to, SearchQuery{Query: "alpha widgets"}); err != nil || len(hits) == 0 {
		t.Errorf("search dest = %d hits (err %v), want the copied wing searchable", len(hits), err)
	}

	// Source is untouched: both wings still present.
	if a, _ := svc.List(ctx, from, "alpha", "", 100, 0); len(a) != 2 {
		t.Errorf("source alpha = %d, want 2 (untouched)", len(a))
	}
	if b, _ := svc.List(ctx, from, "beta", "", 100, 0); len(b) != 1 {
		t.Errorf("source beta = %d, want 1 (untouched)", len(b))
	}

	// Idempotent: re-copy does not duplicate.
	if _, err := svc.CopyWing(ctx, from, to, "alpha"); err != nil {
		t.Fatalf("re-copy: %v", err)
	}
	if a, _ := svc.List(ctx, to, "alpha", "", 100, 0); len(a) != 2 {
		t.Errorf("dest alpha after re-copy = %d, want 2 (idempotent)", len(a))
	}
}

// TestCopyWingValidation covers the guard rails.
func TestCopyWingValidation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	if _, err := svc.CopyWing(ctx, "a", "a", "w"); err == nil {
		t.Error("same source+dest team: want error")
	}
	if _, err := svc.CopyWing(ctx, "", "b", "w"); err == nil {
		t.Error("empty source: want error")
	}
	if _, err := svc.CopyWing(ctx, "a", "b", ""); err == nil {
		t.Error("empty wing: want error")
	}
}
