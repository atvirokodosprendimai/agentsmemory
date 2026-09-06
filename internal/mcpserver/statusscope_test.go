package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// fixtureWings are names taken from the repository's own declared example list.
//
// ⚠ NOT GENERATED. TestNoRealProjectNamesInWings refuses any wing name that is
// not declared, because a wing name is a PROJECT name and committing a real one
// leaks it. The first version of this fixture built names with a generator and
// was refused — correctly, since the gate cannot know a synthetic name from a
// customer's. Naming the rejected string here would be refused too, which is the
// gate reading comments as well as code, and right to.
var fixtureWings = []string{
	"wing_a",
	"wing_abc",
	"wing_delta",
	"wing_epsilon",
	"wing_zeta",
	"wing_eta",
	"wing_theta",
	"wing_acme",
	"wing_acme-billing",
	"wing_no_such_place",
	"wing_acme_laravel",
	"wing_acme-legacy",
	"wing_acme-old",
	"wing_acmee",
	"wing_agentmemories",
	"wing_alpha",
	"wing_anchor",
	"wing_anything",
	"wing_api",
	"wing_app",
	"wing_atlas",
	"wing_atomic",
	"wing_b",
	"wing_beta",
}

// taxonomyFixture is a palace shaped like the one that motivated #306: many wings,
// each carrying a full room breakdown, of which the caller is in exactly one.
func taxonomyFixture(wings, roomsEach int) []palace.TaxonomyWing {
	if wings > len(fixtureWings) {
		panic("taxonomyFixture: more wings requested than declared example names")
	}
	out := make([]palace.TaxonomyWing, 0, wings)
	for w := 0; w < wings; w++ {
		name := fixtureWings[w]
		tw := palace.TaxonomyWing{Wing: name, Drawers: 100 + w, Memories: 50 + w}
		for r := 0; r < roomsEach; r++ {
			tw.Rooms = append(tw.Rooms, palace.RoomStat{
				Wing: name, Room: "room" + string(rune('a'+r%26)), Drawers: r + 1, Memories: r,
			})
		}
		out = append(out, tw)
	}
	return out
}

// TestStatusServesOtherWingsWithoutTheirRooms pins the shape AND the saving.
//
// am_status is the call the wake-up protocol mandates first, so its cost lands
// once per session per project before any work happens. Measured against this
// palace: 11,995 bytes, 10,145 of them the wings array — 20 wings each with a full
// room-by-room breakdown, 19 of which belong to projects the caller is not in.
//
// Both halves are asserted because either alone is satisfied by a broken
// implementation: "the caller's rooms survive" alone is satisfied by changing
// nothing, and "other wings shrink" alone is satisfied by dropping every wing,
// which would destroy the identity check the tool exists for.
func TestStatusServesOtherWingsWithoutTheirRooms(t *testing.T) {
	full := taxonomyFixture(20, 12)
	own := fixtureWings[0]

	scoped := scopeTaxonomy(full, own)

	if len(scoped) != len(full) {
		t.Fatalf("scoping returned %d wings from %d.\n"+
			"  Every wing's NAME must survive: start-here tells sessions the wing list is how "+
			"they confirm which palace answered, and that an absent wing means nobody has "+
			"written it yet rather than that they are in the wrong palace. Dropping wings "+
			"deletes that check to save a rounding error.", len(scoped), len(full))
	}

	var ownRooms, otherRooms int
	for _, w := range scoped {
		if w.Wing == own {
			ownRooms = len(w.Rooms)
			continue
		}
		otherRooms += len(w.Rooms)
		if w.Drawers == 0 || w.Memories == 0 {
			t.Errorf("%s lost its counts; name AND counts are what keep the identity check "+
				"usable when the rooms are gone", w.Wing)
		}
	}
	if ownRooms != 12 {
		t.Errorf("the caller's own wing kept %d rooms, want 12 — the one wing a session is "+
			"actually working in must arrive whole", ownRooms)
	}
	if otherRooms != 0 {
		t.Errorf("%d room(s) from other wings survived; those are the 84%% this exists to "+
			"remove", otherRooms)
	}

	// The saving is the point, so it is measured rather than assumed. A change that
	// keeps the shape and loses the reduction has not fixed anything.
	before, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(scoped)
	if err != nil {
		t.Fatal(err)
	}
	if len(after)*2 > len(before) {
		t.Errorf("scoped payload is %d bytes against %d unscoped — less than half saved, so "+
			"the room breakdowns are still travelling in some form", len(after), len(before))
	}
	t.Logf("wings array: %d bytes unscoped, %d scoped (%d%% saved)",
		len(before), len(after), 100-100*len(after)/len(before))
}

// TestStatusWithNoResolvableWingServesEverything holds the fallback.
//
// A registration made without a wing has no "own wing" to serve in full, and
// inventing one would be the guess #305 records the cost of: a wrong wing is worse
// than a wide answer, because it is silently wrong. Those callers keep today's
// payload, which is exactly the population that cannot benefit from scoping.
func TestStatusWithNoResolvableWingServesEverything(t *testing.T) {
	full := taxonomyFixture(5, 4)
	scoped := scopeTaxonomy(full, "")
	for i, w := range scoped {
		if len(w.Rooms) != len(full[i].Rooms) {
			t.Fatalf("an unresolvable wing lost room detail on %s: scoping fired where there "+
				"is no wing to scope TO, which silently narrows the answer for the callers "+
				"least able to notice", w.Wing)
		}
	}
}

// TestStatusScopingLeavesTheCallersTaxonomyUnshared guards a defect the two tests
// above cannot see: scopeTaxonomy edits a slice the caller may still be holding.
//
// The wings arrive from drawers.GetTaxonomy and Rooms is a slice header copied by
// value, so nil-ing it on the copy is safe — but a future edit that mutated an
// element in place would corrupt the source for every later reader, and the shape
// assertions above would still pass because they only read the returned slice.
func TestStatusScopingLeavesTheCallersTaxonomyUnshared(t *testing.T) {
	full := taxonomyFixture(3, 5)
	roomsBefore := len(full[1].Rooms)

	_ = scopeTaxonomy(full, full[0].Wing)

	if got := len(full[1].Rooms); got != roomsBefore {
		t.Errorf("scoping emptied the SOURCE taxonomy's rooms (%d, was %d): the caller's own "+
			"data was mutated, so anything reading it after am_status sees a palace with no "+
			"rooms in it", got, roomsBefore)
	}
}
