package skill

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestApplyEditsResolvesEveryAnchorAgainstTheOriginal is the promise a caller
// depends on to author more than one edit at a time: the second anchor is found
// in the text the caller READ, not in the result of the first edit.
//
// Without it a caller would have to predict an intermediate body the server never
// showed anyone, which is unauthorable — and the failure would be silent, because
// an anchor that has drifted usually still matches somewhere.
func TestApplyEditsResolvesEveryAnchorAgainstTheOriginal(t *testing.T) {
	// The first edit deletes text that sits BEFORE the second anchor, so an
	// implementation re-scanning after each edit would still find it — but at a
	// different offset. The giveaway is the result, not the error.
	const body = "alpha beta gamma delta"
	got, err := ApplyEdits(body, []Edit{
		{Old: "alpha ", New: ""},
		{Old: "gamma", New: "GAMMA"},
	})
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}
	if want := "beta GAMMA delta"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestApplyEditsRefusesAnAmbiguousAnchor pins the rule that makes an anchored
// edit safe at all. Replacing the first of several matches is the quiet wrong
// answer: it succeeds, it reports success, and it changed a paragraph the caller
// was not looking at.
func TestApplyEditsRefusesAnAmbiguousAnchor(t *testing.T) {
	_, err := ApplyEdits("note: x\nnote: y\n", []Edit{{Old: "note", New: "NOTE"}})
	if !errors.Is(err, ErrAnchorAmbiguous) {
		t.Fatalf("got %v, want ErrAnchorAmbiguous", err)
	}
	// The refusal has to be actionable: a caller cannot fix "it matched more than
	// once" without knowing how many, and both remedies are worth naming.
	for _, want := range []string{"2 times", "count=2", "unique"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %s", want, err)
		}
	}
}

// TestApplyEditsReplacesEveryOccurrenceWhenCountAgrees is the deliberate escape
// from the uniqueness rule — and it is guarded by the same count, so a caller
// that miscounted is refused rather than surprised.
func TestApplyEditsReplacesEveryOccurrenceWhenCountAgrees(t *testing.T) {
	got, err := ApplyEdits("a x a x a", []Edit{{Old: "a", New: "b", Count: 3}})
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}
	if want := "b x b x b"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := ApplyEdits("a x a", []Edit{{Old: "a", New: "b", Count: 3}}); !errors.Is(err, ErrAnchorCount) {
		t.Errorf("a wrong count: got %v, want ErrAnchorCount", err)
	}
}

// TestApplyEditsRefusesOverlappingEdits covers the case order alone would
// silently resolve. Two edits over the same text express an intention only their
// author knows, and applying whichever sorts first is a guess wearing a result.
func TestApplyEditsRefusesOverlappingEdits(t *testing.T) {
	_, err := ApplyEdits("abcdef", []Edit{
		{Old: "abcd", New: "X"},
		{Old: "cdef", New: "Y"},
	})
	if !errors.Is(err, ErrEditsOverlap) {
		t.Fatalf("got %v, want ErrEditsOverlap", err)
	}
}

// TestApplyEditsRefusesAnAnchorThatIsNotThere is the ordinary stale-anchor case:
// the caller edited a body that has since changed, or mistyped the anchor. It
// must fail rather than apply somewhere else, which is the property that made
// text addressing preferable to line numbers.
func TestApplyEditsRefusesAnAnchorThatIsNotThere(t *testing.T) {
	_, err := ApplyEdits("hello world", []Edit{{Old: "goodbye", New: "hi"}})
	if !errors.Is(err, ErrAnchorNotFound) {
		t.Fatalf("got %v, want ErrAnchorNotFound", err)
	}
	if !strings.Contains(err.Error(), "goodbye") {
		t.Errorf("refusal does not quote the anchor that missed: %s", err)
	}
}

// TestApplyEditsRefusesAnEmptyAnchor: the empty string matches at every offset,
// so an edit carrying one is not an edit — it is an unbounded insert wherever the
// scanner happens to stop.
func TestApplyEditsRefusesAnEmptyAnchor(t *testing.T) {
	if _, err := ApplyEdits("body", []Edit{{Old: "", New: "x"}}); !errors.Is(err, ErrEmptyAnchor) {
		t.Fatalf("got %v, want ErrEmptyAnchor", err)
	}
	if _, err := ApplyEdits("body", nil); !errors.Is(err, ErrNoEdits) {
		t.Fatalf("no edits: got %v, want ErrNoEdits", err)
	}
}

