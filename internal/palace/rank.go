package palace

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// Hybrid-ranking constants, ported verbatim from the frozen Python searcher
// (_hybrid_rank / _bm25_scores) so the Go SaaS recall ranks candidates exactly as
// the original did. search over-fetches a pool of vector neighbours, then this
// re-ranks them by a convex combination of vector similarity and lexical BM25 —
// vector finds the semantically near, BM25 rewards the literally-matching, and
// the blend beats either alone.
const (
	// bm25K1 is Okapi-BM25 term-frequency saturation (1.2–2.0 typical).
	bm25K1 = 1.5
	// bm25B is Okapi-BM25 length normalization (0=none, 1=full).
	bm25B = 0.75
	// hybridVectorWeight / hybridBM25Weight are the convex-combination weights:
	// 0.6 semantic + 0.4 lexical, matching the frozen default. They sum to 1 so the
	// fused score stays in the same [0,1]-ish range as each normalized term.
	hybridVectorWeight = 0.6
	hybridBM25Weight   = 0.4
	// hybridCandidateMultiplier is how far Search over-fetches beyond the requested
	// page so BM25 has a meaningful pool to re-rank (frozen used n_results*3). A
	// re-rank can only reorder what vector retrieval surfaced, so the pool must be
	// wider than the page or BM25 cannot promote a lexical match the page missed.
	hybridCandidateMultiplier = 3
	// closetDistanceCap is the farthest a closet hit may be (cosine distance) and
	// still lend its source a boost (frozen CLOSET_DISTANCE_CAP).
	closetDistanceCap = 1.5
)

// closetRankBoosts is the diminishing boost a closet hit adds to its source's
// drawers by closet rank: the best-matching closet lifts its source most, the
// fifth barely (frozen CLOSET_RANK_BOOSTS). Closets are a ranking SIGNAL, never a
// gate — they only raise scores, never filter — so the boost is added to the
// fused score and the cap above bounds how far a closet may be to count.
var closetRankBoosts = []float64{0.40, 0.25, 0.15, 0.08, 0.04}

// tokenRE matches the frozen _TOKEN_RE: runs of two or more word characters.
// \w is widened to the Unicode letter/number/underscore classes so non-ASCII
// content tokenizes the same way Python's re.UNICODE \w did.
var tokenRE = regexp.MustCompile(`[\p{L}\p{N}_]{2,}`)

// tokenize lowercases text and returns its word tokens (length >= 2). It tolerates
// empty input (returns nil), since a candidate drawer may carry no usable text.
func tokenize(text string) []string {
	if text == "" {
		return nil
	}
	return tokenRE.FindAllString(strings.ToLower(text), -1)
}

// bm25Scores computes Okapi-BM25 for query against each document, with IDF taken
// over the provided corpus (the candidate set itself). Corpus-relative IDF is the
// right choice for re-ranking: it measures how discriminative each query term is
// *within the candidates*, which is exactly what reorders them. The smoothed
// Lucene/BM25+ IDF — log((N - df + 0.5)/(df + 0.5) + 1) — is always non-negative,
// so a term in every candidate cannot drive a score below zero. Returned scores
// are raw (unbounded) and in docs order; the caller normalizes.
func bm25Scores(query string, docs []string) []float64 {
	n := len(docs)
	scores := make([]float64, n)

	// Query terms are a set: a term repeated in the query still contributes once
	// to df/idf, matching the frozen set(_tokenize(query)).
	queryTerms := map[string]struct{}{}
	for _, t := range tokenize(query) {
		queryTerms[t] = struct{}{}
	}
	if len(queryTerms) == 0 || n == 0 {
		return scores
	}

	tokenized := make([][]string, n)
	totalLen := 0
	for i, d := range docs {
		tokenized[i] = tokenize(d)
		totalLen += len(tokenized[i])
	}
	if totalLen == 0 {
		return scores // every candidate is text-less; nothing to rank lexically
	}
	avgdl := float64(totalLen) / float64(n)

	// Document frequency: how many candidates contain each query term (once each).
	df := make(map[string]int, len(queryTerms))
	for _, toks := range tokenized {
		seen := map[string]struct{}{}
		for _, t := range toks {
			if _, isQuery := queryTerms[t]; isQuery {
				seen[t] = struct{}{}
			}
		}
		for t := range seen {
			df[t]++
		}
	}
	idf := make(map[string]float64, len(queryTerms))
	for t := range queryTerms {
		idf[t] = math.Log((float64(n-df[t])+0.5)/(float64(df[t])+0.5) + 1)
	}

	for i, toks := range tokenized {
		dl := len(toks)
		if dl == 0 {
			continue
		}
		// Term frequency of each query term within this document.
		tf := map[string]int{}
		for _, t := range toks {
			if _, isQuery := queryTerms[t]; isQuery {
				tf[t]++
			}
		}
		var score float64
		for term, freq := range tf {
			f := float64(freq)
			num := f * (bm25K1 + 1)
			den := f + bm25K1*(1-bm25B+bm25B*float64(dl)/avgdl)
			score += idf[term] * num / den
		}
		scores[i] = score
	}
	return scores
}

// vecSimFromDistance maps a cosine distance in [0,2] (0 = identical) to a
// similarity in [0,1] via max(0, 1-d), matching the frozen _distance_to_similarity
// for the cosine metric — the only metric agentsmemory's stores use. Absolute (not
// relative-to-max) so adding or removing a candidate cannot reshuffle the others.
func vecSimFromDistance(distance float64) float64 {
	if s := 1 - distance; s > 0 {
		return s
	}
	return 0
}

// HybridScore is one candidate's fused ranking: its position in the input slice
// plus the component and combined scores, exposed so the search tool can report
// the lexical and closet contributions alongside the final order.
type HybridScore struct {
	Index int     // position in the docs/distances input
	Fused float64 // 0.6*vecSim + 0.4*bm25Norm + closetBoost, higher is better
	BM25  float64 // raw Okapi-BM25 score (pre-normalization)
	Boost float64 // closet boost added to this candidate (0 when none)
}

// rankHybrid fuses vector similarity, BM25 and an optional closet boost over a
// candidate set and returns the candidates' indices ordered best-first. docs[i]
// is candidate i's verbatim text, distances[i] its cosine distance, and boosts[i]
// a closet rank boost to add to its score (pass nil for no boosts). BM25 is
// min-max normalized within the set so it is commensurable with the [0,1] vector
// similarity before the weighted sum; the closet boost is added on top because it
// is a signal, not a competing term. A stable sort keeps the vector order as the
// tie-breaker when two candidates fuse equal. docs, distances and (when non-nil)
// boosts must be the same length.
func rankHybrid(query string, docs []string, distances, boosts []float64) []HybridScore {
	raw := bm25Scores(query, docs)
	var maxBM25 float64
	for _, s := range raw {
		if s > maxBM25 {
			maxBM25 = s
		}
	}

	out := make([]HybridScore, len(docs))
	for i := range docs {
		norm := 0.0
		if maxBM25 > 0 {
			norm = raw[i] / maxBM25
		}
		boost := 0.0
		if boosts != nil {
			boost = boosts[i]
		}
		fused := hybridVectorWeight*vecSimFromDistance(distances[i]) + hybridBM25Weight*norm + boost
		out[i] = HybridScore{Index: i, Fused: fused, BM25: raw[i], Boost: boost}
	}
	// Stable so equal-fused candidates keep their incoming (vector) order.
	sort.SliceStable(out, func(a, b int) bool { return out[a].Fused > out[b].Fused })
	return out
}
