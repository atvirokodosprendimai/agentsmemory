package palace

import (
	"context"
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	got := tokenize("The LRU-cache, evicts! 2x x")
	// lowercased, split on non-word, tokens of length >= 2 only ("x" dropped).
	want := []string{"the", "lru", "cache", "evicts", "2x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
	if tokenize("") != nil {
		t.Fatal("empty text should tokenize to nil")
	}
}

func TestBM25ScoresPresenceBeatsAbsence(t *testing.T) {
	docs := []string{
		"the cache uses an lru eviction policy",
		"completely unrelated text about the weather",
	}
	scores := bm25Scores("lru cache eviction", docs)
	if scores[0] <= 0 {
		t.Fatalf("doc containing the query terms should score > 0, got %.3f", scores[0])
	}
	if scores[1] != 0 {
		t.Fatalf("doc with no query terms should score 0, got %.3f", scores[1])
	}
}

func TestBM25ScoresEmptyQueryOrCorpus(t *testing.T) {
	if got := bm25Scores("", []string{"anything"}); got[0] != 0 {
		t.Fatalf("empty query yields zero scores, got %.3f", got[0])
	}
	if got := bm25Scores("q", nil); len(got) != 0 {
		t.Fatalf("empty corpus yields no scores, got %d", len(got))
	}
}

// TestRankHybridLexicalPromotesOverVector pins the convex blend: a candidate with
// a WORSE vector distance but a strong lexical match must outrank one that is
// vector-closer yet lexically empty, because BM25 (weight 0.4) tips the sum.
//
//	A: distance 0.5 -> vecSim 0.5, bm25Norm 1.0 -> fused 0.6*0.5 + 0.4*1.0 = 0.70
//	B: distance 0.1 -> vecSim 0.9, bm25Norm 0.0 -> fused 0.6*0.9 + 0.4*0.0 = 0.54
func TestRankHybridLexicalPromotesOverVector(t *testing.T) {
	docs := []string{
		"the cache uses an lru eviction policy", // strong lexical match
		"a quiet meadow at dawn",                // no query terms
	}
	distances := []float64{0.5, 0.1}
	ranked := rankHybrid("lru cache eviction", docs, distances)
	if ranked[0].Index != 0 {
		t.Fatalf("lexical match should rank first; got index %d (fused %.3f)", ranked[0].Index, ranked[0].Fused)
	}
	if ranked[0].BM25 <= 0 {
		t.Fatalf("top hit should carry a positive raw BM25, got %.3f", ranked[0].BM25)
	}
}

// TestRankHybridNoLexicalFallsBackToVector confirms that when no candidate matches
// the query lexically (all BM25 = 0), the order is pure vector — smallest distance
// first — so hybrid never does worse than vector-only on a lexical miss.
func TestRankHybridNoLexicalFallsBackToVector(t *testing.T) {
	docs := []string{"alpha text", "beta text", "gamma text"}
	distances := []float64{0.3, 0.1, 0.5}
	ranked := rankHybrid("zzz qqq", docs, distances) // query terms appear in no doc
	gotOrder := []int{ranked[0].Index, ranked[1].Index, ranked[2].Index}
	want := []int{1, 0, 2} // by ascending distance: 0.1, 0.3, 0.5
	if !reflect.DeepEqual(gotOrder, want) {
		t.Fatalf("no-lexical order = %v, want vector order %v", gotOrder, want)
	}
}

// TestSearchSurfacesBM25 is an end-to-end check that the hybrid path runs: the
// exact-phrase drawer is the top hit and its lexical BM25 component is populated.
func TestSearchSurfacesBM25(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "the cache uses an lru eviction policy"})
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "the button turns blue on hover"})

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "the cache uses an lru eviction policy"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	if hits[0].Drawer.Content != "the cache uses an lru eviction policy" {
		t.Fatalf("exact phrase should top the hybrid ranking, got %q", hits[0].Drawer.Content)
	}
	if hits[0].BM25 <= 0 {
		t.Fatalf("top hybrid hit should carry a positive BM25, got %.3f", hits[0].BM25)
	}
}
