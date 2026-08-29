package mcptest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// threeMentions is a memory naming one subject in three separate places, which is
// what a real session note looks like: a header, then a body that returns to the
// same terms as it works through the material.
var threeMentions = "2026-08-21 | rerank pool sizing | the header line naming the subject. " +
	strings.Repeat("filler about unrelated matters of no interest at all here. ", 6) +
	"the rerank pool ships at ten because the cross encoder is linear in pool size. " +
	strings.Repeat("more filler, again about something else and quite unrelated. ", 6) +
	"and finally the rerank pool was measured at twenty two seconds when it was fifty. "

// hitsOf decodes a search response into its hits.
func hitsOf(t *testing.T, out string) []map[string]any {
	t.Helper()
	var page struct {
		Count int              `json:"count"`
		Hits  []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("search response is not the JSON an agent parses: %v\n%s", err, out)
	}
	if len(page.Hits) == 0 {
		t.Fatalf("the search returned no hits, so every assertion below would be vacuous:\n%s", out)
	}
	return page.Hits
}

// TestScenarioRegionsReachTheAgent drives the REAL search tool.
//
// SnippetRegions and MemoryIdentity are correct and were unit-tested before this
// existed, and correctness is rung 1. This is rung 2 and 3: something has to call
// them, and the agent has to receive the result. This codebase has twice carried
// a signal in the domain that the wire dropped — ChunksMatched and Reranked both
// existed as fields the one reader who acts on them could not see.
func TestScenarioRegionsReachTheAgent(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	h.MustCall(t, "am_add_drawer", map[string]any{"room": "decisions", "content": threeMentions})

	out := h.MustCall(t, "am_search", map[string]any{
		"query": "rerank pool size", "snippet_chars": 400,
	})
	hits := hitsOf(t, out)

	var sawRegions, sawIdentity bool
	for _, hit := range hits {
		if rs, ok := hit["regions"].([]any); ok && len(rs) > 1 {
			sawRegions = true
			for _, r := range rs {
				m, _ := r.(map[string]any)
				if txt, _ := m["text"].(string); txt == "" {
					t.Error("a region reached the agent with no text")
				} else if !strings.Contains(threeMentions, txt) {
					t.Errorf("a region is not a slice of the memory — something on this path is "+
						"generated, which ADR-019 refuses: %q", txt)
				}
				if _, ok := m["terms_matched"]; !ok {
					t.Error("a region carries no score, so an agent cannot rank the regions itself")
				}
			}
		}
		if id, _ := hit["identity"].(string); id != "" {
			sawIdentity = true
			if !strings.HasPrefix(threeMentions, id) {
				t.Errorf("identity is not the memory's own opening line: %q", id)
			}
		}
	}
	if !sawRegions {
		t.Error("no hit carried its other matching regions. The domain computes them and the wire " +
			"drops them, which is exactly the defect shape this repository keeps shipping — and no " +
			"unit test of SnippetRegions can see it.")
	}
	if !sawIdentity {
		t.Error("no hit carried an identity line")
	}
}

// TestScenarioCoverageVariesAcrossAPage: content_truncated is true for 98% of
// hits, and a flag that almost never varies carries no information — an agent
// cannot fetch five whole memories and nothing tells it which one hides the
// answer. A replacement that is ALSO constant is worse than keeping the first,
// because it looks like progress.
func TestScenarioCoverageVariesAcrossAPage(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "a short memory about the rerank pool and nothing else at all.",
	})
	h.MustCall(t, "am_add_drawer", map[string]any{"room": "decisions", "content": threeMentions})

	out := h.MustCall(t, "am_search", map[string]any{
		"query": "rerank pool", "snippet_chars": 200,
	})
	hits := hitsOf(t, out)
	if len(hits) < 2 {
		t.Fatalf("expected both memories on the page, got %d:\n%s", len(hits), out)
	}
	seen := map[float64]bool{}
	for _, hit := range hits {
		c, ok := hit["content_coverage"].(float64)
		if !ok {
			t.Fatalf("content_coverage is absent from a hit. It is deliberately not omitempty, "+
				"because 0 is a real value and this codebase has shipped an absent field that meant "+
				"four different things at once:\n%s", out)
		}
		if c < 0 || c > 1 {
			t.Errorf("content_coverage = %v, outside 0..1", c)
		}
		seen[c] = true
	}
	if len(seen) < 2 {
		t.Errorf("a short memory and a long one report the SAME coverage (%v) — a constant signal "+
			"is the defect being replaced, not the replacement", seen)
	}
}

// TestScenarioContentKeepsItsMeaning: every agent in the wild reads `content`.
// This change adds beside it and must not redefine it.
func TestScenarioContentKeepsItsMeaning(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	h.MustCall(t, "am_add_drawer", map[string]any{"room": "decisions", "content": threeMentions})

	out := h.MustCall(t, "am_search", map[string]any{
		"query": "rerank pool size", "snippet_chars": 200,
	})
	content, _ := hitsOf(t, out)[0]["content"].(string)
	if content == "" {
		t.Fatalf("content is empty:\n%s", out)
	}
	if strings.Count(content, " … ") > 1 {
		t.Errorf("content now reads like joined regions (%q) — regions are their own field, and "+
			"content keeps the meaning every existing reader depends on", content)
	}
}

