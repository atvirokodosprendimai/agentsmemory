// Bindings for docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md, in the DEFAULT
// LANE — the build tag came off in ADR-044 T6, the last task in this file.
//
// They were gated behind `//go:build readcostspec` while they were deliberately
// red, so `go test ./...` (which CI runs on every push to main,
// .github/workflows/build.yml:65) stayed readable. Gating rather than deleting is
// what @spec means: the test exists and fails, it just is not in the lane that
// gates merges. All four are green now, so the tag would only hide them.
//
// ⚠ THE TAG IS THE SELECTION, and until it came off none of these ran where it
// counts. A binding green only under a tag is not green — it is a test CI has
// never executed, which is this repository's signature defect wearing a test's
// clothes.

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
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

// Bindings for docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md — the facts
// that are MCP RESPONSE CONTRACTS and can only be driven here.
//
// ⚠ THEY LIVE IN THIS PACKAGE ON PURPOSE. An earlier draft put them in
// `internal/palace`, which cannot import `internal/mcpserver` without a cycle —
// so they could only ever have asserted against a helper, never against the
// served response. That is the "implemented, tested and unreachable" shape this
// repository is named for, arriving in the test file rather than the code.
//
// ⚠ DELIBERATELY RED, and a stub that fails is not a test that can fail. When
// each is implemented it must be proved by BREAKING the mechanism named in its
// message and watching it go red — an assertion that passes against correct code
// and never fails against broken code moves the tag to @implemented while
// proving nothing.

const readCostNotYetBuilt = "not built yet — %s"

