package palace

import (
	"context"
	"testing"
	"time"
)

// TestARetractionStampsAnInstantNotADate pins the write half of issue #47.
//
// KGInvalidate's `ended` is optional and defaulted to time.Now().Format
// ("2006-01-02") — a BARE DATE. temporalEndKey promotes a bare date to the end of
// its day, so inEffectAt kept the retracted fact in effect until midnight while
// status:"current" dropped it the moment the row was written. Two filters, two
// answers, one day, and ADR-026 had just told callers the two COMPOSE — which
// invites exactly the query that exposes the disagreement.
//
// It was the DEFAULT path, not an edge case: every retraction made through
// am_kg_invalidate without an explicit `ended` landed date-only.
//
// The assertion is in two halves on purpose. The first reads the mechanism — a
// resolved `ended` that is date-only is the defect, whatever any query says. The
// second reads the CONSEQUENCE through the real inEffectAt, because a format
// assertion alone would still pass if temporalEndKey later learned to stretch
// something else; the reachability rule in AGENTS.md is that a test for "X is now
// available" must fail when X is removed, and reverting the format alone turns
// both halves red.
func TestARetractionStampsAnInstantNotADate(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-kg-instant"

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The empty `ended` IS the case under test — an explicit one never took the
	// defaulting branch, which is why every pre-existing KGInvalidate test passed
	// while the default path shipped the defect.
	n, _, ended, err := svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "", "she left")
	if err != nil {
		t.Fatalf("KGInvalidate with no explicit ended: %v", err)
	}
	if n != 1 {
		t.Fatalf("ended %d facts; want 1 — the rest of this test reads a retraction that did not happen", n)
	}

	if isDateOnly(ended) {
		t.Fatalf("KGInvalidate defaulted ended to %q, a bare date.\n"+
			"  temporalEndKey promotes a bare date to T23:59:59Z, so this fact stays visible to\n"+
			"  as_of for the rest of the day while status:%q excludes it immediately — issue #47.\n"+
			"  Default to time.RFC3339, as KGSupersede already does.", ended, KGStatusCurrent)
	}
	if _, err := time.Parse(time.RFC3339, ended); err != nil {
		t.Fatalf("ended = %q, which is neither a date nor an RFC3339 instant: %v", ended, err)
	}

	// End of the day the retraction happened on. Under the defect this is EXACTLY
	// what temporalEndKey produced for the stored bare date, and inEffectAt excludes
	// only on a strict `<`, so the fact was still in effect here. Under the fix the
	// stored instant is earlier in the day and the fact is gone.
	endOfDay := ended[:len("2006-01-02")] + "T23:59:59Z"
	if ended == endOfDay {
		// One second per day where the retraction instant IS the boundary and the
		// two readings genuinely coincide. Skipping is honest; asserting would be
		// asserting something this input cannot show.
		t.Skip("retraction landed exactly at 23:59:59Z, where the instant and the end-of-day key are the same value")
	}

	res, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: "Alice",
		AsOf:   endOfDay,
		Status: KGStatusAll,
	})
	if err != nil {
		t.Fatalf("KGQuery as_of %s: %v", endOfDay, err)
	}
	for _, fact := range res.Facts {
		if fact.Object == "Acme" {
			t.Errorf("as_of %s still returns the fact retracted at %s, while status:%q excludes it.\n"+
				"  That is the day-scale disagreement issue #47 reports: the two filters answer\n"+
				"  the same question about the same instant two different ways.", endOfDay, ended, KGStatusCurrent)
		}
	}
}

// TestADateOnlyEndStillStretchesToEndOfDay pins the RESIDUAL that the tool
// descriptions and bootstrap-memory.md §6.1 now promise, and it is the reason
// those sentences are a caveat rather than a fix.
//
// temporalEndKey is deliberately unchanged by issue #47. Narrowing it would
// silently re-read every already-ended row in every palace — the inclusive
// reading is what those rows were written under — so the fix went to the write
// path and the read path kept its meaning. A caller may still pass a date-only
// `ended`, and rows stored before the change all carry one.
//
// Without this test the documented caveat is unpinned in both directions: someone
// could narrow temporalEndKey and leave four surfaces describing a lag that no
// longer exists, or widen it and leave them understating one.
func TestADateOnlyEndStillStretchesToEndOfDay(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-kg-dateonly"

	if _, err := svc.KGAdd(ctx, team, "Bob", "works at", "Initech", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const ended = "2026-08-30"
	if _, _, _, err := svc.KGInvalidate(ctx, team, "Bob", "works at", "Initech", ended, "he left"); err != nil {
		t.Fatalf("KGInvalidate: %v", err)
	}

	// Later that same day. status:"current" excludes this fact from the instant it
	// was retracted; as_of keeps it, because the stored value is a date.
	res, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: "Bob",
		AsOf:   ended + "T18:00:00Z",
		Status: KGStatusAll,
	})
	if err != nil {
		t.Fatalf("KGQuery: %v", err)
	}
	var found bool
	for _, fact := range res.Facts {
		if fact.Object == "Initech" {
			found = true
		}
	}
	if !found {
		t.Errorf("a fact ended on the bare date %s was already excluded at %sT18:00:00Z.\n"+
			"  temporalEndKey's end-of-day promotion appears to have been narrowed. That is a\n"+
			"  defensible decision, but it re-reads every already-ended row, so it needs a\n"+
			"  decision record — and it makes the as_of / ended descriptions and\n"+
			"  internal/web/bootstrap-memory.md §6.1 overstate a lag that no longer exists.\n"+
			"  Update all four surfaces with it.", ended, ended)
	}
}
