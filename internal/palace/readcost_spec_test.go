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

package palace

import "testing"

// Binding for docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md F-3 — the one
// fact that is a WRITE-PATH invariant and belongs beside the write path.
//
// ⚠ DELIBERATELY RED. See the note in internal/mcpserver/readcost_spec_test.go.

func TestF3ACorrectionLeavesOneCurrentSuccessor(t *testing.T) {
	t.Fatalf("not built yet — F-3 (UC2-S1, UC2-S2): an advertised correction leaves EXACTLY ONE "+
		"current successor linked to the ended predecessor, including under partial failure and "+
		"concurrent correction. Note what this is NOT: a formally superseded record already "+
		"disappears from default reads (survivorsFrom, memory_search.go:70), so this is not a "+
		"ranking fact — ADR-004 and ADR-038 own history ordering and leave it open. The gap is on "+
		"the WRITE side: supersedeInto writes the successor then ends predecessor chunks one at a "+
		"time without atomicity or a compare-and-swap (supersede.go:84-124). Kill it by replacing "+
		"supersession with a plain Add, by skipping one predecessor chunk's ending, or by racing "+
		"two corrections into two current successors%s", "")
}
