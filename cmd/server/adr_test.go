package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// adrNumberRe extracts the number from an ADR filename (ADR-033-…).
var adrNumberRe = regexp.MustCompile(`^ADR-(\d+)-`)

// duplicateADRNumbers returns the numbers claimed by more than one file.
func duplicateADRNumbers(files []string) []int {
	seen := map[int]string{}
	var dupes []int
	for _, f := range files {
		m := adrNumberRe.FindStringSubmatch(filepath.Base(f))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if _, ok := seen[n]; ok {
			dupes = append(dupes, n)
		} else {
			seen[n] = f
		}
	}
	return dupes
}

// TestDuplicateADRNumbersDetectsCollisions is the mechanism test: the walker
// is only worth anything if the checker it wraps actually catches a second
// claim of a number — the August collision recurrences (two competing
// ADR-024s, then ADR-027/028 claimed twice across open PRs) prove the failure
// mode is real and silent without a gate.
func TestDuplicateADRNumbersDetectsCollisions(t *testing.T) {
	dupes := duplicateADRNumbers([]string{
		"ADR-027-a-behind-index-cannot-return-an-empty-answer.md",
		"ADR-027-a-maintained-document-is-a-set-of-records.md",
		"ADR-028-return-the-identifier-and-the-score.md",
	})
	if len(dupes) != 1 || dupes[0] != 27 {
		t.Fatalf("collision detection returned %v; want [27]", dupes)
	}
	if d := duplicateADRNumbers([]string{"ADR-001-x.md", "ADR-002-y.md"}); len(d) != 0 {
		t.Errorf("unique numbers reported as colliding: %v", d)
	}
}

// TestADRNumbersAreUnique walks docs/adr/: an ADR number is a contract between
// the document and every reference to it, and a second file claiming the same
// number merges without a git conflict. The repo's own doctrine — anything
// that must stay true gets a command whose exit code says so — is the answer
// the palace already recorded for the collision mechanism.
func TestADRNumbersAreUnique(t *testing.T) {
	entries, err := os.ReadDir("../../docs/adr")
	if err != nil {
		t.Fatalf("read docs/adr: %v", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	for _, n := range duplicateADRNumbers(files) {
		t.Errorf("ADR-%03d is claimed by more than one file in docs/adr", n)
	}
}
