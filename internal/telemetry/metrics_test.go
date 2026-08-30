package telemetry

import (
	"context"
	"os"
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
// The table is the whole outcome vocabulary, so an outcome added to
// telemetry.Outcome without a counter mapping shows up here as an unasserted case
// rather than as silence.
func TestEveryOutcomeIncrementsItsCounters(t *testing.T) {
	const stage = "am.test.outcomes"

	cases := []struct {
		outcome Outcome
		want    map[string]int64
	}{
		// Start always counts eligible; End adds the outcome's own counters.
		{Ran, map[string]int64{CounterEligible: 1, CounterSelected: 1, CounterEffect: 1}},
		{Bypassed, map[string]int64{CounterEligible: 1, CounterFallback: 1}},
		{FailedOpen, map[string]int64{CounterEligible: 1, CounterSelected: 1, CounterFallback: 1}},
		{FailedClosed, map[string]int64{CounterEligible: 1, CounterSelected: 1}},
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
			for key, moved := range got {
				if key[0] != stage {
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

	got := delta(t, func() {
		_, sp := Start(context.Background(), stage)
		sp.End(Ran)
		sp.End(Ran)
		sp.End(FailedClosed) // a different outcome must not re-count either
	})

	if got[[2]string{stage, CounterSelected}] != 1 {
		t.Errorf("selected moved by %d across three End calls, want 1",
			got[[2]string{stage, CounterSelected}])
	}
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
