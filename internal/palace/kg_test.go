package palace

import (
	"context"
	"testing"
)

// findFact returns the first fact matching predicate+object, or nil.
func findFact(facts []KGFact, predicate, object string) *KGFact {
	for i := range facts {
		if facts[i].Predicate == predicate && facts[i].Object == object {
			return &facts[i]
		}
	}
	return nil
}

func TestKGAddQueryDedup(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	res, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.TripleID == "" {
		t.Fatal("expected a triple id")
	}

	// Dedup: re-adding the identical current fact returns the same id, no duplicate.
	res2, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", "")
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if res2.TripleID != res.TripleID {
		t.Fatalf("dedup should return the existing id: %s vs %s", res2.TripleID, res.TripleID)
	}

	// Query outgoing: predicate is normalized to works_at; current is true.
	facts, _, err := svc.KGQuery(ctx, team, "Alice", "", "both")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	f := findFact(facts, "works_at", "Acme")
	if f == nil {
		t.Fatalf("expected Alice works_at Acme, got %+v", facts)
	}
	if !f.Current || f.Direction != "outgoing" {
		t.Fatalf("fact should be current+outgoing: %+v", *f)
	}

	// Incoming from the object side resolves the subject name.
	in, _, err := svc.KGQuery(ctx, team, "Acme", "", "incoming")
	if err != nil {
		t.Fatalf("query incoming: %v", err)
	}
	if g := findFact(in, "works_at", "Acme"); g == nil || g.Subject != "Alice" {
		t.Fatalf("incoming should show Alice as subject, got %+v", in)
	}
}

func TestKGInvalidateAndAsOf(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, err := svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2025-06-01"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	// After invalidation the fact is historical, not current.
	facts, _, _ := svc.KGQuery(ctx, team, "Alice", "", "both")
	f := findFact(facts, "works_at", "Acme")
	if f == nil || f.Current || f.ValidTo != "2025-06-01" {
		t.Fatalf("fact should be ended 2025-06-01: %+v", facts)
	}

	// as_of mid-window: in effect.
	mid, _, _ := svc.KGQuery(ctx, team, "Alice", "2024-06-01", "both")
	if findFact(mid, "works_at", "Acme") == nil {
		t.Fatal("fact should be in effect as of 2024-06-01")
	}
	// as_of after the end: not in effect.
	after, _, _ := svc.KGQuery(ctx, team, "Alice", "2025-12-01", "both")
	if findFact(after, "works_at", "Acme") != nil {
		t.Fatal("fact should NOT be in effect as of 2025-12-01 (ended)")
	}
	// as_of before the start: not in effect.
	before, _, _ := svc.KGQuery(ctx, team, "Alice", "2023-01-01", "both")
	if findFact(before, "works_at", "Acme") != nil {
		t.Fatal("fact should NOT be in effect as of 2023-01-01 (not yet started)")
	}

	// Supersede flow: after invalidation a new current fact can be added.
	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Globex", "2025-06-01", "", "", "", ""); err != nil {
		t.Fatalf("post-invalidate add: %v", err)
	}
	now, _, _ := svc.KGQuery(ctx, team, "Alice", "2025-12-01", "outgoing")
	if findFact(now, "works_at", "Globex") == nil {
		t.Fatalf("the new current fact should be in effect: %+v", now)
	}
}

func TestKGStatsAndTimeline(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	_, _ = svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", "")
	_, _ = svc.KGAdd(ctx, team, "Bob", "knows", "Alice", "", "", "", "", "")
	_, _, _ = svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2025-06-01")

	stats, err := svc.KGStats(ctx, team)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Triples != 2 || stats.CurrentFacts != 1 || stats.ExpiredFacts != 1 {
		t.Fatalf("stats wrong: %+v", stats)
	}
	if stats.Entities != 3 { // Alice, Acme, Bob
		t.Fatalf("expected 3 entities, got %d", stats.Entities)
	}

	tl, label, err := svc.KGTimeline(ctx, team, "Alice")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if label != "Alice" || len(tl) < 2 {
		t.Fatalf("timeline for Alice should include both facts, got %d (%s)", len(tl), label)
	}
}

func TestKGValidation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// Inverted interval is rejected.
	if _, err := svc.KGAdd(ctx, team, "A", "rel", "B", "2025-01-01", "2024-01-01", "", "", ""); err == nil {
		t.Fatal("inverted validity interval should be rejected")
	}
	// Malformed date is rejected.
	if _, err := svc.KGAdd(ctx, team, "A", "rel", "B", "2024-13-40", "", "", "", ""); err == nil {
		t.Fatal("malformed date should be rejected")
	}
	// Empty subject is rejected.
	if _, err := svc.KGAdd(ctx, team, "  ", "rel", "B", "", "", "", "", ""); err == nil {
		t.Fatal("empty subject should be rejected")
	}
	// Bad direction is rejected.
	if _, _, err := svc.KGQuery(ctx, team, "A", "", "sideways"); err == nil {
		t.Fatal("invalid direction should be rejected")
	}
}
