package chromemvec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// newTestIndex opens an index in a throwaway directory, so every test exercises
// the real persistent path (files on disk) rather than an in-memory shortcut the
// server never uses.
func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := New(filepath.Join(t.TempDir(), "chromem"))
	if err != nil {
		t.Fatalf("new index: %v", err)
	}
	return idx
}

// TestOpenDiscardsAnOlderIndexLayout covers the upgrade path for an install that
// already has an index on disk: a directory written before the flat filter keys
// existed must be thrown away and rebuilt, not read. Reading it would silently
// return an empty page for every wing-scoped search — a wrong answer, where a
// rebuild costs only a replay of vectors SQLite still holds.
func TestOpenDiscardsAnOlderIndexLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agentsmemory.chromem")
	if err := os.MkdirAll(filepath.Join(dir, "team1"), 0o755); err != nil {
		t.Fatalf("seed old index: %v", err)
	}
	stale := filepath.Join(dir, "team1", "doc.gob")
	if err := os.WriteFile(stale, []byte("pre-v2 document"), 0o644); err != nil {
		t.Fatalf("seed old document: %v", err)
	}

	if _, err := New(dir); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale document survived the layout change (err=%v)", err)
	}
	stamp, err := os.ReadFile(filepath.Join(dir, schemaFile))
	if err != nil || string(stamp) != schemaVersion {
		t.Errorf("schema stamp = %q (err %v), want %q", stamp, err, schemaVersion)
	}

	// Re-opening a stamped directory must keep what is in it — otherwise every
	// restart would throw the index away and pay for a full rebuild.
	idx, err := New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := idx.Upsert(context.Background(), "team1", []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := New(dir); err != nil {
		t.Fatalf("third open: %v", err)
	}
	again, err := New(dir)
	if err != nil {
		t.Fatalf("fourth open: %v", err)
	}
	if n, err := again.Count(context.Background(), "team1"); err != nil || n != 1 {
		t.Errorf("reopen lost the index: count=%d err=%v", n, err)
	}
}

// TestOpenExistingNeverInitializesOrReplacesTheIndex pins the diagnostic open
// against New's intentionally destructive boot behavior. A doctor must return
// the fault and leave its evidence byte-for-byte where it found it.
func TestOpenExistingNeverInitializesOrReplacesTheIndex(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing.chromem")
		if _, err := OpenExisting(dir); err == nil {
			t.Fatal("inspection initialized a missing index")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("inspection created the missing index directory (err=%v)", err)
		}
	})

	t.Run("stale", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "stale.chromem")
		stale := filepath.Join(dir, "team1", "old-index.gob")
		if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
			t.Fatalf("seed stale directory: %v", err)
		}
		if err := os.WriteFile(stale, []byte("evidence"), 0o644); err != nil {
			t.Fatalf("seed stale file: %v", err)
		}

		if _, err := OpenExisting(dir); err == nil {
			t.Fatal("inspection accepted an unstamped index")
		}
		if got, err := os.ReadFile(stale); err != nil || string(got) != "evidence" {
			t.Errorf("inspection replaced stale evidence: content=%q err=%v", got, err)
		}
	})

	t.Run("current", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "current.chromem")
		writer, err := New(dir)
		if err != nil {
			t.Fatalf("create current index: %v", err)
		}
		if err := writer.Upsert(context.Background(), "team1", []store.Point{{
			ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_one"},
		}}); err != nil {
			t.Fatalf("seed current index: %v", err)
		}

		reader, err := OpenExisting(dir)
		if err != nil {
			t.Fatalf("inspect current index: %v", err)
		}
		points, err := reader.PointsByIDs(context.Background(), "team1", []string{"a"})
		if err != nil || len(points) != 1 || points[0].ID != "a" {
			t.Fatalf("read current index: points=%v err=%v", points, err)
		}

		mutations := []struct {
			name string
			run  func() error
		}{
			{"ensure namespace", func() error { return reader.EnsureNamespace(context.Background(), "new-team", 3) }},
			{"upsert", func() error {
				return reader.Upsert(context.Background(), "team1", []store.Point{{ID: "b", Vector: []float32{0, 1, 0}}})
			}},
			{"set payload", func() error {
				return reader.SetPayload(context.Background(), "team1", []string{"a"}, map[string]string{"wing": "changed"})
			}},
			{"delete", func() error { return reader.Delete(context.Background(), "team1", []string{"a"}) }},
		}
		for _, mutation := range mutations {
			if err := mutation.run(); !errors.Is(err, errReadOnly) {
				t.Errorf("read-only %s error = %v, want %v", mutation.name, err, errReadOnly)
			}
		}
		if collections := reader.db.ListCollections(); len(collections) != 1 || collections["team1"] == nil {
			t.Errorf("rejected writes changed the in-memory collection set: %v", collections)
		}

		again, err := OpenExisting(dir)
		if err != nil {
			t.Fatalf("reopen after rejected writes: %v", err)
		}
		if collections := again.db.ListCollections(); len(collections) != 1 || collections["team1"] == nil {
			t.Errorf("rejected writes changed the persisted collection set: %v", collections)
		}
		points, err = again.PointsByIDs(context.Background(), "team1", []string{"a", "b"})
		if err != nil || len(points) != 1 || points[0].ID != "a" || points[0].Payload["wing"] != "wing_one" {
			t.Fatalf("rejected writes changed persisted state: points=%v err=%v", points, err)
		}

		res, err := again.Search(context.Background(), "missing-team", []float32{1, 0, 0}, 1, nil)
		if err != nil || len(res.H) != 0 {
			t.Errorf("search missing namespace: hits=%v err=%v", res.H, err)
		}
		points, err = again.PointsByIDs(context.Background(), "missing-team", []string{"a"})
		if err != nil || len(points) != 0 {
			t.Errorf("read missing namespace: points=%v err=%v", points, err)
		}
		if n, err := again.Count(context.Background(), "missing-team"); err != nil || n != 0 {
			t.Errorf("count missing namespace: count=%d err=%v", n, err)
		}
		if collections := again.db.ListCollections(); len(collections) != 1 || collections["team1"] == nil {
			t.Errorf("reads changed the collection set: %v", collections)
		}
	})
}

