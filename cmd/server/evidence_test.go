package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ADR-003 T3's completeness gate: four run records, one binary, one clean
// commit, the deciding cells present and above their floors.
//
// ⚠ IT CHECKS COMPLETENESS AND PROVENANCE, NEVER DIRECTION. A run saying the
// closet prior HELPS must pass this exactly as one saying it hurts — the task's
// own invariant, and the reason the assertions below never look at the sign of
// DeltaMRR or of an interval bound. A gate that only accepts the answer its
// author wanted is not a gate, and this ADR is retiring a default that shipped
// before its measurement was taken, so the temptation is real rather than
// theoretical.
//
// The floors come from the ADR (§Floors): D1 is read only with at least 40
// admitted cases, D2/R1/R2 only with at least 10, because below ten a paired
// bootstrap resamples fewer than ten distinct deltas and restates the sample
// instead of estimating anything. A cell BELOW its floor is not a failure of
// this test — Table 2 gives it the value `n/a` — so the floor is asserted for
// D1 alone, which is the cell that decides the ADR.
//
// `moved > 0` in at least one record is the instrument check the ADR reads
// first (cell S1): if the two arms never ranked any admitted case differently,
// the closet-ON and closet-OFF heads are producing identical orderings and
// every delta below is measuring nothing.

// closetRecord is the subset of a `<stem>.cells.json` this gate reads.
type closetRecord struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
	Wing   string `json:"wing"`
	Style  string `json:"style"`
	Cells  []struct {
		Category string `json:"Category"`
		Admitted int    `json:"Admitted"`
		Moved    int    `json:"Moved"`
	} `json:"cells"`
}

// closetEvidenceDir is where T3's four records live.
const closetEvidenceDir = "../../docs/adr/ADR-003-retire-the-closet-prior/evidence"

// TestClosetEvidenceIsComplete is the gate.
func TestClosetEvidenceIsComplete(t *testing.T) {
	// Named for the ADR's cell ids so a failure names the row of Table 2 that
	// cannot be read, rather than a filename the reader then has to map back.
	want := []struct {
		cell, file, category string
		floor                int
	}{
		{"D1", "mined-paraphrase.cells.json", "single", 40},
		{"D2", "mined-real.cells.json", "real", 0},
		{"R1", "curated-paraphrase.cells.json", "single", 0},
		{"R2", "curated-real.cells.json", "real", 0},
	}

	var commits []string
	anyMoved := false
	for _, w := range want {
		path := filepath.Join(closetEvidenceDir, w.file)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s (%s): %v\n"+
				"  T3 is not done until all four runs have been taken with ONE binary. A missing "+
				"record is not a small gap: Table 2 reads the ADR's outcome from these four cells "+
				"together, and three of them decide nothing on their own.", w.cell, w.file, err)
			continue
		}
		var rec closetRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Errorf("%s (%s): parse: %v", w.cell, w.file, err)
			continue
		}
		if rec.Commit == "" {
			t.Errorf("%s (%s): no commit recorded. `go run` carries no VCS stamp, so a record "+
				"without one cannot be tied to the code that produced it", w.cell, w.file)
		}
		if rec.Dirty {
			t.Errorf("%s (%s): taken from a dirty tree, so the commit it names is not the code "+
				"that ran", w.cell, w.file)
		}
		commits = append(commits, rec.Commit)

		var cell *struct {
			Category string `json:"Category"`
			Admitted int    `json:"Admitted"`
			Moved    int    `json:"Moved"`
		}
		for i := range rec.Cells {
			if rec.Cells[i].Category == w.category {
				cell = &rec.Cells[i]
				break
			}
		}
		if cell == nil {
			t.Errorf("%s (%s): carries no %q cell; its categories are %v",
				w.cell, w.file, w.category, categoriesOf(rec))
			continue
		}
		if cell.Moved > 0 {
			anyMoved = true
		}
		// Only D1 carries a floor here. A cell below ITS floor is `n/a` by Table
		// 2 rather than a failure, so asserting floors on D2/R1/R2 would turn a
		// recorded non-result into a red test — which is how a gate starts
		// pressuring the corpus instead of measuring it.
		if w.floor > 0 && cell.Admitted < w.floor {
			t.Errorf("%s (%s): %d admitted case(s), below its floor of %d.\n"+
				"  This is the cell the ADR is decided from, and below the floor a paired "+
				"bootstrap restates the sample rather than estimating anything. Grow the mined "+
				"corpus — ⚠ --n is a ceiling on distinct source_file values, not on drawers, so "+
				"more drawers in the same sessions buys nothing.",
				w.cell, w.file, cell.Admitted, w.floor)
		}
	}

	for i := 1; i < len(commits); i++ {
		if commits[i] != commits[0] {
			t.Errorf("the four records do not agree on a commit (%s vs %s): a record from a "+
				"different sha is a different measurement, and Table 2 compares them against "+
				"each other", commits[0], commits[i])
			break
		}
	}

	if len(commits) == 4 && !anyMoved {
		t.Error("no record has moved > 0: the closet-ON and closet-OFF heads ranked every " +
			"admitted case identically in all four runs, so every delta below is measuring " +
			"nothing. This is cell S1, the instrument check the ADR says to read FIRST — a " +
			"green run here with no movement would be four records that cannot decide anything.")
	}
}

// categoriesOf lists what a record actually carries, so a missing-cell failure
// says what was there instead of only what was wanted.
func categoriesOf(rec closetRecord) []string {
	out := make([]string, 0, len(rec.Cells))
	for _, c := range rec.Cells {
		out = append(out, c.Category)
	}
	return out
}
