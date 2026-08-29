// Bindings for ADR-044 (make a small read trustworthy), from
// docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md — the facts about
// MEASUREMENT PROVENANCE, which are properties of the records and the
// counting-rule artifact rather than of palace behaviour.
//
// The `//go:build readcostspec` tag these bindings shipped behind was removed in
// ADR-044 T2, which is the LAST task in this file: F-5 and F-6 are both green, so
// they belong in the lane CI runs on every push rather than in one collected by
// hand. The sibling files keep their tag until their own last task — T6 for
// internal/mcpserver, T7 for internal/palace — because removing it while a
// binding in the same file is still red would put a deliberately-failing test
// into the default lane.

package repohygiene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestF6ARuleChangeInvalidatesItsBaselines binds ADR-044 F-6 (UC3-S2): changing
// the counting rule invalidates every baseline taken under the previous one, the
// way changing an acceptance fence invalidates its recorded evidence.
//
// The scenario is driven end to end rather than asserted on a field: a fixture
// rule and a baseline citing it, then ONE BYTE of the rule altered, then the
// quote attempted. Asserting that Resolve returns a particular constant would
// prove the switch statement stores the value and nothing more — the behaviour
// that matters is that quoting is REFUSED and the refusal names the rule change.
func TestF6ARuleChangeInvalidatesItsBaselines(t *testing.T) {
	dir := t.TempDir()
	rulePath := filepath.Join(dir, RulePath)
	if err := os.MkdirAll(filepath.Dir(rulePath), 0o755); err != nil {
		t.Fatalf("fixture rule dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, BaselinesDir), 0o755); err != nil {
		t.Fatalf("fixture baselines dir: %v", err)
	}
	if err := os.WriteFile(rulePath, []byte("# a fixture rule\n\ncounts: reads acted on\n"), 0o644); err != nil {
		t.Fatalf("write fixture rule: %v", err)
	}

	before, err := RuleDigest(dir)
	if err != nil {
		t.Fatalf("digest before: %v", err)
	}
	baseline := filepath.Join(dir, BaselinesDir, "fixture.md")
	if err := os.WriteFile(baseline, []byte("# a baseline\n\nrule-sha256: "+before+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture baseline: %v", err)
	}

	found, err := Baselines(dir)
	if err != nil {
		t.Fatalf("baselines: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("fixture should hold exactly one baseline, got %d", len(found))
	}
	if err := found[0].QuoteRate(before); err != nil {
		t.Fatalf("a baseline citing the rule it was measured under must be quotable: %v", err)
	}

	// One byte. Not a rewrite — the point is that any content change is a rule
	// change, because the digest is the identity.
	if err := os.WriteFile(rulePath, []byte("# a fixture rule\n\ncounts: reads acted ON\n"), 0o644); err != nil {
		t.Fatalf("alter fixture rule: %v", err)
	}
	after, err := RuleDigest(dir)
	if err != nil {
		t.Fatalf("digest after: %v", err)
	}
	if after == before {
		t.Fatalf("altering the rule's content must change its identity; both digests are %s", before)
	}

	err = found[0].QuoteRate(after)
	if err == nil {
		t.Fatal("a rate quoted across a rule change must be refused — this is the F-6 kill-case: " +
			"one byte of the active rule altered while the baseline still cites the old identity, " +
			"and the rate quoted anyway")
	}
	if !strings.Contains(err.Error(), "the rule changed") {
		t.Errorf("the refusal must NAME the rule change rather than report a comparison, got: %v", err)
	}

	t.Run("a_reformat_that_changes_no_words_does_not_invalidate", func(t *testing.T) {
		// The inverse failure, and the one that makes a gate get switched off: if
		// trailing whitespace or a CRLF moved the digest, every baseline in the
		// tree would go invalid on an editor setting.
		if err := os.WriteFile(rulePath, []byte("# a fixture rule\r\n\r\ncounts: reads acted ON   \n"), 0o644); err != nil {
			t.Fatalf("reformat fixture rule: %v", err)
		}
		reformatted, err := RuleDigest(dir)
		if err != nil {
			t.Fatalf("digest after reformat: %v", err)
		}
		if reformatted != after {
			t.Errorf("a reformat that changes no words must not change the rule's identity: %s != %s",
				short(reformatted), short(after))
		}
	})

	t.Run("an_uncited_baseline_is_refused_differently", func(t *testing.T) {
		// "No citation" and "stale citation" must stay distinguishable: a gate that
		// reports one where the other happened is reporting an observation it did
		// not make.
		uncited := Baseline{Path: "docs/measurement/baselines/uncited.md"}
		err := uncited.QuoteRate(after)
		if err == nil {
			t.Fatal("a baseline naming no rule must not be quotable")
		}
		if strings.Contains(err.Error(), "the rule changed") {
			t.Errorf("an uncited baseline is not a rule change and must not be reported as one, got: %v", err)
		}
	})
}