// TestSearchFilterNarrowsToPayload proves the wing/room scope is answered by the
// index rather than by the caller: the nearest vector is in the wrong wing, and a
// filtered search must skip past it instead of returning it for the caller to
// discard.
func TestSearchFilterNarrowsToPayload(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	points := []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_one", "room": "decisions"}},
		{ID: "b", Vector: []float32{0.9, 0.1, 0}, Payload: map[string]any{"wing": "wing_two", "room": "decisions"}},
		{ID: "c", Vector: []float32{0.8, 0.2, 0}, Payload: map[string]any{"wing": "wing_two", "room": "diary"}},
	}
	if err := idx.Upsert(ctx, ns, points); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	res, err := idx.Search(ctx, ns, []float32{1, 0, 0}, 3, store.Filter{"wing": "wing_two"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.H) != 2 || res.H[0].ID != "b" || res.H[1].ID != "c" {
		t.Fatalf("wing filter: want [b c], got %v", ids(res.H))
	}

	// Two keys must both hold, and the payload still round-trips verbatim.
	res, err = idx.Search(ctx, ns, []float32{1, 0, 0}, 3, store.Filter{"wing": "wing_two", "room": "diary"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.H) != 1 || res.H[0].ID != "c" {
		t.Fatalf("wing+room filter: want [c], got %v", ids(res.H))
	}
	if res.H[0].Payload["room"] != "diary" {
		t.Errorf("payload not round-tripped: %v", res.H[0].Payload)
	}
}

// ids renders hit ids for a failure message.
func ids(hits []store.Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func TestUpsertSearchRanking(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	points := []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"label": "x-axis"}},
		{ID: "b", Vector: []float32{0, 1, 0}},
		{ID: "c", Vector: []float32{0.9, 0.1, 0}}, // close to a
	}
	if err := idx.Upsert(ctx, ns, points); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	res, err := idx.Search(ctx, ns, []float32{1, 0, 0}, 2, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.H) != 2 {
		t.Fatalf("want 2 hits, got %d", len(res.H))
	}
	if res.H[0].ID != "a" || res.H[1].ID != "c" {
		t.Fatalf("want closest-first [a c], got [%s %s]", res.H[0].ID, res.H[1].ID)
	}
	if res.H[0].Score <= res.H[1].Score {
		t.Fatalf("scores must descend, got %v then %v", res.H[0].Score, res.H[1].Score)
	}
	// The payload must survive the JSON round-trip through chromem's
	// string-only metadata, and the reserved key must not leak into it.
	if got := res.H[0].Payload["label"]; got != "x-axis" {
		t.Fatalf("payload label = %v, want x-axis", got)
	}
	if _, leaked := res.H[0].Payload[payloadKey]; leaked {
		t.Fatalf("reserved key %q leaked into the caller payload", payloadKey)
	}
	if res.H[1].Payload != nil {
		t.Fatalf("point stored without a payload came back with %v", res.H[1].Payload)
	}
}

