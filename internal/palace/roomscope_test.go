package palace

import (
	"context"
	"testing"
)

// TestASearchScopedToARoomStaysInThatRoom pins the half of the scope filter that
// nothing asserted.
//
// ⚠ FOUND BY MUTATION. drawerMatchesSearch reads
//
//	(q.Wing == "" || d.Wing == q.Wing) && (q.Room == "" || d.Room == q.Room)
//
// and deleting the ROOM half left the whole package green. The wing half is
// covered — severing it reddens TestScopeDropsCountEachCauseSeparately — so the
// filter as a whole looked tested while half of it was load-bearing on nobody
// having touched it.
//
// The room is an ordinary argument of am_search and am_list_drawers, and an agent
// that asks for `decisions` and receives `diary` does not get an error: it gets
// prose from the wrong aspect, indistinguishable from a real answer. That is the
// failure this asserts against, in both directions — the room must exclude, and
// it must not exclude the room that was asked for.
func TestASearchScopedToARoomStaysInThatRoom(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-roomscope", "wing_alpha"

	const wanted = "the ledger reconciliation decision this search is scoped to"
	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: wanted}); err != nil {
		t.Fatalf("add decisions: %v", err)
	}
	// Same wing, same vocabulary, different ROOM. Only the room separates them, so
	// a filter that ignores it returns this one too.
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: "diary",
		Content: "the ledger reconciliation as a diary note, which a room-scoped search must not return",
	}); err != nil {
		t.Fatalf("add diary: %v", err)
	}

	page, err := svc.SearchPage(ctx, team, SearchQuery{
		Wing: wing, Room: "decisions", Query: "ledger reconciliation", Limit: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Hits) == 0 {
		t.Fatal("a room-scoped search returned nothing at all; a filter that excludes everything " +
			"is not scoping, and this test would pass for the wrong reason")
	}
	for _, h := range page.Hits {
		if h.Drawer.Room != "decisions" {
			t.Errorf("a search scoped to room %q returned a hit from %q (%s). An agent that asked "+
				"for one aspect and received another gets no error — it gets prose from the wrong "+
				"room, which reads exactly like an answer", "decisions", h.Drawer.Room, h.Drawer.ID)
		}
	}
}

// TestDrawerMatchesSearchFiltersOnBothHalves pins the SECOND line of defence
// directly, because the first one hides it.
//
// searchFilter already pushes wing and room into the vector store's own filter,
// so in ordinary operation an out-of-room candidate never reaches
// drawerMatchesSearch — which is why the end-to-end test above passes with the
// room half deleted. That does not make the check ornamental: it is what holds if
// a driver ignores a filter key it does not understand, and every candidate the
// ranker sees passes through it.
//
// A defence-in-depth check is exactly the thing that rots unnoticed, so it gets a
// test at its own level rather than through a path that cannot exercise it. Both
// halves and the empty-means-unscoped cases are here, since "filters everything"
// and "filters nothing" are the two ways this function goes wrong.
func TestDrawerMatchesSearchFiltersOnBothHalves(t *testing.T) {
	d := Drawer{Wing: "wing_alpha", Room: "decisions"}
	for _, tc := range []struct {
		name string
		q    SearchQuery
		want bool
	}{
		{"unscoped matches anything", SearchQuery{}, true},
		{"its own wing and room", SearchQuery{Wing: "wing_alpha", Room: "decisions"}, true},
		{"wing only", SearchQuery{Wing: "wing_alpha"}, true},
		{"room only", SearchQuery{Room: "decisions"}, true},
		{"another wing is excluded", SearchQuery{Wing: "wing_beta"}, false},
		{"ANOTHER ROOM IS EXCLUDED", SearchQuery{Room: "diary"}, false},
		{"right wing, wrong room", SearchQuery{Wing: "wing_alpha", Room: "diary"}, false},
		{"wrong wing, right room", SearchQuery{Wing: "wing_beta", Room: "decisions"}, false},
	} {
		if got := drawerMatchesSearch(d, tc.q); got != tc.want {
			t.Errorf("%s: drawerMatchesSearch(%+v) = %v, want %v", tc.name, tc.q, got, tc.want)
		}
	}
}
