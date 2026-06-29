package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// fakeSoT is an in-memory SourceOfTruth: enough to prove Hybrid's write ordering
// and Rebuild without pulling in SQLite.
type fakeSoT struct {
	points    map[string][]store.Point // namespace -> points
	upserts   int
	deletes   int
	ensureErr error
	upsertErr error
}

func newFakeSoT() *fakeSoT { return &fakeSoT{points: map[string][]store.Point{}} }

func (f *fakeSoT) EnsureNamespace(_ context.Context, _ string, _ int) error { return f.ensureErr }

func (f *fakeSoT) Upsert(_ context.Context, ns string, pts []store.Point) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts++
	f.points[ns] = append(f.points[ns], pts...)
	return nil
}

func (f *fakeSoT) Search(_ context.Context, _ string, _ []float32, _ int) ([]store.Hit, error) {
	return nil, errors.New("source of truth should not serve search")
}

func (f *fakeSoT) Delete(_ context.Context, ns string, ids []string) error {
	f.deletes++
	keep := f.points[ns][:0]
	for _, p := range f.points[ns] {
		if !slices.Contains(ids, p.ID) {
			keep = append(keep, p)
		}
	}
	f.points[ns] = keep
	return nil
}

func (f *fakeSoT) AllPoints(_ context.Context, ns string) ([]store.Point, error) {
	return f.points[ns], nil
}

func (f *fakeSoT) Namespaces(_ context.Context) ([]string, error) {
	nss := make([]string, 0, len(f.points))
	for ns := range f.points {
		nss = append(nss, ns)
	}
	return nss, nil
}

func (f *fakeSoT) PointsByIDs(_ context.Context, ns string, ids []string) ([]store.Point, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []store.Point
	for _, p := range f.points[ns] {
		if want[p.ID] {
			out = append(out, p)
		}
	}
	return out, nil
}

// fakeIndex records what the search index was asked to do and returns canned
// search results.
type fakeIndex struct {
	ensured    map[string]int
	upserted   map[string][]store.Point
	upserts    int
	deletes    int
	searchHits []store.Hit
	upsertErr  error
}

func newFakeIndex() *fakeIndex {
	return &fakeIndex{ensured: map[string]int{}, upserted: map[string][]store.Point{}}
}

func (f *fakeIndex) EnsureNamespace(_ context.Context, ns string, dim int) error {
	f.ensured[ns] = dim
	return nil
}

func (f *fakeIndex) Upsert(_ context.Context, ns string, pts []store.Point) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts++
	f.upserted[ns] = append(f.upserted[ns], pts...)
	return nil
}

func (f *fakeIndex) Search(_ context.Context, _ string, _ []float32, _ int) ([]store.Hit, error) {
	return f.searchHits, nil
}

func (f *fakeIndex) Delete(_ context.Context, _ string, _ []string) error {
	f.deletes++
	return nil
}

func TestHybridUpsertWritesBothSoTFirst(t *testing.T) {
	sot, idx := newFakeSoT(), newFakeIndex()
	h := store.NewHybrid(sot, idx)
	ctx := context.Background()

	pts := []store.Point{{ID: "a", Vector: []float32{1, 0}}}
	if err := h.Upsert(ctx, "team1", pts); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sot.upserts != 1 {
		t.Fatalf("source of truth not written: %d upserts", sot.upserts)
	}
	if idx.upserts != 1 {
		t.Fatalf("index not written: %d upserts", idx.upserts)
	}
}

func TestHybridUpsertDurableWhenIndexFails(t *testing.T) {
	sot, idx := newFakeSoT(), newFakeIndex()
	idx.upsertErr = errors.New("qdrant down")
	h := store.NewHybrid(sot, idx)
	ctx := context.Background()

	err := h.Upsert(ctx, "team1", []store.Point{{ID: "a", Vector: []float32{1, 0}}})
	if err == nil {
		t.Fatal("want error surfaced when index fails")
	}
	// The point must still be durable in the source of truth.
	all, _ := sot.AllPoints(ctx, "team1")
	if len(all) != 1 {
		t.Fatalf("source of truth lost data on index failure: %d points", len(all))
	}
}

func TestHybridUpsertAbortsWhenSoTFails(t *testing.T) {
	sot, idx := newFakeSoT(), newFakeIndex()
	sot.upsertErr = errors.New("disk full")
	h := store.NewHybrid(sot, idx)

	err := h.Upsert(context.Background(), "team1", []store.Point{{ID: "a", Vector: []float32{1, 0}}})
	if err == nil {
		t.Fatal("want error when source of truth fails")
	}
	// Index must not be touched if the truth was never persisted.
	if idx.upserts != 0 {
		t.Fatalf("index written despite source-of-truth failure: %d upserts", idx.upserts)
	}
}

func TestHybridSearchRoutesToIndex(t *testing.T) {
	sot, idx := newFakeSoT(), newFakeIndex()
	idx.searchHits = []store.Hit{{ID: "a", Score: 0.9}}
	h := store.NewHybrid(sot, idx)

	hits, err := h.Search(context.Background(), "team1", []float32{1, 0}, 5)
	if err != nil {
		t.Fatalf("search: %v", err) // would error if it hit the SoT
	}
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("search not served by index: %+v", hits)
	}
}

func TestHybridRebuildReplaysSoT(t *testing.T) {
	sot, idx := newFakeSoT(), newFakeIndex()
	ctx := context.Background()
	// Seed the source of truth directly, then simulate a fresh/empty index.
	_ = sot.Upsert(ctx, "team1", []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}},
		{ID: "b", Vector: []float32{0, 1, 0}},
	})
	h := store.NewHybrid(sot, idx)

	if err := h.Rebuild(ctx, "team1"); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if idx.ensured["team1"] != 3 {
		t.Fatalf("index namespace not ensured with dim 3: %d", idx.ensured["team1"])
	}
	if len(idx.upserted["team1"]) != 2 {
		t.Fatalf("rebuild did not replay all points: got %d", len(idx.upserted["team1"]))
	}
}
