// Package storetest holds the behaviour every VectorStore implementation must
// share, as one suite each backend runs against itself.
//
// It exists because this repository's characteristic defect is a capability that
// is finished and unreachable, and a multi-backend seam is where that hides
// best: a method added to an interface compiles for every implementation the
// moment each one has a body, whether or not any body does the thing. A suite
// that only ever runs against the convenient backend passes while another
// returns nil and changes nothing.
package storetest

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// Factory builds a fresh, empty store for one test. t is passed so a backend
// that needs a temp dir or a database can fail the test directly.
type Factory func(t *testing.T) store.VectorStore

// RunPointsConformance exercises PointsByIDs against one backend.
//
// The contract it pins: a payload written by Upsert comes back verbatim, an id
// the store does not hold is OMITTED rather than erroring (matching Delete, so a
// caller need not check existence first), and an empty id list is a no-op.
func RunPointsConformance(t *testing.T, name string, newStore Factory) {
	t.Helper()
	t.Run(name+"/PointsByIDs", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		const ns = "team-conformance"
		if err := s.EnsureNamespace(ctx, ns, 3); err != nil {
			t.Fatalf("EnsureNamespace: %v", err)
		}
		in := []store.Point{
			{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_acme", "room": "decisions"}},
			{ID: "b", Vector: []float32{0, 1, 0}, Payload: map[string]any{"wing": "wing_alpha", "room": "diary"}},
		}
		if err := s.Upsert(ctx, ns, in); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		got, err := s.PointsByIDs(ctx, ns, []string{"a", "b"})
		if err != nil {
			t.Fatalf("Points: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("PointsByIDs returned %d point(s), want 2 — a backend that cannot read a payload back "+
				"cannot be checked for drift, and a drift check that reads nothing reports clean", len(got))
		}
		byID := map[string]store.Point{}
		for _, p := range got {
			byID[p.ID] = p
		}
		for _, want := range in {
			p, ok := byID[want.ID]
			if !ok {
				t.Fatalf("PointsByIDs omitted %q, which was just written", want.ID)
			}
			for k, v := range want.Payload {
				if p.Payload[k] != v {
					t.Errorf("point %q payload[%q] = %v, want %v", want.ID, k, p.Payload[k], v)
				}
			}
			// EXACTLY the keys that were written. A driver that keeps its own
			// bookkeeping in the payload — a reserved id key, a JSON blob of the
			// whole payload beside a flattened copy of it — must hide that here
			// as it already does on Search, or the same point reads differently
			// depending on which method fetched it.
			if len(p.Payload) != len(want.Payload) {
				t.Errorf("point %q came back with payload %v; want exactly the keys written, %v — "+
					"a driver's internal keys must not reach the caller", want.ID, p.Payload, want.Payload)
			}
		}

		// An unknown id is omitted, not an error: the caller is asking what the
		// store holds, and "it holds nothing for this id" is an answer.
		mixed, err := s.PointsByIDs(ctx, ns, []string{"a", "no-such-id"})
		if err != nil {
			t.Fatalf("PointsByIDs with an unknown id: %v", err)
		}
		if len(mixed) != 1 || mixed[0].ID != "a" {
			t.Errorf("PointsByIDs with an unknown id returned %d point(s), want just %q", len(mixed), "a")
		}

		none, err := s.PointsByIDs(ctx, ns, nil)
		if err != nil || len(none) != 0 {
			t.Errorf("PointsByIDs(nil) = %d point(s), %v; want 0, nil", len(none), err)
		}
	})
}

// RunCountConformance exercises Count against one backend.
//
// Count exists for the coverage check (ADR-033): comparing the two halves'
// counts tells a caller whether the index ingested everything the source of
// truth holds. The contract pinned here: the count equals what was written,
// re-upserting an id REPLACES rather than inflates, Delete decrements, and a
// payload patch leaves the count untouched. A backend that cannot count its
// own points cannot corroborate a rebuild trigger, and the whole fallback is
// built on that comparison.
func RunCountConformance(t *testing.T, name string, newStore Factory) {
	t.Helper()
	t.Run(name+"/Count", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		const ns = "team-count"

		if err := s.EnsureNamespace(ctx, ns, 3); err != nil {
			t.Fatalf("EnsureNamespace: %v", err)
		}
		// An empty namespace is a legitimate state (a workspace before its first
		// drawer), not an error.
		n, err := s.Count(ctx, ns)
		if err != nil {
			t.Fatalf("Count on an empty namespace: %v", err)
		}
		if n != 0 {
			t.Fatalf("Count on an empty namespace = %d, want 0", n)
		}

		pts := []store.Point{
			{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_acme"}},
			{ID: "b", Vector: []float32{0, 1, 0}, Payload: map[string]any{"wing": "wing_acme"}},
			{ID: "c", Vector: []float32{0, 0, 1}},
		}
		if err := s.Upsert(ctx, ns, pts); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if n, err = s.Count(ctx, ns); err != nil || n != 3 {
			t.Fatalf("Count after Upsert = %d, want 3 (err %v)", n, err)
		}

		// Re-upserting an id replaces: the count must not inflate. A driver that
		// duplicates on Upsert would feed a coverage check a forever-growing
		// expected and never see the index catch up.
		if err := s.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_alpha"}}}); err != nil {
			t.Fatalf("re-upsert: %v", err)
		}
		if n, err = s.Count(ctx, ns); err != nil || n != 3 {
			t.Fatalf("Count after a same-id re-upsert = %d, want 3 (err %v)", n, err)
		}

		// Delete decrements.
		if err := s.Delete(ctx, ns, []string{"b"}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if n, err = s.Count(ctx, ns); err != nil || n != 2 {
			t.Fatalf("Count after Delete = %d, want 2 (err %v)", n, err)
		}

		// A payload patch is a label change, not a population change.
		if err := s.SetPayload(ctx, ns, []string{"a"}, map[string]string{"wing": "wing_beta"}); err != nil {
			t.Fatalf("SetPayload: %v", err)
		}
		if n, err = s.Count(ctx, ns); err != nil || n != 2 {
			t.Fatalf("Count after SetPayload = %d, want 2 (err %v)", n, err)
		}

		// Count is per-namespace: another team's namespace stays at zero.
		if n, err = s.Count(ctx, "team-other"); err != nil || n != 0 {
			t.Fatalf("Count on an untouched namespace = %d, want 0 (err %v)", n, err)
		}
	})
}

