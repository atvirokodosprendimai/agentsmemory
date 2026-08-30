package mcptest_test

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestThePublishedIdempotencyHintMatchesWhatTheToolDoes binds the two declarations
// that carry real weight to observed behaviour, at the same surface a client reads
// them from.
//
// The declaration list is otherwise an OPINION with a reason attached: flipping a
// value there changes both the map and what the chokepoint stamps, so the wire and
// the declaration still agree and every test stays green. That is fine for the
// entries whose answer is a judgement, and not fine for these two, where the answer
// is a property of the store that somebody could change by accident.
//
// diary_write is the one that matters most. Its non-idempotence is not an
// implementation detail — it is the diary exemption in contentKeyOf, and the last
// time that exemption was bypassed two identical reflections became one row while
// the call reported two. Advertising the tool as idempotent would invite a client
// to collapse exactly the retry the store deliberately keeps.
func TestThePublishedIdempotencyHintMatchesWhatTheToolDoes(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_gamma")
	hints := publishedHints(t, h)

	t.Run("diary_write is declared non-idempotent and is", func(t *testing.T) {
		args := map[string]any{
			"agent_name": "hint-probe", "topic": "the same reflection",
			"content": "Filed twice on purpose, to prove two entries come back.",
		}
		first := h.JSON(t, h.MustCall(t, "am_diary_write", args))
		second := h.JSON(t, h.MustCall(t, "am_diary_write", args))

		a, _ := first["entry_id"].(string)
		b, _ := second["entry_id"].(string)
		if a == "" || b == "" {
			t.Fatalf("no entry_id on one of the writes: %v / %v", first, second)
		}
		if a == b {
			t.Fatalf("two identical journal entries returned one id (%s) — the diary exemption "+
				"is bypassed again, and the hint now advertises the wrong contract", a[:12])
		}
		if idempotent(hints["am_diary_write"]) {
			t.Error("am_diary_write publishes idempotentHint=true while two identical calls " +
				"produce two entries. A client may collapse a retry the store keeps on purpose")
		}
	})

	t.Run("add_drawer is declared idempotent and is", func(t *testing.T) {
		args := map[string]any{
			"wing": "wing_gamma", "room": "decisions", "source_file": "hint-probe.md",
			"content": "Re-filing the same source must land on the row already holding it.",
		}
		first := firstDrawerID(t, h, h.MustCall(t, "am_add_drawer", args))
		second := firstDrawerID(t, h, h.MustCall(t, "am_add_drawer", args))
		if first != second {
			t.Errorf("re-filing the same source minted a new id (%s then %s) — every anchor, "+
				"tunnel and fact pointing at the first is now orphaned, and the tool is "+
				"advertised as idempotent", first[:12], second[:12])
		}
		if !idempotent(hints["am_add_drawer"]) {
			t.Error("am_add_drawer publishes idempotentHint=false while re-filing the same " +
				"source is a no-op; a client will avoid a retry that is free")
		}
	})
}

// publishedHints reads the annotations off the live tools/list, so the assertions
// above compare behaviour against what a client is actually told.
func publishedHints(t *testing.T, h *mcptest.Harness) map[string]mcp.ToolAnnotation {
	t.Helper()
	defs, err := h.ListToolDefinitions(t)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	out := make(map[string]mcp.ToolAnnotation, len(defs))
	for _, d := range defs {
		out[d.Name] = d.Annotations
	}
	for _, name := range []string{"am_diary_write", "am_add_drawer"} {
		if _, ok := out[name]; !ok {
			t.Fatalf("%s is not on the published surface — the checks below compare against "+
				"nothing", name)
		}
	}
	return out
}

func idempotent(a mcp.ToolAnnotation) bool {
	return a.IdempotentHint != nil && *a.IdempotentHint
}
