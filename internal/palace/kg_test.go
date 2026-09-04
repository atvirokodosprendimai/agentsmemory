package palace

import (
	"context"
	"fmt"
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

// TestAnOversizedNodeWarnsAndStillWrites is ADR-053 T3's gate.
//
// The ~35-leaf guidance the skills teach is a convention nothing enforced, and a
// convention nothing enforces goes stale silently. This makes it visible at the
// moment it is broken, to the only party who can act on it.
//
// ⚠ IT ASSERTS BOTH HALVES ON PURPOSE. A warning that accompanied a REFUSAL
// would satisfy a warning-only check, and refusing is exactly what the owner
// rejected on 2026-09-04: the write is the moment an agent holds the knowledge,
// and refusing there loses a fact to save a shape.
func TestAnOversizedNodeWarnsAndStillWrites(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	teamID := "team-fanout"
	ctx := context.Background()

	var last KGAddResult
	for i := range KGFanOutLimit + 1 {
		var err error
		last, err = svc.KGAdd(ctx, teamID, "wide", fmt.Sprintf("leaf-%03d", i), "target", "", "", "", "", "")
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}

	if last.FanOut <= KGFanOutLimit {
		t.Errorf("FanOut = %d after %d edges; the warning reports the node the caller has just "+
			"created, so it must count AFTER the write", last.FanOut, KGFanOutLimit+1)
	}
	if last.FanOutAdvice == "" {
		t.Errorf("no advice on a node past the limit — a count with no remedy beside it is a number " +
			"the reader has to look up what to do about")
	}

	// The write landed. This is the half a warning-only assertion would miss.
	res, err := svc.KGQuery(ctx, teamID, KGQueryInput{Entity: "wide", Limit: MaxKGQueryLimit})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(res.Facts) != KGFanOutLimit+1 {
		t.Errorf("the node holds %d facts; want %d — a warning that dropped the write would be a "+
			"refusal wearing a warning's clothes", len(res.Facts), KGFanOutLimit+1)
	}
}

// TestANodeUnderTheLimitWarnsAboutNothing keeps the field's PRESENCE as the
// signal. A warning that arrives on every write is one every caller learns to
// skip, and then it warns about nothing.
func TestANodeUnderTheLimitWarnsAboutNothing(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	res, err := svc.KGAdd(context.Background(), "team-narrow", "narrow", "one", "target", "", "", "", "", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.FanOutAdvice != "" {
		t.Errorf("advice on a one-edge node: %q", res.FanOutAdvice)
	}
}

// TestEveryWingRootStillResolvesWithContainmentHidden is ADR-053 T2's first
// gate, and it is written before the rule it protects on purpose.
//
// ⚠ THE WRONG RULE LOOKS MORE PRINCIPLED. "Hide what the server derived" reads
// better than "hide what has a room: subject", and it empties three of six wing
// roots in the live corpus — the address start-here tells every session to walk
// FIRST — because the root's own `holds` edge to its entry room is derived too.
// Measured 2026-09-04: 580 of 586 derived edges are room listings; the other 6
// are that spine.
func TestEveryWingRootStillResolvesWithContainmentHidden(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	const team = "team-root"
	ctx := context.Background()

	// Mint the root through the shipped path rather than by hand, so this holds
	// on a fresh install rather than only against a corpus someone prepared.
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: "wing_acme", Room: EntryRoom,
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The tier below.",
	}); err != nil {
		t.Fatalf("seed the entry room: %v", err)
	}

	res, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "wing_acme.root", Direction: "outgoing"})
	if err != nil {
		t.Fatalf("query the root: %v", err)
	}
	if len(res.Facts) == 0 {
		t.Fatalf("the wing root resolved to nothing with containment hidden — this is the front "+
			"door every session walks first, and %q is a rule about room listings, not about the "+
			"root's own edge to its entry room", "isContainmentEdge")
	}
}

// TestAnAbsentEntryPointStillResolvesUnknown is T2's second gate, and it cannot
// be left to the first.
//
// ⚠ HIDING ROWS WITHOUT HIDING THE ENTITY TURNS "ABSENT" INTO "PRESENT AND
// EMPTY", and those are opposites in what a caller does next. "No entry point"
// is recoverable — a session reads it and walks the fallback chain. "Empty entry
// point" is an answer, and a session acts on it. resolveKGTerms decides
// unknown_term on whether the entity NAME exists, never on whether rows came
// back, and attachDerivedEdge upserts a kg_entities row for the room: subject —
// so the node survives any filter on its edges. The two failures are identical
// from a count, which is why assertion one cannot cover this.
func TestAnAbsentEntryPointStillResolvesUnknown(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	res, err := svc.EntryPoint(context.Background(), "team-absent", "wing_no_such_place")
	if err != nil {
		t.Fatalf("entry point: %v", err)
	}
	if res.Resolution != KGResolutionUnknownTerm {
		t.Errorf("resolution = %q; want %q. A wing with no entry point must stay distinguishable "+
			"from one whose entry point is empty, or a session stops instead of falling back",
			res.Resolution, KGResolutionUnknownTerm)
	}
}

// TestContainmentIsHiddenAndCounted covers the rule itself: hidden by default,
// reported when hidden, and restored exactly by the flag.
func TestContainmentIsHiddenAndCounted(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	const team = "team-containment"
	ctx := context.Background()
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: "wing_acme", Room: "decisions", Content: "a decision worth remembering",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	room := DerivedEdgeSubject("wing_acme", "decisions")

	// The predicate sweep is the entry point that hurts: 587 edges on the live
	// corpus, 586 of them containment.
	hidden, err := svc.KGQuery(ctx, team, KGQueryInput{Predicate: DerivedEdgePredicate})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(hidden.Facts) != 0 {
		t.Errorf("a bare %q sweep returned %d containment edge(s) by default", DerivedEdgePredicate, len(hidden.Facts))
	}
	if hidden.Withheld[KGWithheldContainment] == 0 {
		t.Errorf("nothing reported under %q — a filtered page that does not say so presents itself "+
			"as the whole answer", KGWithheldContainment)
	}

	shown, err := svc.KGQuery(ctx, team, KGQueryInput{Predicate: DerivedEdgePredicate, IncludeContainment: true})
	if err != nil {
		t.Fatalf("sweep with the flag: %v", err)
	}
	if int64(len(shown.Facts)) != hidden.Withheld[KGWithheldContainment] {
		t.Errorf("the flag returned %d edge(s); %d were reported hidden. A flag that restores a "+
			"different set is not a restoration", len(shown.Facts), hidden.Withheld[KGWithheldContainment])
	}

	// ⚠ Asking a ROOM what it holds is the one question containment edges answer,
	// and EntryPoint asks exactly it. The carve-out is the whole reason the rule
	// keys on the queried entity as well as on the subject.
	asked, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: room, Direction: "outgoing"})
	if err != nil {
		t.Fatalf("ask the room: %v", err)
	}
	if len(asked.Facts) == 0 {
		t.Error("asking a room what it holds returned nothing — the caller named the room, so the " +
			"listing IS the answer they asked for")
	}
}
