package palace

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// endlessVectors is an index that always returns a full prefix and never
// honours the scope filter. That combination is what makes widening
// pathological: nothing in the prefix survives filtering, so the distinct-memory
// count never advances and "the backend ran out" never becomes true.
type endlessVectors struct {
	store.VectorStore
	ks []int
}

func (e *endlessVectors) Search(ctx context.Context, namespace string, vector []float32, k int, filter store.Filter) (store.SearchResult, error) {
	if strings.HasSuffix(namespace, "::closets") {
		return e.VectorStore.Search(ctx, namespace, vector, k, filter)
	}
	e.ks = append(e.ks, k)
	hits := make([]store.Hit, k)
	for i := range hits {
		hits[i] = store.Hit{ID: fmt.Sprintf("ghost-%d", i), Score: 1}
	}
	return store.SearchResult{H: hits}, nil
}

// TestCandidateWideningIsBounded pins the safety stop.
//
// Every other termination condition assumes the vector index honoured the scope
// filter. searchCandidates deliberately does not rely on that — the durable row
// is the authority — and when the two disagree the loop walks the corpus a
// doubling at a time. Before the bound the only ceiling was an int-overflow
// guard, so this fixture would widen until the process died rather than fail;
// that is why the assertion is on the requested depths and not on a return
// value.
func TestCandidateWideningIsBounded(t *testing.T) {
	svc := newTestService(t)
	vectors := &endlessVectors{VectorStore: svc.vectors}
	svc.vectors = vectors

	if _, err := svc.Search(context.Background(), "team-widen", SearchQuery{
		Query: "anything", Wing: "wing_alpha", Limit: 5,
	}); err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(vectors.ks) < 2 {
		t.Fatalf("index was asked %d times; the fixture is not provoking widening at all", len(vectors.ks))
	}
	first, widest := vectors.ks[0], 0
	for _, k := range vectors.ks {
		widest = max(widest, k)
	}
	// The 8 is written out rather than read from maxCandidateWidening on
	// purpose. Asserting against the constant under test makes the gate vacuous
	// — it passes at any value, including one that restores the unbounded walk.
	// Moving the bound should require deciding to move it here too.
	const wantBound = 8
	if widest > first*wantBound {
		t.Fatalf("widening reached depth %d from a candidate target of %d, past the %dx bound",
			widest, first, wantBound)
	}
}

// TestCandidateWideningDoesNotRefetchRows pins the incremental load.
//
// Widening re-asks the index for a SUPERSET of the previous prefix in the same
// order, so a round that reloads the whole prefix pays for every earlier round
// again — about twice the final prefix in database work. Nothing about the
// RESULT differs, so the gate watches the statements: a row resolved once must
// not be requested a second time.
func TestCandidateWideningDoesNotRefetchRows(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Two long memories, deeply chunked. The prefix must be FULL and hold far
	// fewer distinct memories than the candidate target, or the loop terminates
	// on its first round and there is no second fetch to observe.
	var ordered []store.Hit
	var firstChunk string
	for _, marker := range []string{"NEEDLE", "HAYSTACK"} {
		added, err := svc.Add(ctx, "team-widen", AddInput{
			Wing: "wing_alpha", Room: "decisions", Content: longMemory(marker, 12),
		})
		if err != nil {
			t.Fatalf("add %s: %v", marker, err)
		}
		for _, d := range added.Drawers {
			ordered = append(ordered, store.Hit{ID: d.ID, Score: 1})
		}
		if firstChunk == "" {
			firstChunk = added.Drawers[0].ID
		}
	}
	vectors := &orderedVectors{VectorStore: svc.vectors, hits: ordered}
	svc.vectors = vectors

	rec := &sqlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	svc.repo.db = svc.repo.db.Session(&gorm.Session{Logger: rec})
	svc.repo.reader = svc.repo.reader.Session(&gorm.Session{Logger: rec}) // reads run on the reader since ADR-052 T5

	if _, err := svc.Search(ctx, "team-widen", SearchQuery{Query: "NEEDLE", Limit: 5}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(vectors.ks) < 2 {
		t.Fatalf("index was asked %d time(s) at depths %v; without widening there is no refetch to detect",
			len(vectors.ks), vectors.ks)
	}

	requested := 0
	for _, sql := range rec.statements() {
		// The row-resolution query, not the memory-chunk union — and not the
		// supersedes lookup, which names the same ids in an IN clause on a
		// DIFFERENT column. That one runs once per page by construction and is not
		// what this gate is about; counting it would make a widening alarm fire on
		// a payload query and teach the next reader to ignore it.
		if strings.Contains(sql, "FROM `drawers`") && strings.Contains(sql, firstChunk) &&
			!strings.Contains(sql, "UNION ALL") && !strings.Contains(sql, "superseded_by") {
			requested++
		}
	}
	if requested == 0 {
		t.Fatal("no statement resolved the first chunk; the gate is not watching the right query")
	}
	if requested > 1 {
		t.Fatalf("the same drawer row was fetched by %d separate statements; widening is refetching the prefix it already loaded", requested)
	}
}