func TestF1CoverageCountsEveryDisclosedRange(t *testing.T) {
	// A memory long enough to be windowed, whose runes are their own addresses:
	// full[n] identifies position n, so a fixture can name a range and the
	// assertion can be read without counting characters.
	full := addressable(2000)
	total := float64(len([]rune(full)))

	// The shapes SnippetWithHead actually produces, plus the region shapes
	// SnippetRegions puts beside them. Each `content` is a verbatim slice-join of
	// `full` with the same markers the render path adds, because coveredRunes
	// recovers the window's offsets from the rendered text and a fixture that
	// skipped the markers would test an input the server never emits.
	cases := []struct {
		name    string
		content string
		regions []regionView
		want    float64
		why     string
	}{{
		name:    "a window with no regions is the window",
		content: string([]rune(full)[0:100]) + "…",
		want:    100 / total,
		why:     "unchanged from the arithmetic this replaces: with nothing beside it, the window IS the disclosure",
	}, {
		name:    "a region disclosed beside the window is counted",
		content: string([]rune(full)[0:100]) + "…",
		regions: []regionView{{Text: string([]rune(full)[300:400]), Start: 300}},
		want:    200 / total,
		why: "THE DEFECT. The region is in the same response, in front of the same caller. " +
			"Reporting 100/2000 tells an agent it holds 5% of a memory it holds 10% of, and the " +
			"decision that number exists to inform is whether to spend a second call",
	}, {
		name:    "the head-joined two-range window is mapped back to both ranges",
		content: string([]rune(full)[0:120]) + " … " + string([]rune(full)[1000:1300]) + "…",
		regions: []regionView{{Text: string([]rune(full)[1500:1600]), Start: 1500}},
		want:    520 / total,
		why: "the real shape of a hit whose match is not at the top: SnippetWithHead keeps the " +
			"identity and joins the matching window to it. Both halves are disclosed; the join " +
			"marker is not part of the memory and must not be counted",
	}, {
		name:    "a whole memory is 1",
		content: full,
		want:    1,
		why:     "ADR-019's rule, kept: snippet_chars=0 must not report the same figure as showing none of it",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := coveredRunes(c.content, c.regions, full)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("coverage = %v, want %v\n%s", got, c.want, c.why)
			}
			if got > 1 {
				t.Errorf("coverage = %v, above 1 — a fraction of a memory cannot exceed the memory", got)
			}
		})
	}

	// The inverse defect, as a subtest so it is inside the acceptance fence
	// rather than a sibling the fence's -run never reaches.
	t.Run("a memory whose own prose contains the elision marker is still 1", func(t *testing.T) {
		// ⚠ THE SEPARATOR IS STRUCTURE IN A RENDERED SNIPPET AND CONTENT IN A MEMORY
		// THAT QUOTES ONE, and this corpus is full of memories that quote one — review
		// notes, adr-debt output, transcripts. Recovering the window's offsets by
		// splitting the rendered string on " … " cuts one contiguous slice in two and
		// loses the separator's runes to neither piece.
		//
		// Measured before the fix: a memory returned WHOLE reported 0.9806. The caller
		// asked for all of it, received all of it, and was told it held 98% — this
		// binding's own failure sentence one direction over, reachable on the live
		// corpus today rather than hypothetically.
		full := "The review said adr-debt … 0 broken, exit 0, and went on at length about why " +
			"the corpus was fine, which is the sort of sentence this palace is full of."
		if got := coveredRunes(full, nil, full); got != 1 {
			t.Errorf("coverage for a memory returned WHOLE = %.4f, want 1 — its prose quotes an "+
				"elision and the offsets were recovered by splitting on that marker", got)
		}
	})

	t.Run("a later window is located after the piece before it", func(t *testing.T) {
		// strings.Index takes the FIRST occurrence, so a later window whose text also
		// appears earlier in the memory was located at the wrong offset. It can only
		// under-report, which is why it was not urgent — but the recovery is ambiguous
		// by construction and the pieces are known to arrive in order.
		// BOTH PIECES MUST BE AMBIGUOUS or the cursor is never exercised: the first
		// draft's second piece carried a unique tail, so it located correctly with or
		// without the cursor and the mutant survived. Here the two pieces are the same
		// text, which is the only shape that can tell the two implementations apart.
		const repeated = "the same sentence appears twice. "
		full := repeated + "filler between the two copies. " + repeated
		content := repeated + snippetJoin + repeated
		got := coveredRunes(content, nil, full)
		// Both copies disclosed, the filler between them not.
		want := float64(2*len([]rune(repeated))) / float64(len([]rune(full)))
		if diff := got - want; diff < -0.0001 || diff > 0.0001 {
			t.Errorf("coverage = %.4f, want %.4f — the later piece was located at the FIRST "+
				"occurrence of its text, so its range overlapped the opening's and the union "+
				"reported less than was disclosed", got, want)
		}
	})

	t.Run("an overlapping region is not double counted", func(t *testing.T) {
		content := string([]rune(full)[0:200]) + "…"
		regions := []regionView{{Text: string([]rune(full)[50:150]), Start: 50}}
		got := coveredRunes(content, regions, full)
		if want := 200 / total; math.Abs(got-want) > 1e-9 {
			t.Errorf("coverage = %v, want %v — the region lies wholly inside the window, so the "+
				"caller received 200 runes and not 300. Summing them reports MORE of the memory "+
				"than was disclosed, which reads as completeness and is worse than the "+
				"under-report it would replace", got, want)
		}
	})

	t.Run("a withheld region keeps coverage below 1", func(t *testing.T) {
		// The binding's second kill-case, stated as its own assertion: a response
		// that withheld anything may not claim the memory is fully disclosed.
		content := string([]rune(full)[0:1000]) + "…"
		regions := []regionView{{Text: string([]rune(full)[1200:1900]), Start: 1200}}
		if got := coveredRunes(content, regions, full); got >= 1 {
			t.Errorf("coverage = %v with 300 runes never rendered — a caller reads 1.0 as "+
				"\"there is nothing more to fetch\"", got)
		}
	})
}

// addressable builds a memory whose text encodes its own offsets, so a fixture
// can slice by position and a failure can be read without counting characters.
func addressable(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%06d the memory continues here with prose that is not filler-shaped. ", i)
	}
	return string([]rune(b.String())[:n])
}

func TestF2NoHitIsSilentlyPartial(t *testing.T) {
	// The marking, not the paths that call it: partialWithFetchID is the single
	// place the three hand-written sites were unified into, so this is where an
	// off-by-one or a half-set pair would live.
	t.Run("a marked view carries both fields, never one", func(t *testing.T) {
		var v drawerView
		v.Content = "a fragment"
		partialWithFetchID(&v, 60237)
		if !v.Truncated {
			t.Error("a view that carries less than its memory is not marked — a caller reads an " +
				"unmarked response as complete, which is the whole defect")
		}
		if v.FullLength != 60237 {
			t.Errorf("content_length = %d, want 60237. drawerView's own comment requires both "+
				"fields or neither: \"truncated\" without the original length says something is "+
				"missing and not how much, which is not enough to decide whether to fetch it",
				v.FullLength)
		}
		if v.ID != "" {
			t.Error("the marking invented an id; the fetch id is the view's own, which the " +
				"caller already holds")
		}
	})

	t.Run("the reported length is the memory's, not the fragment's", func(t *testing.T) {
		// The binding's named kill-case. A marking that reported len(content) would
		// tell a caller the fragment it holds IS the memory.
		var v drawerView
		v.Content = "1600 runes worth of chunk"
		partialWithFetchID(&v, 60237)
		if v.FullLength == len([]rune(v.Content)) {
			t.Errorf("content_length = %d, the length of the fragment rather than of the memory "+
				"— a caller comparing what it holds against what exists would conclude it holds "+
				"all of it", v.FullLength)
		}
	})
}

