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

package repohygiene

import "testing"

// Bindings for docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md — the facts
// about MEASUREMENT PROVENANCE, which are properties of the records and the
// counting-rule artifact rather than of palace behaviour.
//
// ⚠ DELIBERATELY RED. See the note in internal/mcpserver/readcost_spec_test.go.

const readRuleNotYetBuilt = "not built yet — %s"

func TestF5ABaselineNamesItsCountingRule(t *testing.T) {
	t.Fatalf(readRuleNotYetBuilt, "F-5 (UC3-S1): a baseline names the counting rule it was measured "+
		"under BY CONTENT, not by description. ADR-041's spec F-3 already forbids shipping a "+
		"mechanism before a baseline — this is the additive half: the rule is a committed artifact "+
		"with an identity. Derive the universe from task metadata rather than a maintained list, so "+
		"a mechanism added tomorrow joins the check. Kill it by recording a baseline with no rule "+
		"reference")
}

func TestF6ARuleChangeInvalidatesItsBaselines(t *testing.T) {
	t.Fatalf(readRuleNotYetBuilt, "F-6 (UC3-S2): changing the counting rule invalidates every "+
		"baseline taken under the previous one, the way changing a fence invalidates its recorded "+
		"evidence. Kill it by altering one byte of the active rule while a baseline still cites the "+
		"old identity and watching a rate be quoted anyway")
}