func TestUpsertReplacesByID(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	if err := idx.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"v": "old"}}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := idx.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{0, 1, 0}, Payload: map[string]any{"v": "new"}}}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	n, err := idx.Count(ctx, ns)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("re-upserting an ID must replace, not duplicate: count = %d", n)
	}
	res, err := idx.Search(ctx, ns, []float32{0, 1, 0}, 1, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := res.H[0].Payload["v"]; got != "new" {
		t.Fatalf("payload = %v, want the replacement", got)
	}
}

// TestSearchClampsK guards the one place chromem's contract differs from the
// seam's: chromem errors when asked for more results than it holds, while
// store.VectorStore promises a short result slice instead.
func TestSearchClampsK(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	if err := idx.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	res, err := idx.Search(ctx, ns, []float32{1, 0, 0}, 10, nil)
	if err != nil {
		t.Fatalf("search with k above the point count: %v", err)
	}
	if len(res.H) != 1 {
		t.Fatalf("want the 1 stored point, got %d hits", len(res.H))
	}
}

func TestSearchEdgeCases(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	// An empty namespace is a legitimate state (a workspace before its first
	// drawer), not an error.
	res, err := idx.Search(ctx, "empty", []float32{1, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search on an empty namespace: %v", err)
	}
	if len(res.H) != 0 {
		t.Fatalf("want no hits, got %d", len(res.H))
	}

	if err := idx.Upsert(ctx, "team1", []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if res, err = idx.Search(ctx, "team1", []float32{1, 0, 0}, 0, nil); err != nil || len(res.H) != 0 {
		t.Fatalf("k <= 0 must return no hits and no error, got %d hits, err %v", len(res.H), err)
	}
}

func TestNamespacesAreIsolated(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	if err := idx.Upsert(ctx, "team1", []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert team1: %v", err)
	}
	if err := idx.Upsert(ctx, "team2", []store.Point{{ID: "b", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert team2: %v", err)
	}

	res, err := idx.Search(ctx, "team1", []float32{1, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.H) != 1 || res.H[0].ID != "a" {
		t.Fatalf("tenant isolation broken: %+v", res.H)
	}
}

func TestDelete(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	points := []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}},
		{ID: "b", Vector: []float32{0, 1, 0}},
	}
	if err := idx.Upsert(ctx, ns, points); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// "missing" is not stored: the seam says unknown IDs are ignored, not an error.
	if err := idx.Delete(ctx, ns, []string{"a", "missing"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := idx.Delete(ctx, ns, nil); err != nil {
		t.Fatalf("delete with no ids must be a no-op: %v", err)
	}

	n, err := idx.Count(ctx, ns)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 point left, got %d", n)
	}
}

// TestPersistsAcrossReopen is the property that makes chromem a usable local
// backend: a restart must find the index where it left it, not rebuild from
// scratch.
func TestPersistsAcrossReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chromem")
	ctx := context.Background()
	const ns = "team1"

	first, err := New(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := first.Upsert(ctx, ns, []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"label": "x-axis"}},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	second, err := New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	res, err := second.Search(ctx, ns, []float32{1, 0, 0}, 1, nil)
	if err != nil {
		t.Fatalf("search after reopen: %v", err)
	}
	if len(res.H) != 1 || res.H[0].ID != "a" {
		t.Fatalf("index did not survive the reopen: %+v", res.H)
	}
	if got := res.H[0].Payload["label"]; got != "x-axis" {
		t.Fatalf("payload after reopen = %v, want x-axis", got)
	}
}
