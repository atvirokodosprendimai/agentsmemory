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
	"fmt"
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
	// QuoteRate, not a message stitched around Resolve. The two refusals are
	// deliberately different sentences — "nobody bound this" and "the rule moved
	// out from under it" — and the earlier version appended "compares against a
	// rule that is not the one on disk" to BOTH, which is false for the first:
	// nothing was compared. Reported in review 2026-08-29. Reusing QuoteRate also
	// means the gate and the quoting path cannot drift into two answers.
	usable := 0
	for _, b := range found {
		if err := b.QuoteRate(current); err != nil {
			t.Errorf("%v", err)
			continue
		}
		if b.Usable() {
			usable++
		}
	}
	t.Logf("%d baseline(s) checked against rule %s; %d usable", len(found), current[:12], usable)

	// A BASELINE THAT SAYS IT IS NOT ONE MUST NOT COUNT AS ONE.
	//
	// The version of this gate that shipped counted files, so the degenerate
	// baseline ADR-044 T1 recorded — which says in its own prose "must not be read
	// as satisfying F-5" — satisfied F-5. A test asserting that X is available,
	// passing when X is absent, is the defect this repository is named for in its
	// own AGENTS.md, and it had reached the gate that polices it.
	//
	// Shipping anyway is allowed and is not silent: it requires a written entry
	// naming the record that decided it.
	if usable == 0 {
		requireWrittenOverride(t, found, shippedWithoutUsableBaseline)
	}

	// A baseline in a SUBDIRECTORY is found. The shipped version globbed
	// "*.md", which matches siblings only, so a baseline filed one directory
	// down left the gate GREEN — against this function's own promise that a
	// baseline added tomorrow joins the check without anyone registering it, and
	// silently, which is the class this package exists to catch. Reported in
	// review 2026-08-29. A README is skipped in the same walk, because a
	// directory like this acquires one and reddening CI over it teaches people
	// the gate is noise.
	t.Run("a nested baseline joins the check and a README does not", func(t *testing.T) {
		dir := writeFixtureCorpus(t, "rule-sha256: "+strings.Repeat("a", 64)+"\nbaseline: usable\n")
		nested := filepath.Join(dir, BaselinesDir, "2026")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("fixture subdirectory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(nested, "deeper.md"),
			[]byte("# a baseline nobody bound\n"), 0o644); err != nil {
			t.Fatalf("write nested baseline: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, BaselinesDir, "README.md"),
			[]byte("# what this directory holds\n"), 0o644); err != nil {
			t.Fatalf("write README: %v", err)
		}
		got, err := Baselines(dir)
		if err != nil {
			t.Fatalf("baselines: %v", err)
		}
		var paths []string
		for _, b := range got {
			paths = append(paths, b.Path)
		}
		if len(got) != 2 {
			t.Fatalf("found %d baseline(s) %v, want 2 — the nested one must be checked and the "+
				"README must not be", len(got), paths)
		}
		for _, b := range got {
			if strings.HasSuffix(b.Path, "README.md") {
				t.Errorf("a README was treated as a baseline (%s), which reds CI over a file "+
					"any directory acquires", b.Path)
			}
		}
	})

	// The falsifiability half, INSIDE the fence and driving the SAME function.
	//
	// A corpus with a written override cannot exercise the branch that reports a
	// missing one, so the branch is driven over inputs that ARE wrong. The verdict
	// travels through a substitutable testing.TB because a test cannot pin its own
	// reporting: without the shim, severing the check left the gate green while it
	// printed that everything was in order — which this package has shipped before.
	t.Run("only a baseline that says usable is usable", func(t *testing.T) {
		// Drives Usable() itself, and it exists because the first version of this
		// subtest did not. It built a Baseline with Verdict "degenerate" and called
		// requireWrittenOverride directly, so the PREDICATE was never exercised: a
		// mutant reading `Verdict != "unusable"` made the corpus's degenerate file
		// count as usable and the whole fence still passed. adr-verify refused to
		// score it, which is the only reason it was noticed.
		//
		// "" is not usable, and that is the direction to be wrong in: an unmarked
		// baseline is one nobody has judged.
		for _, tc := range []struct {
			verdict string
			want    bool
		}{
			{"usable", true},
			{"degenerate", false},
			{"", false},
			{"unusable", false},
			{"USABLE", false},
		} {
			if got := (Baseline{Verdict: tc.verdict}).Usable(); got != tc.want {
				t.Errorf("Baseline{Verdict: %q}.Usable() = %v, want %v — a predicate that admits "+
					"anything but one spelling lets a file declaring itself degenerate satisfy "+
					"the requirement it disclaims", tc.verdict, got, tc.want)
			}
		}
	})

	t.Run("shipping with no usable baseline and no override is caught", func(t *testing.T) {
		degenerate := []Baseline{{Path: "docs/measurement/baselines/x.md", Verdict: "degenerate"}}
		var spy recorder
		requireWrittenOverride(&spy, degenerate, map[string]string{})
		if !spy.failed {
			t.Error("a corpus whose every baseline declares itself degenerate, with no record " +
				"taking responsibility, was reported as satisfying F-5 — which is the exact " +
				"shape review found in the shipped gate")
		}
		// The fixture keys are deliberately NOT record-shaped. A key that looks like
		// a record number is a citation to a record that does not exist, and
		// TestEveryCitedADRResolves catches it — which it did, on the first run of
		// this subtest, and again on the comment that explained the first catch by
		// spelling the number out. Describe it; do not write it.
		var blank recorder
		requireWrittenOverride(&blank, degenerate, map[string]string{"a-fixture-record": "   "})
		if !blank.failed {
			t.Error("an override whose reason is whitespace counted as written — the list would " +
				"then be the silent exemption it exists to replace")
		}
		var ok recorder
		requireWrittenOverride(&ok, degenerate, map[string]string{"a-fixture-record": "a stated reason"})
		if ok.failed {
			t.Error("a written override was refused, so the gate blocks the decision it is " +
				"supposed to record")
		}
	})

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

// requireWrittenOverride fails unless some record has taken responsibility, in
// writing, for shipping mechanism work with no usable baseline.
//
// Split out because the falsifiability subtests drive THIS function rather than a
// copy of it. A falsifiability half that reimplements the check pins nothing:
// severing the real one would leave it green, which this package has already
// shipped once.
func requireWrittenOverride(tb testing.TB, found []Baseline, overrides map[string]string) {
	tb.Helper()
	for record, reason := range overrides {
		if strings.TrimSpace(reason) != "" {
			tb.Logf("no usable baseline; shipping under a written override from %s", record)
			return
		}
	}
	var says []string
	for _, b := range found {
		says = append(says, fmt.Sprintf("%s says %q", b.Path, b.Verdict))
	}
	tb.Errorf("%d baseline file(s) and NONE usable (%s) — F-5 forbids shipping a mechanism against "+
		"a baseline that cannot move, and a file that declares itself degenerate is not a baseline "+
		"however well it is written. Either record one that is usable, or add an entry to "+
		"shippedWithoutUsableBaseline naming the record that decided to ship anyway and why",
		len(found), strings.Join(says, "; "))
}

// TestF5AnOverrideNamesItsReason refuses an override with no written reason, so
// the list cannot become the dodge it exists to prevent.
func TestF5AnOverrideNamesItsReason(t *testing.T) {
	for record, reason := range shippedWithoutUsableBaseline {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s overrides F-5 with no reason — the reason IS the review, and an entry "+
				"without one is a silent exemption wearing a written one's clothes", record)
		}
	}
}

// recorder is a testing.TB that remembers whether it was failed, so a
// falsifiability check can drive a gate and inspect its VERDICT rather than
// inheriting it.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Errorf(string, ...any) { r.failed = true }
func (r *recorder) Error(...any)          { r.failed = true }
func (r *recorder) Fatalf(string, ...any) { r.failed = true }
func (r *recorder) Helper()               {}
func (r *recorder) Logf(string, ...any)   {}