// The ROOT-chunk case — a marking keyed on ParentID would leave chunk 0 of a
// 47-chunk memory looking complete — is driven against the REAL am_get_drawer in
// internal/mcptest (TestScenarioAFetchedChunkSaysItIsOne). It cannot live here:
// mcptest imports this package, so a binding in package mcpserver can exercise
// the marking but never the handler that selects it.

// f4Team/f4Wing hold the one memory this binding needs: several chunks long, with
// a needle that appears ONLY in the last of them.
const (
	f4Team = "team-f4"
	// Reuses the neutral example this package already uses. A fixture wing named
	// for the task would be a project name in the corpus, which
	// TestNoRealProjectNamesInWings refuses — correctly, and it caught this one.
	f4Wing = budgetWing
	f4Room = "decisions"
	// The needle is nonsense on purpose. A phrase that also occurs in the opening
	// would make the test pass on a fixture that never exercised a later chunk.
	f4Needle = "zarquon threadbare palimpsest"
	// The marker proves the OPENING came back too. Together with the needle it is
	// what separates "the memory" from "the chunk that matched".
	f4Opening = "OPENING MARKER lodestar vestibule"
)

// f4Memory builds a memory several times ChunkSize, opening with f4Opening and
// closing with f4Needle, and varied line by line in between.
//
// ⚠ VARIED, NOT REPEATED. `strings.Repeat` of one sentence produces byte-identical
// chunks that collapse under content-addressed dedupe, so the memory comes back a
// fraction of its stored size and the test asserts against a corpus that does not
// exist. That is a property of the fixture, not of the code, and it cost this
// repository an hour on 2026-08-29.
func f4Memory() string {
	var b strings.Builder
	b.WriteString(f4Opening + ": this memory is deliberately longer than several chunks. ")
	for line := 0; b.Len() < palace.ChunkSize*5; line++ {
		fmt.Fprintf(&b, "filler line %d carrying distinct wording so overlap removal finds the real seam "+
			"rather than an early false match. ", line)
	}
	b.WriteString(" " + f4Needle + " appears here and nowhere else in this memory.")
	return b.String()
}

