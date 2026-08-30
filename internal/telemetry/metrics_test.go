package telemetry

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"go.opentelemetry.io/otel"
)

// reader is the process-wide manual metric reader every assertion in this file
// collects from. It is installed by TestMain rather than per test because Inc
// resolves the am.feature counter from the GLOBAL meter provider exactly once,
// behind featureOnce — so whichever provider is installed when the first counter
// increment happens is the only one that will ever see any of them. A per-test
// provider would therefore work in whichever test ran first and silently record
// nothing in every test after it.
//
// That is not incidental: it is a large part of why these counters had no test.
// The obvious way to write one does not work, and it fails by reporting zero,
// which is indistinguishable from the counter not being incremented at all.
var reader *sdkmetric.ManualReader

func TestMain(m *testing.M) {
	reader = sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	os.Exit(m.Run())
}

// counters collects the am.feature counter and returns it keyed by
// feature name and counter name.
//
// Cumulative, so every assertion below works on a DELTA across an action rather
// than an absolute: other tests in this package end spans too, and a test that
// asserted an absolute would pass or fail depending on what ran before it.
func counters(t *testing.T) map[[2]string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	out := map[[2]string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "am.feature" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("am.feature is %T, want an int64 sum", m.Data)
			}
			for _, dp := range sum.DataPoints {
				feature, _ := dp.Attributes.Value("am.feature")
				counter, _ := dp.Attributes.Value("am.counter")
				out[[2]string{feature.AsString(), counter.AsString()}] += dp.Value
			}
		}
	}
	return out
}

