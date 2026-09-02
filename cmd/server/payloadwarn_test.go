package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// stubSampler answers SamplePayloadCoverage from a table, and records which
// namespaces were sampled at all — the second half being the point: a namespace
// this check must not warn about is one it must not even ask about, since the
// question costs an HTTP round trip against a collection whose answer is known.
type stubSampler struct {
	coverage map[string][2]int // namespace -> {withKeys, sampled}
	asked    []string
}

func (s *stubSampler) SamplePayloadCoverage(_ context.Context, namespace string, _ []string, _ int) (int, int, error) {
	s.asked = append(s.asked, namespace)
	c, ok := s.coverage[namespace]
	if !ok {
		return 0, 0, fmt.Errorf("unexpected namespace %q", namespace)
	}
	return c[0], c[1], nil
}

// collect runs the real loop and returns the warnings it emitted.
func collect(t *testing.T, sampler *stubSampler, namespaces []string) []string {
	t.Helper()
	var warnings []string
	reportPayloadGaps(context.Background(), sampler, namespaces, func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})
	return warnings
}

// TestTheBootCheckIgnoresTheEntityNamespace pins the fix for issue #164: the KG
// entity collection carries a label and no wing/room BY DESIGN, and entityMatches
// searches it with a nil filter, so a warning that scoped search is broken for
// those points is false in a way an operator cannot act on — and its advice names
// a repair driven by drawer rows the namespace does not have.
//
// Removing the palace.IsEntityNamespace skip in reportPayloadGaps must turn this
// red. It asserts the loop's behaviour rather than the predicate's, because the
// predicate can be perfect while nothing consults it.
func TestTheBootCheckIgnoresTheEntityNamespace(t *testing.T) {
	const team = "af063b4c-e118-4faa-9507-32de9fdad5ed"
	entities := team + "::kg_entities"
	sampler := &stubSampler{coverage: map[string][2]int{
		team:     {100, 100}, // drawers: fully labelled, the healthy case
		entities: {0, 100},   // entities: no wing/room, exactly as designed
	}}

	warnings := collect(t, sampler, []string{team, entities})

	if len(warnings) != 0 {
		t.Fatalf("a healthy palace warned about its own entity index: %v", warnings)
	}
	for _, ns := range sampler.asked {
		if palace.IsEntityNamespace(ns) {
			t.Errorf("sampled the entity namespace %q: the answer is known without asking, "+
				"and asking costs a scroll against the collection at every boot", ns)
		}
	}
}

// TestTheBootCheckStillWarnsAboutAnUnlabelledDrawerNamespace is the other half,
// and it is not optional: a skip written slightly too wide silences the real
// finding this check exists for, and a test asserting only "nothing warned" would
// pass just as happily with the whole check deleted.
func TestTheBootCheckStillWarnsAboutAnUnlabelledDrawerNamespace(t *testing.T) {
	const team = "af063b4c-e118-4faa-9507-32de9fdad5ed"
	entities := team + "::kg_entities"
	sampler := &stubSampler{coverage: map[string][2]int{
		team:     {0, 100}, // drawers written before the payload existed
		entities: {0, 100},
	}}

	warnings := collect(t, sampler, []string{team, entities})

	if len(warnings) != 1 {
		t.Fatalf("want exactly one warning for the unlabelled drawer namespace, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "100 of 100") {
		t.Errorf("the warning must count what it sampled, got %q", warnings[0])
	}
	if !strings.Contains(warnings[0], "sync --repair-payload") {
		t.Errorf("the warning must name the repair, got %q", warnings[0])
	}
}

// TestAPartiallyLabelledNamespaceWarns covers the state a repair leaves behind if
// it is interrupted: some points labelled, some not. Scoped search is genuinely
// lossy there, and reporting it needs the count to be the MISSING ones rather
// than the sampled ones.
func TestAPartiallyLabelledNamespaceWarns(t *testing.T) {
	const team = "af063b4c-e118-4faa-9507-32de9fdad5ed"
	sampler := &stubSampler{coverage: map[string][2]int{team: {60, 100}}}

	warnings := collect(t, sampler, []string{team})

	if len(warnings) != 1 {
		t.Fatalf("want one warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "40 of 100") {
		t.Errorf("want the count of MISSING points, got %q", warnings[0])
	}
}

// TestAnUnreachableNamespaceIsNotThisChecksBusiness keeps the check quiet about
// what it could not measure. A boot warning that fires when Qdrant is briefly
// unreachable teaches operators to ignore it, which costs more than the one real
// finding it exists to deliver.
func TestAnUnreachableNamespaceIsNotThisChecksBusiness(t *testing.T) {
	sampler := &stubSampler{coverage: map[string][2]int{}} // every lookup errors

	if warnings := collect(t, sampler, []string{"unreachable-team"}); len(warnings) != 0 {
		t.Fatalf("an unreachable collection warned: %v", warnings)
	}
}