// TestF4ChunkingCreatesNoReassemblyObligation is F-4 (UC1-S3): a caller never joins
// chunks to obtain a memory's content.
//
// ⚠ THIS TASK PINS A GUARANTEE RATHER THAN BUILDING ONE, and that is the honest
// outcome T6 step 2 names in advance. ADR-013 collapses a page to one hit per
// memory and ADR-024 ranks memories rather than chunks, so the render loop already
// reads h.MemoryContent with h.Drawer.Content only as a fallback
// (drawers.go, the `fullContent` assignment). These assertions went green the
// moment they were written — no behaviour changed, and inventing a change to
// justify the task would have been the wrong repair.
//
// So the evidence that they BIND is not a red-then-green pair. It is the mutant the
// binding names: render h.Drawer.Content in place of the memory's content and watch
// this go red. That mutant is in the Mutation Log, tool-written.
func TestF4ChunkingCreatesNoReassemblyObligation(t *testing.T) {
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), budgetDim)
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{
		TeamID: f4Team, UserID: "u1", Role: tenant.RoleAdmin,
	})

	stored := f4Memory()
	added, err := drawers.Add(ctx, f4Team, palace.AddInput{
		Wing: f4Wing, Room: f4Room, SourceFile: "f4-memory", Content: stored,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A one-chunk memory cannot exercise this fact at all: "the chunk" and "the
	// memory" would be the same string and every assertion below would pass on a
	// fixture that never posed the question.
	if len(added.Drawers) < 3 {
		t.Fatalf("fixture stored %d chunk(s) against ChunkSize %d — F-4 is about a match in a "+
			"LATER chunk, so a memory this short cannot exercise it", len(added.Drawers), palace.ChunkSize)
	}

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	registerDrawers(&registrar{srv: srv}, drawers,
		usage.NewService(usage.NewRepo(gdb), graphTestCaps{}), false)
	const tool = mcpprotocol.ToolPrefix + "search"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", tool)
	}

	// snippet_chars=0 asks for the whole memory, which is the request that makes
	// "did I receive the memory or the chunk" a question with a checkable answer.
	res, err := st.Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: tool, Arguments: map[string]any{
			"query": f4Needle, "wing": f4Wing, "limit": 5, "snippet_chars": 0,
		}}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var page struct {
		Count int `json:"count"`
		Hits  []struct {
			Content       string `json:"content"`
			MemoryID      string `json:"memory_id"`
			ParentID      string `json:"parent_id"`
			ChunkIndex    int    `json:"chunk_index"`
			ChunksMatched int    `json:"chunks_matched"`
		} `json:"hits"`
	}
	body := resultText(res)
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode search result: %v\n%s", err, body[:min(len(body), 400)])
	}
	if page.Count == 0 {
		t.Fatalf("the needle matched nothing; this page proves nothing about reassembly\n%s",
			body[:min(len(body), 400)])
	}

	// ONE hit per memory. Several chunks matching must not become several hits the
	// caller has to recognise as siblings and stitch — that recognition IS the
	// reassembly obligation this fact removes.
	if page.Count != 1 {
		t.Errorf("a match inside one memory returned %d hits; a caller now has to work out "+
			"they are chunks of one record and join them", page.Count)
	}

	hit := page.Hits[0]
	// The content is the MEMORY's, which is checkable in both directions: the
	// needle proves the matching chunk is present, and the opening proves the
	// chunks BEFORE it came too. Either alone passes on the chunk.
	if !strings.Contains(hit.Content, f4Needle) {
		t.Errorf("the hit that matched %q does not contain it", f4Needle)
	}
	if !strings.Contains(hit.Content, f4Opening) {
		t.Errorf("a match in a LATER chunk returned content without the memory's opening — " +
			"the caller received the chunk that matched, and obtaining the memory would mean " +
			"fetching and joining its siblings")
	}
	if got, want := len([]rune(hit.Content)), len([]rune(stored)); got != want {
		t.Errorf("content is %d runes against %d stored; a whole-memory request must return "+
			"the memory, not a chunk of it and not a lossy rejoin", got, want)
	}

	t.Run("chunk metadata survives as diagnostics", func(t *testing.T) {
		// F-4 removes the OBLIGATION to join chunks; it does not remove the
		// evidence that chunking happened. ADR-024 keeps these for compatibility
		// and internal/mcptest/regions_test.go reads chunk_index over the real
		// transport, so dropping them here would break a consumer to satisfy a
		// fact that never asked for it.
		if hit.MemoryID == "" {
			t.Error("memory_id is absent; the id that groups a memory's chunks is the one " +
				"diagnostic a caller cannot reconstruct")
		}
		if hit.ChunksMatched < 1 {
			t.Errorf("chunks_matched = %d; it reports how many of a memory's chunks matched "+
				"and is retained by ADR-024", hit.ChunksMatched)
		}
		// chunk_index is retained rather than asserted non-zero: which chunk won is
		// a ranking outcome, and pinning it here would make this binding fail on a
		// ranking change it does not govern.
		if hit.ChunkIndex < 0 {
			t.Errorf("chunk_index = %d, which is not a chunk position", hit.ChunkIndex)
		}
	})
}

