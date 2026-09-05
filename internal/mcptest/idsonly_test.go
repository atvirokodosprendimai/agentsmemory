package mcptest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestAnIdsOnlyPageCarriesNoContentAndSaysSo drives the real tool over the
// harness: with ids_only the page keeps its identity and its numbers, drops the
// text, says on every hit that it is partial, and costs a fraction of the full
// page on the same fixture. Without it, the page is what it always was.
//
// ADR-060. The size bound is asserted here rather than trusted from the record's
// Context, because a thin view that quietly regrows — one field at a time, each
// justified — is the failure a number in prose cannot catch.
func TestAnIdsOnlyPageCarriesNoContentAndSaysSo(t *testing.T) {
	h := mcptest.New(t)

	// Three memories long enough that the full page renders a window AND regions
	// for each — the shape a real page has — rather than three one-liners whose
	// full hit is barely larger than its identity.
	for _, c := range []string{
		strings.Repeat("the recall identifier is minted once per search and stored as the event row's key. ", 25),
		strings.Repeat("a caller ranks hits by blended score and fetches the few it filters in with am_get_drawer. ", 25),
		strings.Repeat("the recall identifier is minted once per search and the page carries the numbers a caller ranks by. ", 25),
	} {
		h.MustCall(t, "am_add_drawer", map[string]any{"wing": "wing_alpha", "room": "decisions", "content": c})
	}
	args := map[string]any{"query": "recall identifier minted once per search, ranked by blended score", "wing": "wing_alpha", "limit": 5}
	full := h.MustCall(t, "am_search", args)
	thinArgs := map[string]any{}
	for k, v := range args {
		thinArgs[k] = v
	}
	thinArgs["ids_only"] = true
	thin := h.MustCall(t, "am_search", thinArgs)

	fullID, fullHits := pageOf(t, full)
	thinID, thinHits := pageOf(t, thin)
	if len(fullHits) == 0 {
		t.Fatal("the full search found nothing, so nothing below is proved")
	}
	if len(thinHits) != len(fullHits) {
		t.Fatalf("ids_only returned %d hits against %d: the mode changed WHAT was found, not how much of it", len(thinHits), len(fullHits))
	}
	if strings.TrimSpace(thinID) == "" || len(thinID) != len(fullID) {
		t.Errorf("the thin page's search_id %q is not shaped like the full page's %q", thinID, fullID)
	}
	// Half, not the tenth measured in production: the production hits were 88k-
	// character transcripts carrying four regions each, and a hermetic fixture
	// cannot honestly reproduce that. What the bound pins is that the thin view
	// cannot regrow past half of a real-shaped page one field at a time.
	t.Logf("full page %d bytes, thin page %d bytes", len(full), len(thin))
	if len(thin)*2 > len(full) {
		t.Errorf("the thin page is %d bytes against %d for the full page — not the fraction the mode exists for", len(thin), len(full))
	}

	fullKeys := map[string]bool{}
	for k := range fullHits[0] {
		fullKeys[k] = true
	}
	for i, hit := range thinHits {
		for _, banned := range []string{"content", "regions", "content_coverage"} {
			if _, ok := hit[banned]; ok {
				t.Errorf("thin hit %d carries %q; an ids-only hit must hold none of the memory", i, banned)
			}
		}
		for _, want := range []string{"id", "memory_id", "wing", "room", "distance", "content_length"} {
			if _, ok := hit[want]; !ok {
				t.Errorf("thin hit %d lacks %q, which a caller ranks or fetches by", i, want)
			}
		}
		// blended_score is omitempty on the full hit (no reranker in this harness
		// means zero), so it is required on the thin hit exactly when the full
		// hit carries it — the rank a caller sorts by must not vanish in the thin mode.
		if _, onFull := fullHits[i]["blended_score"]; onFull {
			if _, ok := hit["blended_score"]; !ok {
				t.Errorf("thin hit %d dropped blended_score that the full hit carries", i)
			}
		}
		if tr, _ := hit["content_truncated"].(bool); !tr {
			t.Errorf("thin hit %d does not say it is partial (content_truncated); a caller reading it as whole reads an empty memory", i)
		}
		if n, _ := hit["content_length"].(float64); n <= 0 {
			t.Errorf("thin hit %d reports content_length %v; a fetch would return more than that", i, n)
		}
		for k := range hit {
			if !fullKeys[k] {
				t.Errorf("thin hit %d carries %q, a key the full hit does not — the thin view is built from the full view, not beside it", i, k)
			}
		}
	}
	if _, ok := fullHits[0]["content"]; !ok {
		t.Error("the FULL page lost its content; the default must be byte-for-byte what it was")
	}

	// Explicit false is the default, not a third mode.
	thinArgs["ids_only"] = false
	_, offHits := pageOf(t, h.MustCall(t, "am_search", thinArgs))
	if len(offHits) == 0 {
		t.Fatal("ids_only=false found nothing")
	}
	if _, ok := offHits[0]["content"]; !ok {
		t.Error("ids_only=false returned a hit without content")
	}
	var probe map[string]any
	_ = json.Unmarshal([]byte(thin), &probe)
	if _, ok := probe["withheld"]; ok {
		t.Error("an ids-only page reports withheld hits; nothing on it is paid for by content, so nothing can be withheld")
	}
}

// TestAThinPageOverTheBudgetWithholdsNothing drives the one branch the fixture
// above cannot reach: three whole memories past ResponseBudget make the FULL
// page withhold, and the thin page of the same hits must not report it —
// nothing on a thin page is paid for by content, so a withheld count there
// would tell a caller a hit is missing that is on the page. Review of #277
// measured the reset as an equivalent mutant under the small fixture; this is
// the fixture under which it is not.
func TestAThinPageOverTheBudgetWithholdsNothing(t *testing.T) {
	h := mcptest.New(t)
	for i, seed := range []string{
		"the recall identifier is minted once per search and stored as the event row's key. ",
		"a caller ranks hits by blended score and fetches the few it filters in with am_get_drawer. ",
		"the recall identifier is minted once per search and the page carries the numbers a caller ranks by. ",
	} {
		// ~22,000 runes each. With snippet_chars=20000 the first two windows spend
		// the 40,000-rune budget and the third hit is withheld — a whole-memory
		// request (snippet_chars=0) would instead fall back to a bounded window
		// and never reach the withheld branch.
		h.MustCall(t, "am_add_drawer", map[string]any{"wing": "wing_alpha", "room": "decisions",
			"content": strings.Repeat(seed, 250) + " " + strings.Repeat("x", i)})
	}
	args := map[string]any{"query": "recall identifier minted once per search, ranked by blended score", "wing": "wing_alpha", "limit": 5, "snippet_chars": 20000}
	var full map[string]any
	if err := json.Unmarshal([]byte(h.MustCall(t, "am_search", args)), &full); err != nil {
		t.Fatal(err)
	}
	if _, ok := full["withheld"]; !ok {
		t.Fatalf("the full page did not withhold anything, so this case does not reach the branch it exists for: %v", full["count"])
	}
	args["ids_only"] = true
	var thin map[string]any
	if err := json.Unmarshal([]byte(h.MustCall(t, "am_search", args)), &thin); err != nil {
		t.Fatal(err)
	}
	if _, ok := thin["withheld"]; ok {
		t.Errorf("the thin page reports withheld hits over the budget; every hit is on it: %v", thin["withheld"])
	}
	if thin["count"] != full["count"] {
		t.Errorf("thin count %v differs from full count %v", thin["count"], full["count"])
	}
}