// TestScenarioCoverageIsOneWhenNothingWasCut: a hit showing the whole memory
// reports coverage 1, not 0.
//
// It reported 0 — the field was only assigned when the snippet cut something, so
// "you were shown all of it" and "you were shown none of it" arrived identical.
// Worse than the uninformative flag it replaces, and it was found by a mutant
// that made coverage a CONSTANT and still passed: the wrong zero was supplying
// the variation the other test looked for.
func TestScenarioCoverageIsOneWhenNothingWasCut(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "a short memory about the rerank pool, shown whole.",
	})
	out := h.MustCall(t, "am_search", map[string]any{
		"query": "rerank pool", "snippet_chars": 400,
	})
	hit := hitsOf(t, out)[0]
	c, ok := hit["content_coverage"].(float64)
	if !ok {
		t.Fatalf("content_coverage absent:\n%s", out)
	}
	if c < 0.99 {
		t.Errorf("a memory returned WHOLE reports coverage %v — an agent reads that as having been "+
			"shown almost none of it, and fetches a memory it already has", c)
	}
}

// TestScenarioChildChunkCarriesRootAnchor pins the protocol-level staleness
// defect through the real MCP transport. Anchors are written on chunk zero, but
// the matching passage deliberately lives only in a child chunk. Whichever A/B
// ranking arm serves the hit, staleness belongs to the memory and must travel.
func TestScenarioChildChunkCarriesRootAnchor(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	const marker = "CHILD-ANCHOR-MARKER"
	added := h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": filler(2400) + marker + " the retry rule changed",
		"code_anchors": []any{map[string]any{
			"path": "internal/retry/retry.go", "snippet": "func retryChanged()",
		}},
	})
	if got := drawerCount(t, h, added); got < 2 {
		t.Fatalf("fixture produced %d chunk(s), so a child cannot win:\n%s", got, added)
	}

	listed := h.MustCall(t, "am_list_anchors", map[string]any{"wing": "wing_acme"})
	id := firstAnchorID(t, h, listed)
	h.MustCall(t, "am_mark_anchors", map[string]any{
		"verdicts": []any{map[string]any{"id": id, "status": "drifted", "line": 17}},
	})

	out := h.MustCall(t, "am_search", map[string]any{
		"query": marker, "wing": "wing_acme", "snippet_chars": 200,
	})
	hit := hitsOf(t, out)[0]
	chunkIndex, _ := hit["chunk_index"].(float64)
	if chunkIndex < 1 {
		t.Fatalf("chunk zero won, so the fixture did not exercise the defect:\n%s", out)
	}
	if memoryID, _ := hit["memory_id"].(string); memoryID == "" {
		t.Fatalf("the hit has no stable memory identity:\n%s", out)
	}
	if stale, _ := hit["stale"].(bool); !stale {
		t.Fatalf("the child hit lost its root chunk's drifted anchor:\n%s", out)
	}
	anchors, _ := hit["code_anchors"].([]any)
	if len(anchors) == 0 {
		t.Fatalf("the child hit carries stale=true but no anchor evidence:\n%s", out)
	}
	anchor, _ := anchors[0].(map[string]any)
	if status, _ := anchor["status"].(string); status != "drifted" {
		t.Fatalf("anchor status = %q, want drifted:\n%s", status, out)
	}
}

// TestScenarioCoverageCountsTheRegionsItRendered is the rung the arithmetic's own
// unit test cannot reach: something has to CALL it.
//
// `coveredRunes` can be correct and the wire can still carry the old number —
// this repository has shipped that shape four times, a finished capability with
// nothing selecting it. The unit test in internal/mcpserver drives the function;
// this drives the real am_search tool over the transport and reads the field an
// agent actually receives. Restore the window-only division at the call site and
// this is what goes red.
func TestScenarioCoverageCountsTheRegionsItRendered(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	h.MustCall(t, "am_add_drawer", map[string]any{"room": "decisions", "content": threeMentions})

	out := h.MustCall(t, "am_search", map[string]any{
		"query": "rerank pool size", "snippet_chars": 200,
	})
	hit := hitsOf(t, out)[0]

	rs, _ := hit["regions"].([]any)
	if len(rs) < 2 {
		t.Skipf("the fixture produced %d region(s), so there is nothing beside the window to "+
			"count and the assertion below would be vacuous:\n%s", len(rs), out)
	}
	content, _ := hit["content"].(string)
	length, _ := hit["content_length"].(float64)
	coverage, ok := hit["content_coverage"].(float64)
	if !ok || length == 0 {
		t.Fatalf("content_coverage or content_length absent from a truncated hit:\n%s", out)
	}

	windowOnly := float64(len([]rune(content))) / length
	if coverage <= windowOnly {
		t.Errorf("content_coverage = %v, which is the window alone (%v) — the response also "+
			"rendered %d regions of this memory, so the caller was shown more than the number "+
			"it is being asked to act on. Under-reporting here is what makes the defensive "+
			"read (whole:true) the rational one:\n%s", coverage, windowOnly, len(rs), out)
	}
	if coverage > 1 {
		t.Errorf("content_coverage = %v — a fraction of a memory cannot exceed the memory, and "+
			"over-reporting reads as completeness", coverage)
	}
}
