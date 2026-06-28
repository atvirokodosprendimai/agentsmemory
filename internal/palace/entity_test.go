package palace

import (
	"strings"
	"testing"
)

// has reports whether entities contains name.
func has(entities []string, name string) bool {
	for _, e := range entities {
		if e == name {
			return true
		}
	}
	return false
}

func TestExtractEntitiesFrequencyThreshold(t *testing.T) {
	// "Zephyr" appears twice (an entity); "Nimbus" once (below threshold).
	got := extractEntities("Zephyr launched the run. Later Zephyr paged Nimbus once.")
	if !has(got, "Zephyr") {
		t.Fatalf("Zephyr (x2) should be an entity, got %v", got)
	}
	if has(got, "Nimbus") {
		t.Fatalf("Nimbus (x1) is below the freq>=2 threshold, got %v", got)
	}
}

func TestExtractEntitiesDropsStoplistAndCommon(t *testing.T) {
	// "The" is stoplisted; "Action"/"After" are common (COCA / stoplist) — none
	// should survive even though each appears twice and is capitalized.
	got := extractEntities("The cache. The cache. Action here. Action there. After this. After that.")
	for _, bad := range []string{"The", "Action", "After"} {
		if has(got, bad) {
			t.Fatalf("%q should be filtered (stoplist/COCA), got %v", bad, got)
		}
	}
}

func TestExtractEntitiesShortDropped(t *testing.T) {
	// "Hi" repeats but is only two characters (needs > 2), so it is not an entity.
	if got := extractEntities("Hi Hi Hi there"); has(got, "Hi") {
		t.Fatalf("two-char token should be dropped, got %v", got)
	}
}

func TestExtractEntitiesKnownSystemCompound(t *testing.T) {
	// A multi-word known system is recognised as ONE entity when it recurs, and its
	// constituent words are masked (not separately counted).
	got := extractEntities("We shipped with Claude Code today. Then Claude Code again tomorrow.")
	if !has(got, "Claude Code") {
		t.Fatalf("recurring known system should be an entity, got %v", got)
	}
	if has(got, "Claude") {
		t.Fatalf("known-system constituent should be masked, not counted: %v", got)
	}
}

func TestExtractEntitiesSortedAndCapped(t *testing.T) {
	// Many distinct recurring entities must come back sorted and capped at the limit.
	var sb strings.Builder
	for i := 0; i < entityMetadataLimit+10; i++ {
		// Each name appears twice; names are Aaa00..Aaa34-ish, clearly proper nouns.
		name := "Zz" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		sb.WriteString(name + " " + name + ". ")
	}
	got := extractEntities(sb.String())
	if len(got) > entityMetadataLimit {
		t.Fatalf("entities should be capped at %d, got %d", entityMetadataLimit, len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("entities should be sorted, got %v", got)
		}
	}
}
