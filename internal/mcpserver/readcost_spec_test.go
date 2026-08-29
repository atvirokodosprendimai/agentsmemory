//go:build readcostspec

// These bindings are DELIBERATELY RED and gated behind a build tag, so `go test
// ./...` — which CI runs on every push to main (.github/workflows/build.yml:65) —
// stays green while they wait for their ADR. Collect them with:
//
//	go test -tags readcostspec ./...
//
// The repository already uses this shape for `contractaxis`. Gating rather than
// deleting keeps them collectable, which is what @spec means: the test exists and
// fails, it just is not in the default lane. Remove the tag in the commit that
// turns them green.

package mcpserver

import (
	"fmt"
	"math"
	"strings"
	"testing"
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
	t.Fatalf(readCostNotYetBuilt, "F-2 (UC1-S2): a hit that does not carry its whole memory must "+
		"say so, report the full length, and carry the id that fetches the rest — never a fragment "+
		"a caller cannot tell is a fragment. Note `am_search` has limit but no cursor "+
		"(drawers.go:786-800), so 'fetch the rest' means am_get_drawer, not paging. Kill it by "+
		"restoring a silent fragment, or by an off-by-one in the reported length")
}

func TestF4ChunkingCreatesNoReassemblyObligation(t *testing.T) {
	t.Fatalf(readCostNotYetBuilt, "F-4 (UC1-S3): a caller never joins chunks to obtain a memory's "+
		"content. Chunk metadata MAY remain as diagnostics — ADR-024 keeps memory_id and "+
		"chunks_matched for compatibility and this fact does not remove them. Kill it by rendering "+
		"h.Drawer.Content in place of the memory's content, so a match in a later chunk returns only "+
		"that chunk")
}

func TestF7APageReportsWhatItWithheld(t *testing.T) {
	t.Fatalf(readCostNotYetBuilt, "F-7 (UC1-S4): a page must report how many hits it withheld. "+
		"`am_search` has limit but no offset or cursor (drawers.go:786-800, M-10) and the spec "+
		"declines to add one (Non-Goals, Grill Log 8), so the count is the ONLY evidence a withheld "+
		"hit existed — without it a page cut short by the response budget is indistinguishable from "+
		"an exhausted corpus. This is a NEW obligation restored from old F-2, kept as its own fact so "+
		"the scope increase is visible rather than folded into an existing binding. Kill it by "+
		"reporting zero withheld on a page that was cut, or by counting hits dropped for relevance "+
		"as withheld — the count is about the BUDGET, not about ranking, which this spec does not touch")
}