// delta runs fn and returns how much each (feature, counter) pair moved.
func delta(t *testing.T, fn func()) map[[2]string]int64 {
	t.Helper()
	before := counters(t)
	fn()
	after := counters(t)
	out := map[[2]string]int64{}
	for k, v := range after {
		if d := v - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

// TestEveryOutcomeIncrementsItsCounters is the falsifiability ADR-025 criterion 7
// and T5 require and did not have. Before this file, CounterEligible,
// CounterSelected, CounterEffect and CounterFallback appeared in ZERO test files
// in the repository: deleting the entire counter block from Span.End turned
// nothing red, so the acceptance criterion "the runtime report distinguishes no
// eligible traffic from eligible-but-never-selected" was unmet by construction
// rather than by a bug.
//
// "Every outcome" is DERIVED from the production vocabulary by declaredOutcomes,
// not restated here. A hand-maintained second list is the defect this file exists
// to close, not a way to close it: an earlier draft carried one, and adding a
// fifth Outcome constant left this test green — the exact silence ADR-025
// criterion 7 is about. The table below is now checked against the declaration,
// so a new outcome fails here on the commit that adds it.
func TestEveryOutcomeIncrementsItsCounters(t *testing.T) {
	const stage = "am.test.outcomes"

	cases := []struct {
		outcome Outcome
		want    map[string]int64
		// unpinned names counters whose behaviour for this outcome is a decision
		// nobody has taken yet: neither required nor forbidden here. It is not a
		// TODO — it is the difference between a test that records a decision and
		// a test that manufactures one by asserting the current code.
		unpinned []string
	}{
		// Start always counts eligible; End adds the outcome's own counters.
		//
		// CounterEffect is unpinned on Ran. Span.End increments it today, but
		// issue #54 leaves "does running imply an effect on the served result?"
		// open for a human: under option 1 effect is declared separately, and a
		// test requiring End(Ran) to emit it would REJECT that fix. Pinning the
		// current collapse here would settle by assertion a question this PR
		// exists to keep open. TestEffectIsRecordableIndependently covers the
		// half that is decided — that the counter works at all.
		{outcome: Ran, want: map[string]int64{CounterEligible: 1, CounterSelected: 1}, unpinned: []string{CounterEffect}},
		{outcome: Bypassed, want: map[string]int64{CounterEligible: 1, CounterFallback: 1}},
		{outcome: FailedOpen, want: map[string]int64{CounterEligible: 1, CounterSelected: 1, CounterFallback: 1}},
		{outcome: FailedClosed, want: map[string]int64{CounterEligible: 1, CounterSelected: 1}},
	}

	mapped := map[string]bool{}
	for _, tc := range cases {
		mapped[string(tc.outcome)] = true
	}
	for _, declared := range declaredOutcomes(t) {
		if !mapped[declared] {
			t.Errorf("outcome %q is declared in the telemetry package and has no case here, so "+
				"nothing says which counters it moves. An outcome that increments nothing is "+
				"invisible to the unsampled report — add its row rather than deleting this check.",
				declared)
		}
	}

	for _, tc := range cases {
		t.Run(string(tc.outcome), func(t *testing.T) {
			got := delta(t, func() {
				_, sp := Start(context.Background(), stage)
				sp.End(tc.outcome)
			})
			for counter, want := range tc.want {
				if got[[2]string{stage, counter}] != want {
					t.Errorf("outcome %s: counter %q moved by %d, want %d — a stage outcome that "+
						"increments nothing is invisible to the unsampled report, which is the only "+
						"signal that survives sampling",
						tc.outcome, counter, got[[2]string{stage, counter}], want)
				}
			}
			// Nothing beyond the expected set moved: an outcome that also bumped a
			// counter it does not mean would make "eligible but never selected"
			// unreadable, which is the exact question ADR-025 asks these to answer.
			unpinned := map[string]bool{}
			for _, c := range tc.unpinned {
				unpinned[c] = true
			}
			for key, moved := range got {
				if key[0] != stage || unpinned[key[1]] {
					continue
				}
				if _, expected := tc.want[key[1]]; !expected {
					t.Errorf("outcome %s also incremented %q by %d, which nothing expects",
						tc.outcome, key[1], moved)
				}
			}
		})
	}
}

// TestStartCountsEligibleBeforeAnyOutcome pins the half that makes "unused"
// answerable. ADR-025 defines unused as eligible > 0 with selected and effect at
// 0 over a window — so eligible has to be counted when the stage is ENTERED, not
// when it finishes. A stage that panics, or one whose span is never ended, must
// still show as eligible or the window cannot tell "never reached" from "reached
// and skipped".
func TestStartCountsEligibleBeforeAnyOutcome(t *testing.T) {
	const stage = "am.test.eligible-only"

	got := delta(t, func() {
		_, _ = Start(context.Background(), stage) // deliberately never ended
	})

	if got[[2]string{stage, CounterEligible}] != 1 {
		t.Errorf("eligible moved by %d on Start, want 1 — counting it at End instead would make an "+
			"unfinished stage indistinguishable from one that was never in force",
			got[[2]string{stage, CounterEligible}])
	}
	for _, c := range []string{CounterSelected, CounterEffect, CounterFallback} {
		if moved := got[[2]string{stage, c}]; moved != 0 {
			t.Errorf("%q moved by %d before any outcome was recorded", c, moved)
		}
	}
}

// TestASecondEndCountsOnce. End is idempotent so a defer can always fire beside
// an explicit call, and this is the half of that promise the counters care about:
// double-counting selected would inflate exactly the denominator an operator
// reads the unsampled report for.
func TestASecondEndCountsOnce(t *testing.T) {
	const stage = "am.test.double-end"

	first := delta(t, func() {
		_, sp := Start(context.Background(), stage)
		sp.End(Ran)
	})
	repeat := delta(t, func() {
		_, sp := Start(context.Background(), stage)
		sp.End(Ran)
		sp.End(Ran)
		sp.End(FailedClosed) // a different outcome must not re-count either
	})

	// The COMPLETE delta, not just selected. Idempotence is a promise about the
	// whole counter block inside sync.Once, so an increment accidentally moved
	// outside it would be caught wherever it sits — including CounterEffect,
	// whose per-outcome semantics this file deliberately leaves unpinned. That
	// is exactly why the comparison is against the single-End delta rather than
	// against a literal: it holds whichever way issue #54 is decided.
	for key, moved := range repeat {
		if key[0] != stage {
			continue
		}
		if moved != first[key] {
			t.Errorf("%q moved by %d across three End calls but by %d across one — End is meant to "+
				"be idempotent so a defer can fire beside an explicit call, and double-counting "+
				"inflates the denominator an operator reads the unsampled report for",
				key[1], moved, first[key])
		}
	}
	for key, moved := range first {
		if key[0] == stage && repeat[key] == 0 {
			t.Errorf("%q moved by %d on a single End and not at all across three", key[1], moved)
		}
	}
}

// TestEffectIsRecordableIndependently pins the half of CounterEffect that IS
// decided: the counter exists, is reachable, and lands on its own series.
//
// It deliberately does not go through Span.End. ADR-025 defines "unused" as
// eligible > 0 with selected and effect at 0, so effect has to be recordable by
// a stage that knows it changed the served result — which is a different fact
// from the stage having run. Issue #54 is open on whether End(Ran) should imply
// it; whichever way that goes, this assertion stands.
func TestEffectIsRecordableIndependently(t *testing.T) {
	const stage = "am.test.effect-alone"

	got := delta(t, func() {
		Inc(context.Background(), stage, CounterEffect)
	})

	if got[[2]string{stage, CounterEffect}] != 1 {
		t.Errorf("effect moved by %d when incremented directly, want 1 — if this counter is not "+
			"recordable on its own then ADR-025's \"eligible but no effect\" window cannot be "+
			"answered by any caller",
			got[[2]string{stage, CounterEffect}])
	}
	for _, c := range []string{CounterEligible, CounterSelected, CounterFallback} {
		if moved := got[[2]string{stage, c}]; moved != 0 {
			t.Errorf("%q moved by %d while only effect was incremented", c, moved)
		}
	}
}

// declaredOutcomes returns every Outcome constant's wire value, read from this
// package's non-test sources.
//
// Derived rather than listed because a hand-kept copy of a vocabulary is the
// failure this whole file is a gate against: the universe has to come from the
// declaration, so a constant added tomorrow joins the check on the same commit.
// Same shape as the repo's other house gates, which derive their universes from
// source for the same reason (AGENTS.md, "Reachability").
func declaredOutcomes(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse telemetry package: %v", err)
	}

	var out []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				// A const block carries its type down from the first spec that
				// names one, so track it rather than reading each spec alone.
				var typeName string
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if id, ok := vs.Type.(*ast.Ident); ok {
						typeName = id.Name
					}
					if typeName != "Outcome" {
						continue
					}
					for _, v := range vs.Values {
						lit, ok := v.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						value, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatalf("unquote %s: %v", lit.Value, err)
						}
						out = append(out, value)
					}
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no Outcome constants found — this check has stopped checking anything")
	}
	return out
}

// TestIncIgnoresEmptyNames: an unnamed feature or counter would land as an empty
// attribute value and quietly become its own series, which is worse than being
// dropped — it looks like data.
func TestIncIgnoresEmptyNames(t *testing.T) {
	got := delta(t, func() {
		Inc(context.Background(), "", CounterSelected)
		Inc(context.Background(), "am.test.empty", "")
	})

	for key, moved := range got {
		if key[0] == "" || key[1] == "" {
			t.Errorf("an empty name was recorded as (%q, %q) += %d", key[0], key[1], moved)
		}
	}
}

// TestANilSpanIsSafeAndCountsNothing. Span is nil-safe so a forgotten Start
// cannot panic the served path; that guarantee is only worth having if the nil
// path also records nothing, since a nil span has no feature name to key on.
func TestANilSpanIsSafeAndCountsNothing(t *testing.T) {
	got := delta(t, func() {
		var sp *Span
		sp.Set()
		sp.End(Ran)
	})

	if len(got) != 0 {
		t.Errorf("a nil span moved counters: %v", got)
	}
}