func TestF7APageReportsWhatItWithheld(t *testing.T) {
	// F-7 (UC1-S4). The count is the ONLY evidence a withheld hit existed:
	// `am_search` has limit but no offset or cursor, and the spec declines to add
	// one (Non-Goals, Grill Log 8), so without it a page cut short by the response
	// budget is indistinguishable from an exhausted corpus.
	//
	// ⚠ THE RECORD'S DEFINITION DOES NOT MATCH THIS CODE, and the deviation is
	// written into the task file. T5's Affected Files says "a withheld hit is not
	// on the page". The render loop never DROPS a hit: past the budget
	// `headWithin` returns the empty string with cut=true, so the hit arrives with
	// its id, its metadata and ZERO runes of the memory. Withheld therefore means
	// ON THE PAGE CARRYING NOTHING — which is already house vocabulary, since
	// am_list_drawers' own description says "as much of their opening as the
	// budget still allows — possibly none".
	//
	// ⚠ DRIVEN THROUGH THE REGISTERED HANDLER, not through a helper. A test of a
	// counting helper stays green while the call site in registerSearch reverts to
	// reporting nothing, which is the "implemented, tested and unreachable" shape
	// this repository is named for.
	// The key is spelled out rather than referencing the production constant: a
	// test that imports the name it is checking goes red by failing to COMPILE,
	// which proves the symbol is missing and nothing about the behaviour.
	const withheldByBudget = "budget"

	srv, ctx := budgetTestServer(t)
	const tool = mcpprotocol.ToolPrefix + "search"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", tool)
	}

	search := func(args map[string]any) f7Page {
		t.Helper()
		res, err := st.Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: tool, Arguments: args,
		}})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		var page f7Page
		body := resultText(res)
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("decode search result: %v\n%s", err, body[:min(len(body), 400)])
		}
		return page
	}

	// snippet_chars past any memory's length asks for all of every hit, so the
	// budget is spent by the third memory and the tail arrives empty. This is the
	// branch that produces a zero-rune hit at all; a fixture that never reaches it
	// makes every assertion below vacuous, which the counts guard against.
	page := search(map[string]any{
		"query": "budget probe memory content", "wing": budgetWing,
		"limit": 10, "snippet_chars": 100000,
	})

	empty, trimmed := 0, 0
	for _, h := range page.Hits {
		switch {
		case len([]rune(h.Content)) == 0:
			empty++
		case h.Truncated:
			trimmed++
		}
	}
	if empty == 0 {
		sizes := make([]int, len(page.Hits))
		for i, h := range page.Hits {
			sizes[i] = len([]rune(h.Content))
		}
		t.Fatalf("no hit came back empty, so this fixture never exhausted the %d-rune budget "+
			"and the assertions below cannot fail: %d hit(s), per-hit runes %v",
			ResponseBudget, page.Count, sizes)
	}
	if trimmed == 0 {
		t.Fatalf("no hit was trimmed-but-nonempty, so this page cannot distinguish the two " +
			"counters and the conflation the record warns about would pass here")
	}

	if page.Withheld == nil {
		t.Fatalf("a page that delivered %d hit(s) carrying nothing reported no withheld count "+
			"— it is indistinguishable from an exhausted corpus, which is the whole of F-7", empty)
	}
	if got := page.Withheld[withheldByBudget]; got != empty {
		t.Errorf("withheld[%q] = %d against %d hit(s) that arrived carrying nothing",
			withheldByBudget, got, empty)
	}

	// THE CONFLATION, as an assertion rather than a warning. The note's number is
	// the trimmed count an agent actually reads; a hit that was first counted
	// over-budget and then emptied must appear in exactly one of the two.
	if n := f7NoteCount(t, page.Note); n != trimmed {
		t.Errorf("the note reports %d trimmed hit(s) against %d that are on the page with "+
			"less than the whole memory — a hit counted as both trimmed and withheld makes "+
			"both numbers wrong while each looks plausible.\n  note=%q", n, trimmed, page.Note)
	}

	// The remedy has to be nameable. A withheld hit IS resumable by id, which is
	// better than Grill Log 8 assumed when it declined a cursor.
	if !strings.Contains(page.Note, "am_get_drawer") {
		t.Errorf("hits were withheld and the note does not name the call that retrieves "+
			"them: note=%q", page.Note)
	}

	t.Run("hits dropped by limit are not withheld", func(t *testing.T) {
		// The kill-case the binding names: the count is about the BUDGET, not about
		// ranking. Four of the six fixture memories never reach this page, and none
		// of them is withheld.
		page := search(map[string]any{
			"query": "budget probe memory content", "wing": budgetWing, "limit": 2,
		})
		if page.Count != 2 {
			t.Fatalf("limit=2 returned %d hits; the negative case needs the limit to bite",
				page.Count)
		}
		for _, h := range page.Hits {
			if len([]rune(h.Content)) == 0 {
				t.Fatal("a windowed hit came back empty; this page cannot isolate the limit")
			}
		}
		if page.Withheld != nil {
			t.Errorf("withheld = %v on a page the budget never cut — hits excluded by limit, "+
				"by relevance or by the history filter are not withheld, and counting them "+
				"makes the number mean nothing", page.Withheld)
		}
	})
}

// f7Page is the am_search response as F-7 reads it.
type f7Page struct {
	Count    int            `json:"count"`
	Note     string         `json:"note"`
	Withheld map[string]int `json:"withheld"`
	Hits     []struct {
		Content    string `json:"content"`
		Truncated  bool   `json:"content_truncated"`
		FullLength int    `json:"content_length"`
	} `json:"hits"`
}

// f7NoteCount reads the trimmed-hit count out of the page note, which is the
// number an agent actually sees. Reading the prose rather than a second field is
// deliberate: the note is the surface, and a note that drifts from the counter is
// the same defect as a wrong counter.
func f7NoteCount(t *testing.T, note string) int {
	t.Helper()
	m := regexp.MustCompile(`last (\d+) hit`).FindStringSubmatch(note)
	if m == nil {
		t.Fatalf("the note does not report how many hits were trimmed: %q", note)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("note count %q is not a number: %v", m[1], err)
	}
	return n
}
