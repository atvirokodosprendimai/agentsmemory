package mcpserver

import (
	"os"
	"strings"
	"testing"
)

// TestParseAnchorListSeparatesEmptyFromUnreadable pins the one distinction that
// makes the replace path safe.
//
// parseAnchors is tolerant by design and the rationale is good where it was
// written: an unreadable entry means "no anchors added" and the memory is worth
// more than its anchor. At a REPLACE the same empty result means "delete the
// anchors this memory already has" — so tolerance inverts into data loss, and
// `code_anchors: {…}` instead of `[{…}]` is an ordinary mistake for an LLM
// caller. An unknown recorded as a definite negative is the shape this
// repository has already fixed at the read end; this is it at the write end.
func TestParseAnchorListSeparatesEmptyFromUnreadable(t *testing.T) {
	deliberate := []any{}
	if got, readable, sent, _ := parseAnchorList(deliberate); !readable || len(got) != 0 || sent != 0 {
		t.Errorf("a genuine empty list must read as readable, empty and sent=0, got readable=%v "+
			"len=%d sent=%d — otherwise a deliberate clear becomes impossible", readable, len(got), sent)
	}

	// A non-empty list whose entries are ALL malformed is not a clear. It parses
	// to readable-and-empty and would otherwise delete the memory's anchors — and
	// since most callers send exactly one anchor, this is the likeliest way to get
	// an entry wrong at all: the single anchor sent had a typo. sent is what
	// separates it from [].
	for name, raw := range map[string]any{
		"one entry, key typo":     []any{map[string]any{"paht": "a.go", "snippet": "x"}},
		"one entry, no snippet":   []any{map[string]any{"path": "a.go"}},
		"several, all unreadable": []any{map[string]any{"paht": "a.go"}, "not an object"},
	} {
		got, readable, sent, _ := parseAnchorList(raw)
		if !readable {
			t.Errorf("%s: a list is still a list", name)
		}
		if sent == 0 {
			t.Errorf("%s: sent=0, so this is indistinguishable from [] and clears the anchors", name)
		}
		if len(got) != 0 {
			t.Errorf("%s: parsed %d entries, want 0", name, len(got))
		}
	}

	for name, raw := range map[string]any{
		"object instead of a list": map[string]any{"path": "a.go", "snippet": "x"},
		"string instead of a list": "internal/a.go",
		"nil":                      nil,
		"number":                   float64(3),
	} {
		if _, readable, _, _ := parseAnchorList(raw); readable {
			t.Errorf("%s read as a valid list — at the replace site that clears the memory's "+
				"anchors and reports success", name)
		}
	}

	// Individual malformed ENTRIES stay tolerant: the list was readable, so the
	// caller's intent is clear, and dropping one bad entry is not data loss.
	mixed := []any{
		map[string]any{"path": "a.go", "snippet": "func A() {}"},
		map[string]any{"path": "", "snippet": "no path"},
		"not an object",
	}
	got, readable, sent, _ := parseAnchorList(mixed)
	if sent != 3 {
		t.Errorf("sent=%d, want 3", sent)
	}
	if !readable {
		t.Fatal("a list with some bad entries is still a list")
	}
	if len(got) != 1 {
		t.Errorf("kept %d entries, want 1 — the good one", len(got))
	}
}

// TestAnchorReplacementRefusesRatherThanClears drives the decision itself.
//
// The earlier version of this check read drawers.go and grepped the twenty
// lines above the ReplaceAnchors call for the guard's text. That passes against
// a guard disarmed with "&& false" — the string is still there and the refusal
// never fires — which is the same shape as every other defect this file has
// fixed: the component was pinned, the behaviour was not. So the decision moved
// into anchorReplacement, and this drives it.
func TestAnchorReplacementRefusesRatherThanClears(t *testing.T) {
	for name, raw := range map[string]any{
		"an object where a list belongs": map[string]any{"path": "a.go", "snippet": "x"},
		"a string":                       "a.go:1",
		"one entry, key typo":            []any{map[string]any{"paht": "a.go", "snippet": "x"}},
		"one entry, no snippet":          []any{map[string]any{"path": "a.go"}},
		"several, all unreadable":        []any{map[string]any{"paht": "a.go"}, "not an object"},
	} {
		anchors, _, refusal := anchorReplacement(raw)
		if refusal == "" {
			t.Errorf("%s: accepted, and would REPLACE the memory's anchors with %d — a caller who "+
				"got the argument wrong must not lose the anchors they already had", name, len(anchors))
		}
	}

	// A deliberate clear must still work, or the anchors become unremovable.
	if anchors, _, refusal := anchorReplacement([]any{}); refusal != "" || len(anchors) != 0 {
		t.Errorf("[] must clear: refusal=%q len=%d", refusal, len(anchors))
	}

	// One bad row among several is not the refusal case: something survived.
	mixed := []any{
		map[string]any{"path": "a.go", "snippet": "func A() {}"},
		map[string]any{"path": "", "snippet": "no path"},
	}
	if anchors, _, refusal := anchorReplacement(mixed); refusal != "" || len(anchors) != 1 {
		t.Errorf("a list with one good row must succeed and drop the bad one: refusal=%q len=%d",
			refusal, len(anchors))
	}
}

// TestReplacePathConsultsTheDecision pins the SELECTION: anchorReplacement can
// be correct and unreached. No test here drives an MCP handler, so this reads
// the call site — but everything behavioural now lives in the function above,
// so this only has to answer "is it called, and is its refusal honoured".
func TestReplacePathConsultsTheDecision(t *testing.T) {
	src, err := os.ReadFile("drawers.go")
	if err != nil {
		t.Fatalf("read drawers.go: %v", err)
	}
	body := string(src)

	if n := strings.Count(body, "ReplaceAnchors("); n != 1 {
		t.Fatalf("%d ReplaceAnchors call sites — this check reads the first one only, so a second "+
			"destructive path would be invisible to it", n)
	}
	i := strings.Index(body, "ReplaceAnchors(")
	if i < 0 {
		t.Fatal("no ReplaceAnchors call in drawers.go — this check has stopped checking anything")
	}
	// Anchored to where the argument is read rather than counting lines back: a
	// fixed line count goes red when someone adds a log line between the decision
	// and the call, and a gate with false alarms is one people learn to skip.
	k := strings.LastIndex(body[:i], `args["code_anchors"]`)
	if k < 0 {
		t.Fatal("the code_anchors argument is no longer read above the replace")
	}
	window := body[k:i]

	if !strings.Contains(window, "anchorReplacement(") {
		t.Error("the ReplaceAnchors call site does not consult anchorReplacement — the refusals are " +
			"tested but unreached, and a malformed argument clears the memory's anchors")
	}
	if !strings.Contains(window, `refusal != ""`) {
		t.Error("the ReplaceAnchors call site computes the refusal and does not return on it")
	}
}
