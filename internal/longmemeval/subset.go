package longmemeval

import (
	"math/rand/v2"
	"sort"
)

// Selection is the questions one run was taken over, together with what
// selected them.
//
// The ids travel with the numbers for the reason ADR-047's decision rule needs
// them to: the argmax is taken on one half and confirmed on the other, and a
// later, larger run is only comparable to this one if it can say which questions
// this one saw. Seed and N are here so that reproduction needs no notes.
type Selection struct {
	Questions []Question
	IDs       []string
	Seed      int64
	N         int
}

// Subset chooses n questions, stratified by question type and deterministic for
// a seed.
//
// Stratified rather than uniform because n is small by design — ADR-047 runs a
// declared subset, not all 500 — and a uniform draw of twenty over six types
// will silently over-weight one. The failure that matters is not the imbalance
// itself but that per-type scores are how this instrument reports at all: a
// policy can help multi-session recall and hurt preferences, and a subset that
// holds five of one type and none of another reports the mean as if it were the
// finding.
//
// The selection walks the types round-robin, taking one shuffled question from
// each in turn, so a subset smaller than the number of types still spans as many
// distinct types as it has slots. Types are visited in sorted order and each
// stratum is shuffled with a seeded PCG, which makes the whole function a pure
// function of (dataset, n, seed) — no map iteration order reaches the result.
func Subset(ds Dataset, n int, seed int64) Selection {
	byType := map[string][]Question{}
	for _, q := range ds.Questions {
		byType[q.Type] = append(byType[q.Type], q)
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)

	// Sorting first makes the shuffle a function of the seed alone rather than of
	// Go's map iteration order, which is what keeps the whole call reproducible.
	rng := rand.New(rand.NewPCG(uint64(seed), 0x9E3779B97F4A7C15))
	for _, t := range types {
		qs := byType[t]
		rng.Shuffle(len(qs), func(i, j int) { qs[i], qs[j] = qs[j], qs[i] })
	}

	// The VISIT order is shuffled too, and that is not tidiness. When n is
	// smaller than the number of types — the ordinary case for a cheap pilot run
	// — a fixed order always admits the alphabetically-first types, so
	// `single-session-assistant` would be in every small run ever taken and
	// `temporal-reasoning` in none. Which types a small subset covers has to move
	// with the seed, or two runs at different seeds are the same experiment.
	rng.Shuffle(len(types), func(i, j int) { types[i], types[j] = types[j], types[i] })

	out := Selection{Seed: seed, N: n}
	for round := 0; len(out.Questions) < n; round++ {
		took := false
		for _, t := range types {
			if round >= len(byType[t]) {
				continue
			}
			out.Questions = append(out.Questions, byType[t][round])
			took = true
			if len(out.Questions) == n {
				break
			}
		}
		// Every stratum is exhausted: the corpus is smaller than n, which is a
		// legitimate call and returns the whole corpus rather than looping.
		if !took {
			break
		}
	}
	out.IDs = make([]string, len(out.Questions))
	for i, q := range out.Questions {
		out.IDs[i] = q.ID
	}
	return out
}
