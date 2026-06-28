package palace

import (
	"context"
	"reflect"
	"strings"
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
	ranked := rankHybrid("lru cache eviction", docs, distances, nil)
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
	ranked := rankHybrid("zzz qqq", docs, distances, nil) // query terms appear in no doc
	gotOrder := []int{ranked[0].Index, ranked[1].Index, ranked[2].Index}
	want := []int{1, 0, 2} // by ascending distance: 0.1, 0.3, 0.5
	if !reflect.DeepEqual(gotOrder, want) {
		t.Fatalf("no-lexical order = %v, want vector order %v", gotOrder, want)
	}
}

// TestRankHybridClosetBoostLifts pins the closet signal: two candidates with
// equal vector distance and no lexical match are tied, but the one carrying a
// closet boost must rank first, and the boost is recorded on the result.
func TestRankHybridClosetBoostLifts(t *testing.T) {
	docs := []string{"alpha note", "beta note"}
	distances := []float64{0.5, 0.5}
	boosts := []float64{0.0, 0.40}
	ranked := rankHybrid("zzz qqq", docs, distances, boosts) // no lexical signal
	if ranked[0].Index != 1 {
		t.Fatalf("closet-boosted candidate should rank first, got index %d", ranked[0].Index)
	}
	if ranked[0].Boost != 0.40 {
		t.Fatalf("boost should be recorded on the result, got %v", ranked[0].Boost)
	}
}

// TestSearchAppliesClosetBoost is the end-to-end payoff: after mining a source,
// a search whose query matches that source's closet lifts the source's drawers
// with a visible ClosetBoost — the third frozen ranking signal, now wired.
func TestSearchAppliesClosetBoost(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	content := strings.Repeat("Kubernetes orchestrates the deployment pipeline. ", 20) +
		"\n\n# Kubernetes Pipeline\n\nWe deployed the pipeline to production successfully."
	if _, err := svc.Mine(ctx, team, MineInput{Content: content, Wing: "infra", Room: "ops", Source: "k8s"}); err != nil {
		t.Fatalf("mine: %v", err)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "Kubernetes deployment pipeline", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits from the mined source")
	}
	var boosted bool
	for _, h := range hits {
		if h.ClosetBoost > 0 {
			boosted = true
		}
	}
	if !boosted {
		t.Fatal("expected a closet boost to be applied to the mined source's drawers")
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
