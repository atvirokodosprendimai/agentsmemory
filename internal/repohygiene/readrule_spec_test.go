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

import (
	"os"
	"path/filepath"
	"testing"
)

// Bindings for docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md — the facts
// about MEASUREMENT PROVENANCE, which are properties of the records and the
// counting-rule artifact rather than of palace behaviour.
//
// ⚠ DELIBERATELY RED. See the note in internal/mcpserver/readcost_spec_test.go.

const readRuleNotYetBuilt = "not built yet — %s"

// TestF5ABaselineNamesItsCountingRule binds ADR-044 F-5 (UC3-S1): a baseline
// names the counting rule it was measured under BY CONTENT, not by description.
//
// ⚠ WHAT THIS GATE DOES NOT COVER, said out loud rather than left for a reader to
// assume. F-5 has two clauses — "no mechanism ships before a baseline is
// recorded" AND "the baseline names the rule by content". This checks the second
// and the existence half of the first: the rule resolves, at least one baseline
// exists, and every baseline's citation resolves. It does NOT bind a particular
// mechanism task to a particular baseline; ADR-044's task DAG orders that
// structurally, and a DAG is not a gate. A gate whose name claims more than it
// covers is worse than a narrower one.
//
// The universe of baselines is derived from the directory, never from a list
// beside it, so a baseline added tomorrow joins the check without anyone
// remembering to register it.
func TestF5ABaselineNamesItsCountingRule(t *testing.T) {
	root := repoRoot(t)

	current, err := RuleDigest(root)
	if err != nil {
		t.Fatalf("the counting rule must resolve — %s is the artifact F-5 requires: %v", RulePath, err)
	}

	found, err := Baselines(root)
	if err != nil {
		t.Fatalf("read baselines: %v", err)
	}
	if len(found) == 0 {
		t.Fatalf("no baselines under %s — this gate derives its universe from that directory, so an "+
			"empty result means either the path moved or no baseline has been recorded, and F-5 "+
			"forbids shipping a mechanism in the second case", BaselinesDir)
	}
	for _, b := range found {
		if got := b.Resolve(current); got != CitationResolves {
			t.Errorf("%s: %s — a rate quoted from it compares against a rule that is not the one on "+
				"disk (cited %q, current %q)", b.Path, got, b.CitedDigest, current)
		}
	}
	t.Logf("%d baseline(s) checked against rule %s", len(found), current[:12])

	// The falsifiability half lives INSIDE this test rather than beside it. A
	// sibling test sits outside the acceptance fence, and `adr-verify --mutant`
	// would then return `killed` from a fence that never ran the mutant — a
	// verdict of "the test can fail" obtained without running the thing that
	// proves it. These drive the SAME Baselines/Resolve the corpus half drives,
	// because a falsifiability check that reimplements the logic pins nothing:
	// severing the real function would leave it green.
	t.Run("a_baseline_with_no_citation_is_caught", func(t *testing.T) {
		dir := writeFixtureCorpus(t, "# a baseline that names no rule\n\nfetches: 3\n")
		digest, err := RuleDigest(dir)
		if err != nil {
			t.Fatalf("fixture rule: %v", err)
		}
		got, err := Baselines(dir)
		if err != nil {
			t.Fatalf("fixture baselines: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("fixture should hold exactly one baseline, got %d", len(got))
		}
		if state := got[0].Resolve(digest); state != CitationMissing {
			t.Errorf("a baseline citing no rule must report %q, got %q", CitationMissing, state)
		}
	})

	t.Run("a_citation_resolving_to_nothing_is_caught", func(t *testing.T) {
		dir := writeFixtureCorpus(t, "# a baseline citing a rule that is not there\n\n"+
			"rule-sha256: 0000000000000000000000000000000000000000000000000000000000000000\n")
		digest, err := RuleDigest(dir)
		if err != nil {
			t.Fatalf("fixture rule: %v", err)
		}
		got, err := Baselines(dir)
		if err != nil {
			t.Fatalf("fixture baselines: %v", err)
		}
		if state := got[0].Resolve(digest); state != CitationStale {
			t.Errorf("a baseline citing a digest that is not the current rule must report %q, got %q",
				CitationStale, state)
		}
	})

	t.Run("a_digest_in_prose_is_not_a_citation", func(t *testing.T) {
		// The distinction between a setting and the DISCUSSION of a setting. A
		// baseline explaining what a rule-sha256 line looks like must not thereby
		// be treated as carrying one.
		dir := writeFixtureCorpus(t, "# a baseline discussing citations\n\n"+
			"A baseline cites its rule with rule-sha256: "+
			"1111111111111111111111111111111111111111111111111111111111111111 on its own line.\n")
		got, err := Baselines(dir)
		if err != nil {
			t.Fatalf("fixture baselines: %v", err)
		}
		if got[0].CitedDigest != "" {
			t.Errorf("a digest mentioned mid-sentence must not read as a citation, got %q",
				got[0].CitedDigest)
		}
	})
}

// writeFixtureCorpus builds a throwaway root holding a rule and one baseline, so
// the failure states can be driven without a broken artifact in the real tree.
func writeFixtureCorpus(t *testing.T, baseline string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(RulePath)), 0o755); err != nil {
		t.Fatalf("fixture rule dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, BaselinesDir), 0o755); err != nil {
		t.Fatalf("fixture baselines dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, RulePath), []byte("# a fixture rule\n"), 0o644); err != nil {
		t.Fatalf("write fixture rule: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, BaselinesDir, "fixture.md"), []byte(baseline), 0o644); err != nil {
		t.Fatalf("write fixture baseline: %v", err)
	}
	return dir
}

func TestF6ARuleChangeInvalidatesItsBaselines(t *testing.T) {
	t.Fatalf(readRuleNotYetBuilt, "F-6 (UC3-S2): changing the counting rule invalidates every "+
		"baseline taken under the previous one, the way changing a fence invalidates its recorded "+
		"evidence. Kill it by altering one byte of the active rule while a baseline still cites the "+
		"old identity and watching a rate be quoted anyway")
}
