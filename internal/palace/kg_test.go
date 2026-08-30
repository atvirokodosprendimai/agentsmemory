package palace

import (
	"context"
	"reflect"
	"strings"
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
	q, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Direction: "both"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	f := findFact(q.Facts, "works_at", "Acme")
	if f == nil {
		t.Fatalf("expected Alice works_at Acme, got %+v", q.Facts)
	}
	if !f.Current || f.Direction != "outgoing" {
		t.Fatalf("fact should be current+outgoing: %+v", *f)
	}

	// Incoming from the object side resolves the subject name.
	in, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Acme", Direction: "incoming"})
	if err != nil {
		t.Fatalf("query incoming: %v", err)
	}
	if g := findFact(in.Facts, "works_at", "Acme"); g == nil || g.Subject != "Alice" {
		t.Fatalf("incoming should show Alice as subject, got %+v", in.Facts)
	}
}

func TestKGInvalidateAndAsOf(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, _, err := svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2025-06-01", "she left"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	// After invalidation the fact is historical, not current.
	res, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Direction: "both"})
	f := findFact(res.Facts, "works_at", "Acme")
	if f == nil || f.Current || f.ValidTo != "2025-06-01" {
		t.Fatalf("fact should be ended 2025-06-01: %+v", res.Facts)
	}

	// as_of mid-window: in effect.
	mid, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", AsOf: "2024-06-01", Direction: "both"})
	if findFact(mid.Facts, "works_at", "Acme") == nil {
		t.Fatal("fact should be in effect as of 2024-06-01")
	}
	// as_of after the end: not in effect.
	after, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", AsOf: "2025-12-01", Direction: "both"})
	if findFact(after.Facts, "works_at", "Acme") != nil {
		t.Fatal("fact should NOT be in effect as of 2025-12-01 (ended)")
	}
	// as_of before the start: not in effect.
	before, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", AsOf: "2023-01-01", Direction: "both"})
	if findFact(before.Facts, "works_at", "Acme") != nil {
		t.Fatal("fact should NOT be in effect as of 2023-01-01 (not yet started)")
	}

	// Supersede flow: after invalidation a new current fact can be added.
	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Globex", "2025-06-01", "", "", "", ""); err != nil {
		t.Fatalf("post-invalidate add: %v", err)
	}
	now, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", AsOf: "2025-12-01", Direction: "outgoing"})
	if findFact(now.Facts, "works_at", "Globex") == nil {
		t.Fatalf("the new current fact should be in effect: %+v", now.Facts)
	}
}

// TestEndedFactIsAbsentFromCurrentQuery is ADR-026 T1's gate.
//
// It asserts ABSENCE, which is the only assertion that can fail when the filter is
// removed. The pre-ADR-026 tool returned every fact ever recorded and tagged the
// dead ones current:false, so a test that merely queried and found its fact passed
// identically before and after — the behaviour under test was the reader's
// discipline, not the server's. Delete the status wiring and this goes red on the
// current branch; the `all` branch below is what stops it from passing by returning
// nothing at all.
func TestEndedFactIsAbsentFromCurrentQuery(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("add ended-to-be: %v", err)
	}
	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Globex", "2025-06-01", "", "", "", ""); err != nil {
		t.Fatalf("add survivor: %v", err)
	}
	if _, _, _, err := svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2025-06-01", "she left"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	current, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Status: KGStatusCurrent})
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if findFact(current.Facts, "works_at", "Acme") != nil {
		t.Fatalf("the retracted fact must not be returned at status=current: %+v", current.Facts)
	}
	if findFact(current.Facts, "works_at", "Globex") == nil {
		t.Fatalf("the open-ended fact must survive status=current: %+v", current.Facts)
	}

	ended, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Status: KGStatusEnded})
	if err != nil {
		t.Fatalf("ended: %v", err)
	}
	if findFact(ended.Facts, "works_at", "Acme") == nil {
		t.Fatalf("status=ended is the audit direction and must return the retracted fact: %+v", ended.Facts)
	}
	if findFact(ended.Facts, "works_at", "Globex") != nil {
		t.Fatalf("status=ended must not return open-ended facts: %+v", ended.Facts)
	}

	all, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Status: KGStatusAll})
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if findFact(all.Facts, "works_at", "Acme") == nil || findFact(all.Facts, "works_at", "Globex") == nil {
		t.Fatalf("status=all must return both: %+v", all.Facts)
	}

	// An omitted status is status=all until T4 flips it. Pinned so the flip is a
	// visible test change rather than a silent one.
	def, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice"})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(def.Facts) != len(all.Facts) {
		t.Fatalf("the service default must be %q: got %d facts, all returns %d", KGStatusAll, len(def.Facts), len(all.Facts))
	}
}

