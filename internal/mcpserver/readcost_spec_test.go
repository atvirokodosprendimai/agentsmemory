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

import "testing"

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
	t.Fatalf(readCostNotYetBuilt, "F-1 (UC1-S1): a hit's reported coverage must count the primary "+
		"window AND every region returned. Today `Coverage = len(views[i].Content) / len(fullContent)` "+
		"(drawers.go:929) counts the window only, while regions are rendered separately (:859) — so a "+
		"caller deciding whether it needs a second call decides on an under-reported number. Measured "+
		"2026-08-28: 11-13% by the reported figure, 23-27% actually disclosed. Kill it by reporting "+
		"window-only coverage, or by claiming 1.0 while withholding a region")
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
