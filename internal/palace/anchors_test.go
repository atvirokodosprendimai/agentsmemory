package palace

import (
	"context"
	"testing"
)

// TestAnchorsDieWithTheirDrawer: an anchor that outlives its memory makes verify
// report drift on a sentence nobody can read any more — a warning about nothing,
// which is the fastest way to teach people to ignore warnings.
func TestAnchorsDieWithTheirDrawer(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-anchors"

	drawers := mustAdd(t, svc, team, AddInput{Wing: "wing_x", Room: "decisions", Content: "why the installer pins the config dir"})
	id := drawers[0].ID
	if _, err := svc.AddAnchors(ctx, team, id, []AnchorInput{{Path: "installer.go", Snippet: "func pinConfigDir() bool {"}}); err != nil {
		t.Fatalf("add anchors: %v", err)
	}
	if got, err := svc.ListAnchors(ctx, team, AnchorFilter{}); err != nil || len(got) != 1 {
		t.Fatalf("listed %d anchor(s), err %v; want 1", len(got), err)
	}

	if _, err := svc.Delete(ctx, team, id); err != nil {
		t.Fatalf("delete drawer: %v", err)
	}
	got, err := svc.ListAnchors(ctx, team, AnchorFilter{})
	if err != nil {
		t.Fatalf("list anchors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("anchor outlived its drawer: %+v", got)
	}
}

// TestAddAnchorsKeepsExistingVerdicts: re-filing a memory teaches the system
// nothing new about the code, so it must not erase what verification already
// found. Resetting to unchecked would silently clear a drift flag.
func TestAddAnchorsKeepsExistingVerdicts(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-verdicts"

	drawers := mustAdd(t, svc, team, AddInput{Wing: "wing_x", Room: "decisions", Content: "pinned memory"})
	in := []AnchorInput{{Path: "x.go", Snippet: "func target() {}"}}
	if _, err := svc.AddAnchors(ctx, team, drawers[0].ID, in); err != nil {
		t.Fatalf("add anchors: %v", err)
	}
	anchors, _ := svc.ListAnchors(ctx, team, AnchorFilter{})
	if _, err := svc.MarkAnchors(ctx, team, []AnchorVerdict{{ID: anchors[0].ID, Status: AnchorDrifted, Line: 0}}); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// Same anchor filed again — the verdict must survive.
	if _, err := svc.AddAnchors(ctx, team, drawers[0].ID, in); err != nil {
		t.Fatalf("re-add anchors: %v", err)
	}
	after, _ := svc.ListAnchors(ctx, team, AnchorFilter{})
	if len(after) != 1 {
		t.Fatalf("re-adding duplicated the anchor: %d rows", len(after))
	}
	if after[0].Status != AnchorDrifted || !after[0].Stale() {
		t.Errorf("verdict reset by re-filing: %+v", after[0])
	}
}

// TestMarkAnchorsRejectsUnknownStatus keeps the column a closed set: a typo from
// a client must not become a status nothing knows how to read.
func TestMarkAnchorsRejectsUnknownStatus(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.MarkAnchors(context.Background(), "t", []AnchorVerdict{{ID: "x", Status: "probably-fine"}}); err == nil {
		t.Fatal("want an error for an unknown status")
	}
}

// TestReplaceAnchorsSwapsRatherThanAppends pins the semantics a correction
// needs.
//
// A memory that is corrected keeps its old anchor unless something replaces it —
// and the old anchor pins the OLD text, so the staleness check that exists to
// protect the memory is what marks the correction out of date. Appending would
// leave both live and the dead one still checked. Reported from a live session
// that rewrote a memory and watched it stay flagged STALE.
func TestReplaceAnchorsSwapsRatherThanAppends(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	d := mustAddOne(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a memory about some code"})
	if _, err := svc.AddAnchors(ctx, team, d.ID, []AnchorInput{
		{Path: "internal/old.go", Snippet: "func Old() {}"},
	}); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	n, err := svc.ReplaceAnchors(ctx, team, d.ID, []AnchorInput{
		{Path: "internal/new.go", Snippet: "func New() {}"},
	})
	if err != nil {
		t.Fatalf("ReplaceAnchors: %v", err)
	}
	if n != 1 {
		t.Errorf("replaced %d anchor(s), want 1", n)
	}
	got, err := svc.ListAnchors(ctx, team, AnchorFilter{})
	if err != nil {
		t.Fatalf("ListAnchors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("the drawer has %d anchor(s) after a replace, want 1 — the old one survived and "+
			"will keep marking the corrected memory stale", len(got))
	}
	if got[0].Path != "internal/new.go" {
		t.Errorf("the surviving anchor is %s, want internal/new.go", got[0].Path)
	}

	// An empty set clears them, which is the honest option when a rewrite no
	// longer points at any particular code.
	if n, err := svc.ReplaceAnchors(ctx, team, d.ID, nil); err != nil || n != 0 {
		t.Fatalf("clearing: n=%d err=%v", n, err)
	}
	if got, err := svc.ListAnchors(ctx, team, AnchorFilter{}); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Errorf("%d anchor(s) survived an empty replace", len(got))
	}
}

// TestAnchorFilterSelectsByPath is the retrieval T2's PreToolUse cue rests on,
// and the reason that cue is not ADR-041's stopped T5.
//
// T5 died on QUERY QUALITY: at PreToolUse the only thing available is a bare grep
// pattern, and a bare identifier retrieves a session's narrative more often than
// a team's decision. This filter issues no query. An anchor is an exact pin — the
// path is stored beside the drawer id — so the lookup is a join on a string the
// tool call already names, and nothing is ranked. There is no distance to fall
// short of, which is why the stopped finding does not reach it.
//
// The empty-Path case is asserted alongside because the field is additive: every
// existing caller passes the zero value, and a filter that narrowed on "" would
// silently return nothing to the verifier and to doctor.
func TestAnchorFilterSelectsByPath(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-anchor-path"

	seed := func(content, path string) {
		drawers := mustAdd(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: content})
		if _, err := svc.AddAnchors(ctx, team, drawers[0].ID,
			[]AnchorInput{{Repo: "agentsmemory", Path: path, Snippet: "func Example() {"}}); err != nil {
			t.Fatalf("anchor %s: %v", path, err)
		}
	}
	seed("why the rebind guard decides this machine", "internal/auth/origin.go")
	seed("why chunks overlap by 320 runes", "internal/palace/chunk.go")
	seed("a second memory about the guard", "internal/auth/origin.go")

	all, err := svc.ListAnchors(ctx, team, AnchorFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("the zero-value filter returned %d anchors, want 3 — an additive field must not narrow by default", len(all))
	}

	got, err := svc.ListAnchors(ctx, team, AnchorFilter{Path: "internal/auth/origin.go"})
	if err != nil {
		t.Fatalf("list by path: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Path filter returned %d anchors, want the 2 pinned to that file", len(got))
	}
	for _, a := range got {
		if a.Path != "internal/auth/origin.go" {
			t.Errorf("Path filter returned an anchor on %s", a.Path)
		}
	}

	none, err := svc.ListAnchors(ctx, team, AnchorFilter{Path: "internal/nothing/pins/this.go"})
	if err != nil {
		t.Fatalf("list by unpinned path: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("a path nothing pins returned %d anchors; the cue must be silent there", len(none))
	}
}