// The contract it pins: the patch MERGES (a field not named is untouched), the
// VECTOR survives (this is a label change, not a re-embed), an unknown id is
// ignored, and an empty id list or empty patch is a no-op.
//
// Merging rather than replacing is the assertion that matters. A wing merge
// patches `wing` and nothing else; a driver that replaces the payload would
// silently erase `room` on every point it corrected, turning a fix for one
// broken filter into a break of another.
// RunSetPayloadConformance exercises SetPayload against one backend.
func RunSetPayloadConformance(t *testing.T, name string, newStore Factory) {
	t.Helper()
	t.Run(name+"/SetPayload", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		const ns = "team-conformance"
		if err := s.EnsureNamespace(ctx, ns, 3); err != nil {
			t.Fatalf("EnsureNamespace: %v", err)
		}
		in := []store.Point{
			{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_acme-legacy", "room": "decisions"}},
			{ID: "b", Vector: []float32{0, 1, 0}, Payload: map[string]any{"wing": "wing_alpha", "room": "diary"}},
		}
		if err := s.Upsert(ctx, ns, in); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		if err := s.SetPayload(ctx, ns, []string{"a"}, map[string]string{"wing": "wing_acme"}); err != nil {
			t.Fatalf("SetPayload: %v", err)
		}

		got, err := s.PointsByIDs(ctx, ns, []string{"a", "b"})
		if err != nil {
			t.Fatalf("PointsByIDs: %v", err)
		}
		byID := map[string]store.Point{}
		for _, p := range got {
			byID[p.ID] = p
		}
		if w := byID["a"].Payload["wing"]; w != "wing_acme" {
			t.Errorf("patched point a has wing %v, want %q", w, "wing_acme")
		}
		if r := byID["a"].Payload["room"]; r != "decisions" {
			t.Errorf("patching wing erased room on point a (room = %v) — the patch must MERGE, "+
				"or correcting one filter breaks another", r)
		}
		if w := byID["b"].Payload["wing"]; w != "wing_alpha" {
			t.Errorf("point b was patched and was not named: wing = %v", w)
		}

		// The vector must survive: this is a label change, and the whole reason
		// SetPayload exists rather than an Upsert is that re-embedding a memory
		// to correct its wing is a model call per drawer for a string edit.
		res, err := s.Search(ctx, ns, []float32{1, 0, 0}, 2, nil)
		if err != nil {
			t.Fatalf("Search after SetPayload: %v", err)
		}
		if res.StaleIndex {
			t.Errorf("a freshly written store reported a stale index — StaleIndex is the carrier " +
				"of the behind-index flag and must default false on a backend that is its own truth")
		}
		hits := res.H
		if len(hits) == 0 || hits[0].ID != "a" {
			t.Errorf("after patching its payload, point a is no longer the nearest neighbour of its "+
				"own vector (hits %+v) — the patch replaced or dropped the vector", hits)
		}

		// And the patch must change what a FILTERED search matches, not only what a
		// read returns. A driver may keep the payload twice — once verbatim for
		// readers and once flattened so the index can filter on it — and patching
		// only the readable copy leaves every scoped query matching the OLD value.
		// That is precisely the bug this method exists to repair, so a repair that
		// reproduced it in another backend would be invisible.
		oldRes, err := s.Search(ctx, ns, []float32{1, 0, 0}, 5, store.Filter{"wing": "wing_acme-legacy"})
		if err != nil {
			t.Fatalf("filtered search on the old wing: %v", err)
		}
		for _, h := range oldRes.H {
			if h.ID == "a" {
				t.Errorf("point a still matches a filter on its OLD wing after the patch — the copy " +
					"the index filters on was not updated, so every scoped search still sees the old value")
			}
		}
		newRes, err := s.Search(ctx, ns, []float32{1, 0, 0}, 5, store.Filter{"wing": "wing_acme"})
		if err != nil {
			t.Fatalf("filtered search on the new wing: %v", err)
		}
		found := false
		for _, h := range newRes.H {
			if h.ID == "a" {
				found = true
			}
		}
		if !found {
			t.Errorf("point a does not match a filter on its NEW wing after the patch (hits %+v) — "+
				"the memory is unreachable from the wing it now belongs to", newRes.H)
		}

		// Unknown ids and empty inputs are no-ops, matching Delete.
		if err := s.SetPayload(ctx, ns, []string{"no-such-id"}, map[string]string{"wing": "wing_x"}); err != nil {
			t.Errorf("SetPayload on an unknown id errored: %v", err)
		}
		if err := s.SetPayload(ctx, ns, nil, map[string]string{"wing": "wing_x"}); err != nil {
			t.Errorf("SetPayload with no ids errored: %v", err)
		}
		if err := s.SetPayload(ctx, ns, []string{"b"}, nil); err != nil {
			t.Errorf("SetPayload with an empty patch errored: %v", err)
		}
		after, _ := s.PointsByIDs(ctx, ns, []string{"b"})
		if len(after) != 1 || after[0].Payload["wing"] != "wing_alpha" {
			t.Errorf("an empty patch changed point b: %+v", after)
		}
	})
}
