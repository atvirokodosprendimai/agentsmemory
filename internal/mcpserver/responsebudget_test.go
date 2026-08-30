package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestWholeMemorySearchStaysWithinTheResponseBudget is the gate for finding F4.
//
// snippet_chars=0 asks for whole memories, which is documented and deliberate.
// What was missing is a ceiling on the PAGE: a memory may be up to
// palace.MaxContentLength (100,000 runes) and MaxSearchLimit is 100, so one
// search could assemble ~10M runes against roughly 1,920 before whole memories
// were returned at all — a ~60x change in the resource envelope with nothing
// bounding it.
//
// The failure it prevents is not a crash. It is a response that spends a large
// share of a session's context on text nobody asked for — and, on clients that
// truncate, one that arrives smaller than it looks. See responseBudget for why the
// number rests on the first of those and not the second.
func TestWholeMemorySearchStaysWithinTheResponseBudget(t *testing.T) {
	srv, ctx := budgetTestServer(t)

	const tool = mcpprotocol.ToolPrefix + "search"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", tool)
	}

	res, err := st.Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: tool,
		Arguments: map[string]any{
			"query":         "budget probe memory content",
			"wing":          budgetWing,
			"limit":         10,
			"snippet_chars": 0, // "give me whole memories"
		},
	}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	body := resultText(res)
	var decoded struct {
		Count int    `json:"count"`
		Note  string `json:"note"`
		Hits  []struct {
			Content    string `json:"content"`
			Truncated  bool   `json:"content_truncated"`
			FullLength int    `json:"content_length"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode search result: %v\n%s", err, body[:min(len(body), 400)])
	}
	if decoded.Count == 0 {
		t.Fatal("no hits; the fixture proves nothing about response size")
	}

	total := 0
	for _, h := range decoded.Hits {
		total += len([]rune(h.Content))
	}
	if total > responseBudget {
		t.Errorf("whole-memory page carried %d runes, over the %d budget — a page this size "+
			"is context the caller never asked to spend, and on a truncating client "+
			"would receive nothing at all", total, responseBudget)
	}

	// Degrading has to be VISIBLE. A silent cap on "give me everything" teaches
	// an agent that the palace is missing content it actually holds.
	trimmed := 0
	for _, h := range decoded.Hits {
		if h.Truncated {
			trimmed++
			if h.FullLength <= len([]rune(h.Content)) {
				t.Errorf("a truncated hit reports full_length %d against %d returned runes, so the "+
					"caller cannot tell how much it is missing", h.FullLength, len([]rune(h.Content)))
			}
		}
	}
	if trimmed == 0 {
		sizes := make([]int, len(decoded.Hits))
		for i, h := range decoded.Hits {
			sizes[i] = len([]rune(h.Content))
		}
		t.Fatalf("nothing was trimmed, so the fixture never reached the %d budget: %d hit(s) "+
			"totalling %d runes, per-hit %v", responseBudget, decoded.Count, total, sizes)
	}
	if !strings.Contains(decoded.Note, "am_get_drawer") {
		t.Errorf("hits were trimmed for size and the response does not say so, or does not say "+
			"how to get the rest: note=%q", decoded.Note)
	}
}

const (
	budgetTeam = "team-budget"
	budgetWing = "wing_alpha"
	budgetRoom = "decisions"
	budgetDim  = 32
)

// budgetTestServer stands up a real search tool over a palace holding several
// memories that are individually legitimate and collectively too large for one
// response — which is the case the budget exists for, and the one no fixture
// built from short drawers can reach.
func budgetTestServer(t *testing.T) (*server.MCPServer, context.Context) {
	t.Helper()
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), budgetDim)

	// Six memories of ~12k runes each: ~72k total, comfortably past the budget,
	// while each one on its own is an ordinary long note.
	//
	// The body is VARIED rather than a repeated phrase. Reassembly removes the
	// overlap between adjacent chunks by matching a suffix against a prefix, and
	// on highly repetitive text that match lands far earlier than the real seam —
	// so a memory built from one repeated sentence comes back a fraction of its
	// stored size and never reaches the budget. That is a property of the fixture,
	// not of the code under test.
	for i := range 6 {
		var body strings.Builder
		for line := 0; body.Len() < 12000; line++ {
			fmt.Fprintf(&body, "budget probe memory %d line %d: content that is long enough to matter "+
				"and distinct enough that overlap removal finds the real seam. ", i, line)
		}
		if _, err := drawers.Add(context.Background(), budgetTeam, palace.AddInput{
			Wing: budgetWing, Room: budgetRoom,
			SourceFile: string(rune('a'+i)) + "-memory",
			Content:    body.String(),
		}); err != nil {
			t.Fatalf("seed memory %d: %v", i, err)
		}
	}

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	// The whole drawer set, not search alone: the budget bounds every response that
	// can grow with the corpus, and registering only the tool under test is how the
	// listing path went unbounded while search was fixed.
	registerDrawers(&registrar{srv: srv}, drawers,
		usage.NewService(usage.NewRepo(gdb), graphTestCaps{}), false)

	ctx := auth.WithTenant(context.Background(), tenant.Tenant{
		TeamID: budgetTeam, UserID: "u1", Role: tenant.RoleAdmin,
	})
	return srv, ctx
}

// resultText flattens a tool result's text content.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestAListingStaysWithinTheResponseBudget is the other half of the same ceiling.
//
// am_search got a budget; am_list_drawers did not, and it returns WHOLE drawers at
// a default limit of 50. Fifty drawers at ChunkSize is ~80,000 runes against a
// budget of 40,000 — so a single listing could spend twice a search page's whole
// context allowance, unasked. On a client that truncates it is worse than costly:
// the result arrives as nothing and reads as "this room holds nothing". Either way
// it is this repository's signature shape — the fix applied to the path somebody
// tested.
func TestAListingStaysWithinTheResponseBudget(t *testing.T) {
	srv, ctx := budgetTestServer(t)

	const tool = mcpprotocol.ToolPrefix + "list_drawers"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", tool)
	}

	res, err := st.Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      tool,
		Arguments: map[string]any{"wing": budgetWing},
	}})
	if err != nil {
		t.Fatalf("list_drawers: %v", err)
	}

	var decoded struct {
		Count   int    `json:"count"`
		Note    string `json:"note"`
		Drawers []struct {
			Content    string `json:"content"`
			Truncated  bool   `json:"content_truncated"`
			FullLength int    `json:"content_length"`
		} `json:"drawers"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The fixture must actually be able to exhibit the defect. A listing that fits
	// under the budget proves nothing, and a passing test over such a fixture is
	// the failure mode this file exists to prevent.
	if decoded.Count < 10 {
		t.Fatalf("fixture returned %d drawers — too few to exceed the budget, so this "+
			"test cannot fail and is not a gate", decoded.Count)
	}

	total := 0
	trimmed := 0
	for _, d := range decoded.Drawers {
		total += len([]rune(d.Content))
		if d.Truncated {
			trimmed++
			if d.FullLength <= len([]rune(d.Content)) {
				t.Errorf("a trimmed drawer reports full_length %d against %d runes returned; "+
					"the caller cannot tell how much it is missing", d.FullLength, len([]rune(d.Content)))
			}
		}
	}
	if total > responseBudget {
		t.Errorf("the listing returned %d runes of content against a %d budget — past the "+
			"transport ceiling the whole result spills to a file and the caller receives "+
			"nothing at all", total, responseBudget)
	}
	if trimmed == 0 {
		t.Fatal("nothing was marked content_truncated on a listing that had to be trimmed; " +
			"a silent trim is the same wrong answer in a smaller size")
	}
	if !strings.Contains(decoded.Note, "am_get_drawer") {
		t.Errorf("note = %q; a trimmed listing must name the way to read a drawer in "+
			"full, or the caller only learns that something is missing", decoded.Note)
	}
}

// TestASnippetPageStaysWithinTheResponseBudget covers the branch the first budget
// test could not see.
//
// snippet_chars is caller-supplied and unclamped, and the original bound was
// conditioned on snippet_chars<=0 — the one branch somebody tested. So
// limit=100 with a large snippet_chars rendered an unbounded page while the check
// looked on, which is the same shape as the listing going unbounded beside search:
// a guard written for the path in front of the author.
func TestASnippetPageStaysWithinTheResponseBudget(t *testing.T) {
	srv, ctx := budgetTestServer(t)

	const tool = mcpprotocol.ToolPrefix + "search"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", tool)
	}

	res, err := st.Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: tool,
		Arguments: map[string]any{
			"query":         "budget probe memory content",
			"wing":          budgetWing,
			"limit":         10,
			"snippet_chars": 100000, // "a window bigger than any memory" — i.e. all of it
		},
	}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	var decoded struct {
		Count int `json:"count"`
		Hits  []struct {
			Content string `json:"content"`
			Regions []struct {
				Text string `json:"text"`
			} `json:"regions"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Count < 3 {
		t.Fatalf("fixture returned %d hits — too few to exceed the budget, so this test "+
			"cannot fail and is not a gate", decoded.Count)
	}

	// Regions count. They are additional rendered text and were outside the total
	// entirely, so a page could pass the old check and still ship several times the
	// budget.
	total := 0
	for _, h := range decoded.Hits {
		total += len([]rune(h.Content))
		for _, r := range h.Regions {
			total += len([]rune(r.Text))
		}
	}
	if total > responseBudget {
		t.Errorf("a snippet page rendered %d runes against a %d budget — snippet_chars is "+
			"caller-supplied and unclamped, so a bound that skips this branch bounds nothing",
			total, responseBudget)
	}
}