// TestPredicateIsAnEntryPointAndAFilter covers ADR-026 T5's behaviour: predicate
// alone answers "every fact of this relation" without naming an entity, and
// predicate WITH an entity narrows that entity's facts.
//
// Both halves matter. Only the entry point is new capability — the graph's own
// vocabulary was the one dimension nothing could select on, so auditing a relation
// meant reading the whole graph by eye — but the filter half is what makes it
// compose with the entity lookup instead of being a separate tool.
func TestPredicateIsAnEntryPointAndAFilter(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	for _, f := range []struct{ s, p, o string }{
		{"Alice", "works at", "Acme"},
		{"Alice", "knows", "Bob"},
		{"Carol", "works at", "Globex"},
	} {
		if _, err := svc.KGAdd(ctx, team, f.s, f.p, f.o, "2024-01-01", "", "", "", ""); err != nil {
			t.Fatalf("seed %v: %v", f, err)
		}
	}

	// Entry point: no entity at all. Reaches facts about entities the caller never
	// named, which is the capability that did not exist before.
	only, err := svc.KGQuery(ctx, team, KGQueryInput{Predicate: "works at"})
	if err != nil {
		t.Fatalf("predicate-only: %v", err)
	}
	if len(only.Facts) != 2 {
		t.Fatalf("predicate-only should reach both works_at facts, got %+v", only.Facts)
	}
	if findFact(only.Facts, "works_at", "Globex") == nil {
		t.Fatalf("predicate-only must reach an entity the caller never named: %+v", only.Facts)
	}
	for _, f := range only.Facts {
		if f.Direction != "" {
			t.Errorf("with no queried endpoint a fact cannot be incoming or outgoing, got %q", f.Direction)
		}
	}
	if only.Predicate != "works_at" {
		t.Errorf("the applied predicate must be echoed normalized, got %q", only.Predicate)
	}

	// Filter: entity AND predicate. Alice has two facts; one matches.
	both, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Predicate: "works at"})
	if err != nil {
		t.Fatalf("entity+predicate: %v", err)
	}
	if len(both.Facts) != 1 || findFact(both.Facts, "works_at", "Acme") == nil {
		t.Fatalf("entity+predicate should return only Alice's works_at fact, got %+v", both.Facts)
	}

	// Neither entry point is a table dump, not a query.
	if _, err := svc.KGQuery(ctx, team, KGQueryInput{}); err == nil {
		t.Fatal("a query with neither entity nor predicate must be rejected")
	}
}

