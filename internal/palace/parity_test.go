package palace

// Regression parity suite: prove the Go ranking port produces the SAME results as
// the frozen Python mempalace for the same inputs. agentsmemory's rank.go is a
// faithful port of the frozen searcher's ranking math (the project's "mempalace
// part stays SAME as frozen" contract), so a silent numeric or ordering drift is a
// correctness bug. testdata/parity/*.json hold the frozen functions' output for a
// fixed corpus, captured once by gen_golden.py; these tests feed the Go port the
// identical inputs and assert it matches the golden. Embeddings are out of scope —
// ollama bge-m3 vectors are model-dependent, so only the math downstream of a fixed
// distance is byte-reproducible and tested here.
//
// Regenerate the fixtures (only needed if the frozen reference or corpus changes):
//
//go:generate python3 testdata/parity/gen_golden.py

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// parityEps bounds the float gap allowed between Go and frozen Python. The math is
// the same sequence of IEEE-754 operations, but Go's map iteration over per-doc term
// frequencies is randomized while Python iterates dict-insertion order, so BM25's
// term-sum can differ in the last few ULPs. A relative+absolute tolerance absorbs
// that without hiding any real divergence (BM25 scores sit in the ~0.1–3 range).
const parityEps = 1e-9

// loadGolden reads a fixture from testdata/parity into dst, failing the test if the
// file is missing or malformed — a missing golden means the suite isn't actually
// checking anything, which must be loud, not silent.
func loadGolden(t *testing.T, name string, dst any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "parity", name))
	if err != nil {
		t.Fatalf("read golden %s (regenerate with `go generate ./internal/palace`): %v", name, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("unmarshal golden %s: %v", name, err)
	}
}

// floatsClose reports whether two floats agree within parityEps (relative+absolute).
func floatsClose(got, want float64) bool {
	return math.Abs(got-want) <= parityEps*(1+math.Abs(want))
}

// TestParityTokenize pins Go tokenize against frozen _tokenize over a corpus that
// stresses the Python `\w`+re.UNICODE / text.lower() vs Go [\p{L}\p{N}_] /
// strings.ToLower boundary — ligatures, eszett, accents, CJK, and length<2 drops.
func TestParityTokenize(t *testing.T) {
	var g struct {
		Cases []struct {
			Name  string   `json:"name"`
			Input string   `json:"input"`
			Want  []string `json:"want"`
		} `json:"cases"`
	}
	loadGolden(t, "tokenize.json", &g)
	if len(g.Cases) == 0 {
		t.Fatal("tokenize.json has no cases")
	}
	for _, c := range g.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := tokenize(c.Input)
			// len comparison first so nil (Go empty input) and [] (frozen) are equal.
			if len(got) != len(c.Want) {
				t.Fatalf("tokenize(%q) = %v (len %d), frozen = %v (len %d)",
					c.Input, got, len(got), c.Want, len(c.Want))
			}
			for i := range got {
				if got[i] != c.Want[i] {
					t.Errorf("token[%d] = %q, frozen = %q (input %q)", i, got[i], c.Want[i], c.Input)
				}
			}
		})
	}
}

// TestParityBM25 pins Go bm25Scores against frozen _bm25_scores: same Okapi formula,
// smoothed IDF, length normalization, and edge cases (empty query/corpus, text-less
// doc, term-in-every-doc). Compared with a float tolerance, not bit-equality.
func TestParityBM25(t *testing.T) {
	var g struct {
		Cases []struct {
			Name  string    `json:"name"`
			Query string    `json:"query"`
			Docs  []string  `json:"docs"`
			Want  []float64 `json:"want"`
		} `json:"cases"`
	}
	loadGolden(t, "bm25.json", &g)
	if len(g.Cases) == 0 {
		t.Fatal("bm25.json has no cases")
	}
	for _, c := range g.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := bm25Scores(c.Query, c.Docs)
			if len(got) != len(c.Want) {
				t.Fatalf("bm25Scores(%q, %d docs) returned %d scores, frozen returned %d",
					c.Query, len(c.Docs), len(got), len(c.Want))
			}
			for i := range got {
				if !floatsClose(got[i], c.Want[i]) {
					t.Errorf("bm25[%d] = %.12g, frozen = %.12g (Δ %.2e)",
						i, got[i], c.Want[i], got[i]-c.Want[i])
				}
			}
		})
	}
}

// TestParitySimilarity pins Go vecSimFromDistance against frozen
// _distance_to_similarity for the cosine metric (the only metric agentsmemory's
// stores use), including the >1 distances that clamp to 0.
func TestParitySimilarity(t *testing.T) {
	var g struct {
		Cases []struct {
			Name     string  `json:"name"`
			Distance float64 `json:"distance"`
			Want     float64 `json:"want"`
		} `json:"cases"`
	}
	loadGolden(t, "similarity.json", &g)
	if len(g.Cases) == 0 {
		t.Fatal("similarity.json has no cases")
	}
	for _, c := range g.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := vecSimFromDistance(c.Distance)
			if !floatsClose(got, c.Want) {
				t.Errorf("vecSimFromDistance(%v) = %v, frozen = %v", c.Distance, got, c.Want)
			}
		})
	}
}

// TestParityHybrid pins the full re-rank: rankHybrid must order candidates exactly as
// frozen _hybrid_rank does (including stable order for exact ties) and produce the
// same fused score per candidate. want_fused is indexed by ORIGINAL position; the Go
// result carries each candidate's original Index, so we compare component-wise.
func TestParityHybrid(t *testing.T) {
	var g struct {
		Cases []struct {
			Name      string    `json:"name"`
			Query     string    `json:"query"`
			Docs      []string  `json:"docs"`
			Distances []float64 `json:"distances"`
			WantOrder []int     `json:"want_order"`
			WantFused []float64 `json:"want_fused"`
		} `json:"cases"`
	}
	loadGolden(t, "hybrid.json", &g)
	if len(g.Cases) == 0 {
		t.Fatal("hybrid.json has no cases")
	}
	for _, c := range g.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ranked := rankHybrid(c.Query, c.Docs, c.Distances, nil)
			if len(ranked) != len(c.WantOrder) {
				t.Fatalf("rankHybrid returned %d candidates, frozen ordered %d", len(ranked), len(c.WantOrder))
			}

			gotOrder := make([]int, len(ranked))
			fusedByIndex := make(map[int]float64, len(ranked))
			for i, h := range ranked {
				gotOrder[i] = h.Index
				fusedByIndex[h.Index] = h.Fused
			}
			for i := range gotOrder {
				if gotOrder[i] != c.WantOrder[i] {
					t.Fatalf("order = %v, frozen = %v", gotOrder, c.WantOrder)
				}
			}
			for origIdx, want := range c.WantFused {
				if !floatsClose(fusedByIndex[origIdx], want) {
					t.Errorf("fused[orig %d] = %v, frozen = %v (Δ %.2e)",
						origIdx, fusedByIndex[origIdx], want, fusedByIndex[origIdx]-want)
				}
			}
		})
	}
}
