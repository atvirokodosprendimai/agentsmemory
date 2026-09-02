package longmemeval

import (
	"slices"
	"testing"
)

func TestSubsetIsDeterministicForASeed(t *testing.T) {
	ds := mustLoadSample(t)

	a := Subset(ds, 4, 7)
	b := Subset(ds, 4, 7)
	if !slices.Equal(a.IDs, b.IDs) {
		t.Fatalf("two calls with one seed chose different questions: %v vs %v", a.IDs, b.IDs)
	}
	if c := Subset(ds, 4, 8); slices.Equal(a.IDs, c.IDs) && len(ds.Questions) > 4 {
		t.Error("a different seed chose an identical subset; the selection is not seeded at all")
	}
	if a.Seed != 7 || a.N != 4 {
		t.Errorf("the subset must carry the seed and n that produced it, got seed=%d n=%d", a.Seed, a.N)
	}
}

func TestSubsetStratifiesByQuestionType(t *testing.T) {
	ds := mustLoadSample(t)

	// The fixture holds one question of each of the six types. A subset of six
	// must therefore span all six; an unstratified sample would be free to
	// return the same type repeatedly on a larger corpus, and a small --n would
	// silently become five multi-session questions.
	sub := Subset(ds, 6, 1)
	seen := map[string]int{}
	for _, q := range sub.Questions {
		seen[q.Type]++
	}
	if len(seen) != 6 {
		t.Fatalf("a subset of 6 over six types spans %d types, want 6: %v", len(seen), seen)
	}

	half := Subset(ds, 3, 1)
	seenHalf := map[string]int{}
	for _, q := range half.Questions {
		seenHalf[q.Type]++
	}
	if len(seenHalf) != 3 {
		t.Errorf("a subset of 3 must take 3 distinct types before repeating one, got %v", seenHalf)
	}
}

func TestSubsetOfMoreThanTheCorpusIsTheCorpus(t *testing.T) {
	ds := mustLoadSample(t)
	sub := Subset(ds, 999, 1)
	if len(sub.Questions) != len(ds.Questions) {
		t.Errorf("subset of 999 over %d questions returned %d", len(ds.Questions), len(sub.Questions))
	}
}

func mustLoadSample(t *testing.T) Dataset {
	t.Helper()
	ds, err := Load(samplePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return ds
}
