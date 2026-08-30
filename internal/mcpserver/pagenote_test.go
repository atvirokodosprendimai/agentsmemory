package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestAPageNoteIsAssembledNotClobbered covers the page's `note` as a whole,
// which nothing did — each writer of it had a test, the interaction between them
// had none.
//
// Two defects lived in that gap. The withheld sentence stitched itself onto
// whatever `note` already held via fmt.Sprint, so on a page with NO trimmed hits
// it printed the literal string "<nil>" as its first word — shipped, and caught in
// review rather than by this suite. And the four sites that write `note` do so by
// assignment, so the last one wins and a page that has two things to say says one.
//
// The rule this pins: a page may have SEVERAL things to say about itself, every
// one of them reaches the caller, and none of them is assembled by formatting
// another one.
func TestAPageNoteIsAssembledNotClobbered(t *testing.T) {
	srv, ctx := budgetTestServer(t)
	const tool = mcpprotocol.ToolPrefix + "search"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", tool)
	}

	page := func(args map[string]any) (note string, withheld map[string]int, hits int) {
		t.Helper()
		res, err := st.Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tool, Arguments: args}})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		var p struct {
			Count    int            `json:"count"`
			Note     string         `json:"note"`
			Withheld map[string]int `json:"withheld"`
			Hits     []struct {
				Content   string `json:"content"`
				Truncated bool   `json:"content_truncated"`
			} `json:"hits"`
		}
		body := resultText(res)
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("decode: %v\n%s", err, body[:min(len(body), 300)])
		}
		return p.Note, p.Withheld, p.Count
	}

	t.Run("a withheld-only page does not print a nil note", func(t *testing.T) {
		// snippet_chars well under a memory's length: every hit is windowed by the
		// SNIPPET path, so the whole-memory branch never fires and `note` is unset
		// when the withheld sentence is appended. That is the shape that printed
		// "<nil>", and the shape no fixture in this package produced.
		note, withheld, _ := page(map[string]any{
			"query": "budget probe memory content", "wing": budgetWing,
			"limit": 10, "snippet_chars": 4000,
		})
		if withheld == nil {
			t.Skip("this fixture withheld nothing at snippet_chars=4000; the case needs a page " +
				"the budget cut without the whole-memory branch firing")
		}
		if strings.Contains(note, "<nil>") {
			t.Errorf("the page note contains the literal %q — a Go zero value formatted into "+
				"prose an agent reads:\n%s", "<nil>", note)
		}
		if strings.HasPrefix(strings.TrimSpace(note), "the last") {
			t.Errorf("the note opens mid-sentence, so it was built by appending to something "+
				"that was not there:\n%s", note)
		}
	})

	t.Run("a page that has two things to say says both", func(t *testing.T) {
		// Whole memories requested AND the budget exhausted: some hits are trimmed
		// and some arrive empty. Assignment would let the second sentence replace
		// the first, and a caller would be told about one degradation of two.
		note, withheld, _ := page(map[string]any{
			"query": "budget probe memory content", "wing": budgetWing,
			"limit": 10, "snippet_chars": 100000,
		})
		if withheld == nil {
			t.Fatal("fixture withheld nothing; this case cannot exercise two notes at once")
		}
		if !strings.Contains(note, "windowed instead") {
			t.Errorf("the trimmed-hits sentence is missing from a page that trimmed hits — the "+
				"withheld sentence replaced it:\n%s", note)
		}
		if !strings.Contains(note, "carrying NO content") {
			t.Errorf("the withheld sentence is missing from a page that withheld hits:\n%s", note)
		}
		if strings.Contains(note, "<nil>") {
			t.Errorf("the page note contains a formatted zero value:\n%s", note)
		}
	})
}