// TestIndexAllCountsNonOverlappingOccurrences pins the counting rule the `count`
// argument is checked against. "aa" in "aaaa" is two by this reckoning and three
// if overlaps count — a caller who counted by eye must not be told it was wrong.
func TestIndexAllCountsNonOverlappingOccurrences(t *testing.T) {
	if got := indexAll("aaaa", "aa"); len(got) != 2 {
		t.Errorf("indexAll(\"aaaa\", \"aa\") = %v; want 2 occurrences", got)
	}
}

// TestPatchLeavesTheDescriptionAlone is the reason Patch exists as its own path
// rather than as Update with a computed body. A caller changing one paragraph is
// saying nothing about the summary, and the whole-body path reads an omitted
// description as "clear it".
func TestPatchLeavesTheDescriptionAlone(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.skills["team1|guide"] = Skill{
		TeamID: "team1", Name: "guide", Description: "the house guide",
		Content: "step one\nstep two\n", Version: 3,
	}
	svc := NewService(store)
	sk, err := svc.Patch(ctx, fakeCaller{team: "team1", user: "u1", write: true}, "guide",
		[]Edit{{Old: "step two", New: "step two, corrected"}}, 0)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if sk.Description != "the house guide" {
		t.Errorf("description = %q; want it untouched", sk.Description)
	}
	if want := "step one\nstep two, corrected\n"; sk.Content != want {
		t.Errorf("content = %q, want %q", sk.Content, want)
	}
	if sk.Version != 4 {
		t.Errorf("version = %d; want the write to bump it to 4", sk.Version)
	}
}

// TestPatchRefusesAStaleVersion covers the guard a partial edit needs and a
// whole-body replace does not: an edit list was authored against one body and
// means nothing against another, so the server must be able to say so rather than
// apply anchors that happen to still match.
func TestPatchRefusesAStaleVersion(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.skills["team1|guide"] = Skill{TeamID: "team1", Name: "guide", Content: "body", Version: 7}
	svc := NewService(store)
	_, err := svc.Patch(ctx, fakeCaller{team: "team1", user: "u1", write: true}, "guide",
		[]Edit{{Old: "body", New: "BODY"}}, 5)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("got %v, want ErrVersionMismatch", err)
	}
	// Both numbers, because "it changed" without them tells the caller nothing
	// about whether their read was one write behind or twenty.
	for _, want := range []string{"v5", "v7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %s: %s", want, err)
		}
	}
	if got := store.skills["team1|guide"]; got.Content != "body" || got.Version != 7 {
		t.Errorf("a refused patch wrote anyway: %+v", got)
	}
}

// TestPatchIsNotACreatePath: patching a skill that does not exist has no body to
// anchor against, and inventing one would turn a mistyped name into a new skill
// nobody asked for.
func TestPatchIsNotACreatePath(t *testing.T) {
	svc := NewService(newFakeStore())
	_, err := svc.Patch(context.Background(), fakeCaller{team: "team1", user: "u1", write: true},
		"absent", []Edit{{Old: "a", New: "b"}}, 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestPatchHonoursTheRoleGate holds the patch path to the same authorization as
// the replace path. A second write path is a second place the role check can be
// forgotten, which is exactly what happened to this repository's other duplicated
// mechanisms.
func TestPatchHonoursTheRoleGate(t *testing.T) {
	store := newFakeStore()
	store.skills["team1|guide"] = Skill{TeamID: "team1", Name: "guide", Content: "body", Version: 1}
	svc := NewService(store)
	_, err := svc.Patch(context.Background(), fakeCaller{team: "team1", user: "u1", write: false},
		"guide", []Edit{{Old: "body", New: "BODY"}}, 0)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
	if store.skills["team1|guide"].Content != "body" {
		t.Error("a refused patch wrote anyway")
	}
}

// TestPatchRefusesEditsThatEmptyTheBody applies Update's bounds to the RESULT
// rather than to the input. Deleting a body down to nothing is a valid sequence of
// edits and an invalid skill, and only the result can tell the difference.
func TestPatchRefusesEditsThatEmptyTheBody(t *testing.T) {
	store := newFakeStore()
	store.skills["team1|guide"] = Skill{TeamID: "team1", Name: "guide", Content: "gone", Version: 1}
	svc := NewService(store)
	_, err := svc.Patch(context.Background(), fakeCaller{team: "team1", user: "u1", write: true},
		"guide", []Edit{{Old: "gone", New: ""}}, 0)
	if !errors.Is(err, ErrInvalidContent) {
		t.Fatalf("got %v, want ErrInvalidContent", err)
	}
}
