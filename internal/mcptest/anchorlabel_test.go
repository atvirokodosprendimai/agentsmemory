package mcptest_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestAnUnlabelledAnchorIsReportedAtWrite is ADR-056 T1, through the real tool
// registry so the assertion is on the wire shape a caller sees. An anchor
// without `repo` can never be attributed to a tree, so it is verified by
// nothing and reports nothing forever; the write is ACCEPTED as today and the
// response says so, with the one call that labels it. The three negative cases
// pin that the keys are absent — not zero — whenever nothing is unlabelled.
func TestAnUnlabelledAnchorIsReportedAtWrite(t *testing.T) {
	h := mcptest.New(t)
	labelled := map[string]any{"path": "internal/retry/retry.go", "snippet": "const budget = 3", "repo": "acme"}
	unlabelled := map[string]any{"path": "internal/retry/retry.go", "snippet": "const budget = 3"}

	t.Run("am_add_drawer reports the count and the remedy", func(t *testing.T) {
		res := h.JSON(t, h.MustCall(t, "am_add_drawer", map[string]any{
			"wing": "wing_anchor", "room": "decisions", "content": "the retry budget is three attempts",
			"code_anchors": []any{labelled, unlabelled},
		}))
		if got, _ := res["anchors_unlabelled"].(float64); got != 1 {
			t.Fatalf("anchors_unlabelled = %v, want 1: %v", res["anchors_unlabelled"], res)
		}
		advice, _ := res["anchors_advice"].(string)
		if !strings.Contains(advice, "am_update_drawer") || !strings.Contains(advice, "repo") {
			t.Fatalf("anchors_advice does not name the call that labels the anchor: %q", advice)
		}
	})

	t.Run("am_update_drawer with code_anchors reports the same", func(t *testing.T) {
		id := firstID(t, h, h.MustCall(t, "am_add_drawer", map[string]any{
			"wing": "wing_anchor", "room": "decisions", "content": "the retry budget is four attempts",
		}))
		res := h.JSON(t, h.MustCall(t, "am_update_drawer", map[string]any{
			"id": id, "code_anchors": []any{unlabelled},
		}))
		if got, _ := res["anchors_unlabelled"].(float64); got != 1 {
			t.Fatalf("anchors_unlabelled = %v, want 1: %v", res["anchors_unlabelled"], res)
		}
		if advice, _ := res["anchors_advice"].(string); !strings.Contains(advice, "repo") {
			t.Fatalf("anchors_advice missing or empty: %v", res)
		}
	})

	for name, args := range map[string]map[string]any{
		"every anchor labelled": {"code_anchors": []any{labelled}},
		"an empty list":         {"code_anchors": []any{}},
		"the field omitted":     {},
	} {
		t.Run("absent when "+name, func(t *testing.T) {
			call := map[string]any{"wing": "wing_anchor", "room": "decisions", "content": "a memory about " + name}
			for k, v := range args {
				call[k] = v
			}
			res := h.JSON(t, h.MustCall(t, "am_add_drawer", call))
			for _, key := range []string{"anchors_unlabelled", "anchors_advice"} {
				if _, present := res[key]; present {
					t.Errorf("%s is present (%v) when nothing is unlabelled; a caller branching on presence reads a finding", key, res[key])
				}
			}
		})
	}
}

// firstID reads the id of the first drawer an am_add_drawer response minted.
func firstID(t *testing.T, h *mcptest.Harness, out string) string {
	t.Helper()
	res := h.JSON(t, out)
	drawers, _ := res["drawers"].([]any)
	if len(drawers) == 0 {
		t.Fatalf("no drawers in %v", res)
	}
	id, _ := drawers[0].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("no id in %v", drawers[0])
	}
	return id
}