// TestEveryStoredTripleColumnIsReturnedOrExcluded is ADR-026 T6's gate, and it is
// DERIVED rather than hand-listed on purpose.
//
// Walking kgTripleRow by reflection means a column added tomorrow enters this
// check in the commit that creates it, instead of waiting for someone to remember
// this file exists. A hand-written list of expected fields is a second thing to
// maintain and it fails in the silent direction: it keeps passing while the column
// it does not mention goes unreturned. That is precisely how extracted_at,
// source_drawer_id and source_file were written on every fact and surfaced by
// nothing, while source_closet sitting beside them was returned.
//
// The exclusion map has to carry a REASON, so withholding a column is a sentence
// someone had to write rather than an omission nobody noticed.
func TestEveryStoredTripleColumnIsReturnedOrExcluded(t *testing.T) {
	row := reflect.TypeOf(kgTripleRow{})
	fact := reflect.TypeOf(KGFact{})

	for i := 0; i < row.NumField(); i++ {
		name := row.Field(i).Name
		if reason, excluded := kgRowFieldsExcluded[name]; excluded {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s is excluded with an empty reason — an exclusion without one is an omission", name)
			}
			continue
		}
		surfaced := name
		if renamed, ok := kgRowFieldRenames[name]; ok {
			surfaced = renamed
		}
		if _, ok := fact.FieldByName(surfaced); !ok {
			t.Errorf("kg_triples.%s is stored on every fact and returned by nothing.\n"+
				"  Either surface it on KGFact (as %q), or name it in kgRowFieldsExcluded with the reason it is withheld.\n"+
				"  A column written and invisible is the defect ADR-026 T6 exists to close.",
				name, surfaced)
		}
	}

	// A rename that no longer points anywhere would silently excuse its column
	// from the check above, so the map itself is verified against both types.
	for from, to := range kgRowFieldRenames {
		if _, ok := row.FieldByName(from); !ok {
			t.Errorf("kgRowFieldRenames maps %q, which is no longer a kgTripleRow field", from)
		}
		if _, ok := fact.FieldByName(to); !ok {
			t.Errorf("kgRowFieldRenames points %q at %q, which is not a KGFact field", from, to)
		}
	}
	for name := range kgRowFieldsExcluded {
		if _, ok := row.FieldByName(name); !ok {
			t.Errorf("kgRowFieldsExcluded names %q, which is no longer a kgTripleRow field", name)
		}
	}
}

// TestProvenanceReachesTheCaller is the value half of T6: the reflection gate above
// proves a field EXISTS on KGFact, which a zero value satisfies perfectly.
func TestProvenanceReachesTheCaller(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// A REAL drawer, because provenance is now checked: this fixture used to cite
	// "drawer-abc", an id no row ever had, and passed — which is the corpus defect
	// in miniature. Citing a row that exists makes the round-trip it asserts mean
	// something.
	src, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "notes.md", Content: "Alice joined Acme in January"})
	if err != nil {
		t.Fatalf("seed the cited drawer: %v", err)
	}
	cited := src.Drawers[0].ID

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "",
		"closet-1", "notes.md", cited); err != nil {
		t.Fatalf("add: %v", err)
	}

	res, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Direction: "outgoing"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	f := findFact(res.Facts, "works_at", "Acme")
	if f == nil {
		t.Fatalf("expected the seeded fact, got %+v", res.Facts)
	}
	if f.SourceFile != "notes.md" {
		t.Errorf("source_file = %q, want %q", f.SourceFile, "notes.md")
	}
	if f.SourceDrawerID != cited {
		t.Errorf("source_drawer_id = %q, want %q — every fact knows which memory asserted it and no agent could ask",
			f.SourceDrawerID, cited)
	}
	if f.RecordedAt == "" {
		t.Error("recorded_at is empty; transaction time is what makes the graph bitemporal and it is written on every row")
	}
}

func TestKGStatsAndTimeline(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	_, _ = svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", "")
	_, _ = svc.KGAdd(ctx, team, "Bob", "knows", "Alice", "", "", "", "", "")
	_, _, _, _ = svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2025-06-01", "she left")

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
	if _, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "A", Direction: "sideways"}); err == nil {
		t.Fatal("invalid direction should be rejected")
	}
	// So is a status outside the vocabulary — a typo must not silently widen the
	// query back to every fact, which is the failure the default flip exists to end.
	if _, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "A", Status: "expired"}); err == nil {
		t.Fatal("invalid status should be rejected")
	}
}
