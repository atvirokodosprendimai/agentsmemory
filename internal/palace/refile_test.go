package palace

import (
	"context"
	"testing"
)

// ADR-038 T3. Dedup moves to the content key, new rows get an opaque id, and a
// re-file ENDS what it dropped instead of deleting it.

// TestRefilingAnUnchangedSourceKeepsItsIdsAndAnchors is the regression guard, and
// it is the reason the opaque mint and the purgeSource change are ONE commit.
//
// Ids are deterministic before this task, so a re-file of unchanged content
// re-inserts the same ids and every reference survives by accident. Mint an
// opaque id with the delete-all purge still in place and every re-file of a named
// source re-keys every drawer under it — breaking exactly the resolvability this
// decision exists to protect.
//
// The anchor half is red BEFORE this task too: DeleteBySource strips anchors, so
// an unchanged chunk comes back with the same id and no anchor. 39 of the 41
// anchored drawers in the live palace are exposed to that.
func TestRefilingAnUnchangedSourceKeepsItsIdsAndAnchors(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-refile"
	in := AddInput{Wing: "w", Room: "r", SourceFile: "doc.md", Content: "one memory, filed twice"}

	first, err := svc.Add(ctx, team, in)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	id := first.Drawers[0].ID
	if _, err := svc.AddAnchors(ctx, team, id, []AnchorInput{{Path: "internal/x.go", Snippet: "func X() {}"}}); err != nil {
		t.Fatalf("anchor: %v", err)
	}

	second, err := svc.Add(ctx, team, in)
	if err != nil {
		t.Fatalf("re-file: %v", err)
	}
	if second.Drawers[0].ID != id {
		t.Errorf("re-filing UNCHANGED content changed the id: %s -> %s.\n"+
			"Every code_anchor, tunnel, kg_triples.source_drawer_id and parent_id pointing at the old "+
			"one is now dangling — the exact resolvability this decision exists to protect",
			short12(id), short12(second.Drawers[0].ID))
	}
	anchors, err := svc.AnchorsForDrawers(ctx, team, []string{id})
	if err != nil {
		t.Fatalf("read anchors: %v", err)
	}
	if len(anchors[id]) != 1 {
		t.Errorf("the anchor did not survive the re-file (%d remain). A chunk the re-file did not "+
			"change must keep its pin: DeleteBySource stripping anchors is why 39 of 41 anchored "+
			"drawers are one re-file from losing theirs", len(anchors[id]))
	}
}

func TestRefilingTheOriginalTextDoesNotRevertAnEdit(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-refile2"

	res, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "the original"})
	id := res.Drawers[0].ID
	edited := "the correction"
	up, err := svc.Update(ctx, team, id, DrawerPatch{Content: &edited, Reason: "the original was wrong"})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "the original"}); err != nil {
		t.Fatalf("re-file the original: %v", err)
	}
	d, _ := svc.Get(ctx, team, up.Drawer.ID)
	if d.Content != "the correction" {
		t.Errorf("re-filing the ORIGINAL text reverted the edit (content=%q).\n"+
			"Before this task the original's hash WAS the row's id, so the upsert overwrote the "+
			"correction and reported success", d.Content)
	}
}

func TestRefilingTheEditedTextDoesNotDuplicate(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-refile3"

	res, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "before"})
	id := res.Drawers[0].ID
	edited := "after"
	up, err := svc.Update(ctx, team, id, DrawerPatch{Content: &edited, Reason: "the first version was wrong"})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	id = up.Drawer.ID
	if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "after"}); err != nil {
		t.Fatalf("re-file the edited text: %v", err)
	}
	rows, _ := svc.repo.CurrentDrawers(ctx, team, "w")
	if len(rows) != 1 {
		t.Errorf("got %d current rows holding the same text; before this task the edited content "+
			"hashed to a DIFFERENT id than the row carried, so a second row was inserted beside it", len(rows))
	}
	if len(rows) == 1 && rows[0].ID != id {
		t.Errorf("the surviving row is %s, not the original %s — dedup matched but replaced the "+
			"identity, which is the thing that must not move", short12(rows[0].ID), short12(id))
	}
}

func TestAbsorbDrawersStaysIdempotentOnTheContentKey(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-absorb"
	batch := []ImportDrawer{
		{Wing: "w", Room: "r", SourceFile: "x.md", ChunkIndex: 0, Content: "imported one"},
		{Wing: "w", Room: "r", SourceFile: "x.md", ChunkIndex: 1, Content: "imported two"},
	}
	if _, err := svc.AbsorbDrawers(ctx, team, batch); err != nil {
		t.Fatalf("first absorb: %v", err)
	}
	before, _ := svc.repo.CurrentDrawers(ctx, team, "w")
	if _, err := svc.AbsorbDrawers(ctx, team, batch); err != nil {
		t.Fatalf("second absorb: %v", err)
	}
	after, _ := svc.repo.CurrentDrawers(ctx, team, "w")
	if len(after) != len(before) {
		t.Errorf("re-running an import duplicated rows: %d -> %d.\n"+
			"AbsorbDrawers calls SaveUnembedded EXCLUSIVELY (import.go never calls Save), so moving "+
			"only Save's conflict target leaves every import re-run duplicating a whole palace",
			len(before), len(after))
	}
}

// TestFilingContentAnotherWriterAlreadyHoldsUpsertsOntoIt is what kills a mutant
// that reverts Save's conflict target to the id.
//
// mintOrReuse resolves an existing id before the insert, so on the ordinary path
// the target never fires and reverting it changes nothing observable — measured
// 2026-08-27, that mutant SURVIVED the rest of this task's fence. The target
// earns its keep exactly when a row holding the key was NOT visible to the
// resolve: a concurrent writer, or a row written by another path between the
// lookup and the insert. Simulated deterministically here by planting the row
// with a foreign id first.
//
// With the target on the content key the insert upserts onto that row. With it on
// the id, the fresh id collides with nothing, the INSERT proceeds, and the partial
// unique index rejects the whole write.
func TestFilingContentAnotherWriterAlreadyHoldsUpsertsOntoIt(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-race"
	const content = "a memory two writers reach at once"

	key := DrawerID(team, "w", "r", "", 0, content)
	if err := svc.repo.db.Exec(
		`INSERT INTO drawers (team_id,id,wing,room,source_file,chunk_index,content,entities,parent_id,filed_at,content_date,agent,topic,valid_to,superseded_by,ended_reason,ended_at,content_key)
		 VALUES (?,?,?,?,'',0,?,'','','2026-01-01T00:00:00Z','','','','','','','',?)`,
		team, "foreign-writer-id", "w", "r", content, key).Error; err != nil {
		t.Fatalf("plant the row: %v", err)
	}

	if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: content}); err != nil {
		t.Fatalf("filing content another writer already holds must upsert onto that row, not fail: %v", err)
	}
	rows, err := svc.repo.CurrentDrawers(ctx, team, "w")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d current rows for one content key; the conflict target must collapse them", len(rows))
	}
	if len(rows) == 1 && rows[0].ID != "foreign-writer-id" {
		t.Errorf("the surviving row is %s, not the one that was already there — an upsert must keep "+
			"the EXISTING name so anything already pointing at it still resolves", rows[0].ID)
	}
}
