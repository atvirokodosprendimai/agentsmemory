package qdrant

import (
	"strings"
	"testing"
)

// TestCollectionNameIsDeterministicAndScoped verifies the tenancy invariant the
// collection-per-tenant design rests on: the same team always maps to the same
// collection, different teams never collide, and the name is a safe slug.
func TestCollectionNameIsDeterministicAndScoped(t *testing.T) {
	teamA := "11111111-1111-1111-1111-111111111111"
	teamB := "22222222-2222-2222-2222-222222222222"

	a1 := CollectionName(teamA)
	a2 := CollectionName(teamA)
	b1 := CollectionName(teamB)

	if a1 != a2 {
		t.Fatalf("non-deterministic: %q != %q", a1, a2)
	}
	if a1 == b1 {
		t.Fatalf("collision: teamA and teamB share collection %q", a1)
	}
	if !strings.HasPrefix(a1, "mempalace_") || !strings.HasSuffix(a1, "_drawers") {
		t.Fatalf("unexpected format: %q", a1)
	}
	// mempalace_(16 hex)_drawers
	if got := len(a1); got != len("mempalace_")+16+len("_drawers") {
		t.Fatalf("unexpected length %d for %q", got, a1)
	}
}
