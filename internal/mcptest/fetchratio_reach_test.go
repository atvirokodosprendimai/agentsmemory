package mcptest_test

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestTheFetchRateReachesTheToolSurfaceWithItsPopulation is rung 2 for ADR-028
// T4: it proves the ratio is SELECTED, not merely computed.
//
// internal/palace's TestTheFetchRatioNamesItsPopulation calls
// Service.FetchRatesByProfile directly, which is the shape AGENTS.md names as
// this repository's characteristic defect — the component exercised instead of
// the selection. Delete the FetchRatesByProfile call from registerRecallStats,
// or drop `fetch_rates` from the response map, and every assertion in that test
// still passes while no operator can ever see a rate. This drives the real MCP
// transport and reads the number back through the served tool, so it dies when
// the wiring dies.
//
// It also pins the property that makes the number quotable at all, and the one a
// unit test is worst placed to check: the rate MOVES with the denominator. A
// second recall that nobody fetched from must halve it. An instrument that only
// ever reports 1.0 is indistinguishable from one returning a constant, which is
// the failure ADR-028's own step 5 demands a canary against.
func TestTheFetchRateReachesTheToolSurfaceWithItsPopulation(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")

	id := firstDrawerID(t, h, h.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_acme", "room": "decisions",
		"content": "a fetch rate is only readable beside the ranking that produced it",
	}))

	// The profile the server says it is running under. Binding the two surfaces
	// is the point: a `profile_id` that does not match what am_status publishes
	// names a configuration the operator cannot look up, which is the same defect
	// as publishing no profile at all.
	status := h.JSON(t, h.MustCall(t, "am_status", map[string]any{}))
	ranking, _ := status["ranking"].(string)
	if ranking == "" {
		t.Fatal("am_status published no ranking profile; there is nothing for a rate to name")
	}

	page := h.JSON(t, h.MustCall(t, "am_search", map[string]any{
		"query": "a fetch rate is only readable beside the ranking", "wing": "wing_acme",
	}))
	searchID, _ := page["search_id"].(string)
	if searchID == "" {
		t.Fatal("am_search returned no search_id; ADR-028 T1 is the thing that broke, not this")
	}
	h.MustCall(t, "am_get_drawer", map[string]any{"id": id, "search_id": searchID})

	rate := onlyFetchRate(t, h)
	if got, _ := rate["profile_id"].(string); got != ranking {
		t.Errorf("fetch_rates named profile %q; am_status says this server ranks with %q", got, ranking)
	}
	// One logged recall, fetched: the instrument reporting a POSITIVE. Every zero
	// this tool ever publishes is read against the fact that this line moved.
	if logged, fetched := num(t, rate, "recalls_logged"), num(t, rate, "recalls_fetched"); logged != 1 || fetched != 1 {
		t.Fatalf("one recall, fetched once, must read 1/1; got %v/%v", logged, fetched)
	}
	if got := num(t, rate, "rate"); got != 1 {
		t.Errorf("rate = %v; want 1 for a single recall that was fetched", got)
	}

	// A second recall nobody read from. The numerator must hold and the
	// denominator must grow — a rate that ignores unfetched recalls is a count
	// wearing a percent sign.
	h.MustCall(t, "am_search", map[string]any{
		"query": "a question this wing does not answer at all", "wing": "wing_acme",
	})
	rate = onlyFetchRate(t, h)
	if logged, fetched := num(t, rate, "recalls_logged"), num(t, rate, "recalls_fetched"); logged != 2 || fetched != 1 {
		t.Fatalf("a second unfetched recall must enter the denominator only; want 2/1 got %v/%v", logged, fetched)
	}
	if got := num(t, rate, "rate"); got != 0.5 {
		t.Errorf("rate = %v; want 0.5 — the number must move with its denominator", got)
	}
}

// onlyFetchRate reads am_recall_stats and returns the single fetch_rates entry.
//
// It insists on exactly one because this harness runs one server under one
// ranking: a second entry would mean the report grouped by something other than
// the profile, which is the error the whole task exists to prevent.
func onlyFetchRate(t *testing.T, h *mcptest.Harness) map[string]any {
	t.Helper()
	stats := h.JSON(t, h.MustCall(t, "am_recall_stats", map[string]any{"hours": 24}))
	raw, ok := stats["fetch_rates"].([]any)
	if !ok {
		t.Fatalf("am_recall_stats published no `fetch_rates` list (the handler is not calling "+
			"FetchRatesByProfile, and no palace test can see that): %v", stats)
	}
	if len(raw) != 1 {
		t.Fatalf("one server runs one ranking, so want exactly one fetch_rates entry, got %d: %v", len(raw), raw)
	}
	entry, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatalf("fetch_rates entry is not an object: %v", raw[0])
	}
	return entry
}

// num reads one numeric field, failing when it is absent rather than defaulting
// to zero — a missing denominator that reads as 0 is exactly the population
// error ADR-007 forbids, and it must not be able to hide inside this helper.
func num(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("fetch_rates entry carries no %q: %v", key, m)
	}
	return v
}
