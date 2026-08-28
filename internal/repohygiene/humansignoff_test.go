package repohygiene

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// humanEntryRE matches an adr-verify --human sign-off line in a task's
// Verification Log. adr-verify writes the date and the marker; everything after
// them is the text the operator supplied, which is where the outcome lands.
var humanEntryRE = regexp.MustCompile(`(?m)^- (\d{4}-\d{2}-\d{2}) · human-observed · (.+)$`)

// decisionRE finds the outcome word a human sign-off is required to name.
var decisionRE = regexp.MustCompile(`(?i)\bdecision[:\s]+([a-z]+)`)

// readmeRowRE pulls a task's id and status cell out of a tasks/README.md row.
var readmeRowRE = regexp.MustCompile(`(?m)^\|\s*(T\d+)\s*\|[^|]*\|\s*([^|]*?)\s*\|`)

// statusForDecision maps the three outcomes a human-observed task can reach onto
// the README status that reports each one.
//
// The vocabulary is three words rather than two because a run can END without
// deciding: ADR-001 T3 ran its gate, found the corpus saturated and therefore
// unfit to decide, and recorded "neither ship nor withdraw". Its own acceptance
// hint offered only `decision <ship|withdraw>`, so the real outcome had nowhere
// to go and landed in free text.
var statusForDecision = map[string]string{
	"ship":     "done",
	"withdraw": "failed",
	"blocked":  "blocked",
}

// TestAHumanObservedSignOffAgreesWithTheIndex closes the one acceptance path
// whose OUTCOME is prose.
//
// ⚠ A HUMAN SIGN-OFF IS COUNTED BY ITS GRAMMAR, NEVER BY WHAT IT SAYS. Every
// other acceptance route reports a verdict the tooling reads: a tool-written
// entry carries an exit code and a fence digest, and a task is done only when
// both match. The human route carries neither. `adr-next`'s `is_done` accepts a
// human entry on the date-and-marker pattern alone, so ANY text after the marker
// reads as success — including text that says the work must stop.
//
// This is not hypothetical and it is not cheap. Measured 2026-08-28 on this
// corpus: ADR-001 T3 signed off "decision BLOCKED — neither ship nor withdraw …
// T4/T5/T6 not started", and `adr-next` answered `done T3` / `READY T1` while
// `tasks/README.md` still said `pending`. `adr-lint` passed over the divergence,
// and `work-next` named ADR-001's remaining tasks as the next work in the whole
// repository — routing an executor into building exactly what T3 had forbidden.
//
// The half that is ours to fix is not the regex, which lives in another tree. It
// is that the schema had no representation for "ran, and the answer is stop":
// T3's own acceptance hint prescribed `decision <ship|withdraw>`, two values, and
// the executor hit a third. This requires every human sign-off to name its
// outcome from a vocabulary that HAS that third value, and requires the ADR's own
// index to report the same thing.
func TestAHumanObservedSignOffAgreesWithTheIndex(t *testing.T) {
	root := repoRoot(t)
	tasks, err := filepath.Glob(filepath.Join(root, "docs", "adr", "*", "tasks", "T*.md"))
	if err != nil {
		t.Fatalf("glob task files: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("no task files under docs/adr/*/tasks — this gate derives its universe from " +
			"the corpus, so an empty result means the layout moved, not that there is nothing to check")
	}

	readmeCache := map[string]map[string]string{}
	statusOf := func(dir, tid string) (string, bool) {
		rows, ok := readmeCache[dir]
		if !ok {
			rows = map[string]string{}
			if b, err := os.ReadFile(filepath.Join(dir, "README.md")); err == nil {
				for _, m := range readmeRowRE.FindAllStringSubmatch(string(b), -1) {
					// A README carries a dependency table using the same row
					// shape; the status table is the one whose cell is a known
					// status word, so keep the first row per id and let a
					// non-status cell fail loudly rather than silently pass.
					if _, seen := rows[m[1]]; !seen {
						rows[m[1]] = strings.ToLower(strings.Trim(m[2], "`* "))
					}
				}
			}
			readmeCache[dir] = rows
		}
		s, ok := rows[tid]
		return s, ok
	}

	humanTasks, checked := 0, 0
	for _, path := range tasks {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(body)
		if !strings.Contains(text, "Acceptance is human-observed") {
			continue
		}
		humanTasks++
		rel, _ := filepath.Rel(root, path)
		tid := taskIDFromPath(path)

		entries := humanEntryRE.FindAllStringSubmatch(text, -1)
		if len(entries) == 0 {
			continue // signed off by nobody yet; adr-lint owns the done-without-evidence case
		}
		// The LAST sign-off is the standing one; an earlier run that was
		// superseded by a later one is history, exactly like a Verification Log's
		// earlier red entry.
		last := entries[len(entries)-1]
		checked++

		got, ok := statusOf(filepath.Dir(path), tid)
		if !ok {
			t.Errorf("%s (%s): it has a human sign-off but the sibling README has no row for it\n"+
				"The index is where an executor and every routing tool read this task's state.", rel, tid)
			continue
		}
		if problem := signOffProblem(last[2], got); problem != "" {
			t.Errorf("%s (%s): %s\n"+
				"  entry: %s\n"+
				"A human-observed entry is counted done by its DATE AND MARKER alone, so text saying "+
				"the work must stop reads as success. The outcome has to be named from %s and the "+
				"index has to report the same thing — otherwise it is recorded where only a human "+
				"reads it, while every tool that routes work reads the index.",
				rel, tid, problem, truncate(last[2], 160), decisionVocabulary())
		}
	}

	if humanTasks == 0 {
		t.Error("found no task declaring `Acceptance is human-observed` across " +
			"docs/adr/*/tasks — a green run here would mean the phrase changed, not that " +
			"every human sign-off agrees with its index")
	}
	if !t.Failed() {
		t.Logf("%d human-observed task(s), %d signed off, all agreeing with their index", humanTasks, checked)
	}
}

// TestASignOffThatSaysStopIsCaught is the falsifiability half.
//
// The corpus is expected to hold zero disagreements once this branch lands, and a
// clean corpus cannot exercise the branch that reports one — so the check above
// would pass identically with its body deleted. This drives the same comparison
// over fixtures that ARE wrong.
func TestASignOffThatSaysStopIsCaught(t *testing.T) {
	cases := []struct {
		name     string
		entry    string
		status   string
		wantFail bool
	}{
		{"a stop recorded as pending", "corpus unfit; decision BLOCKED — neither ship nor withdraw", "pending", true},
		{"a stop recorded as done", "decision blocked, T4 not started", "done", true},
		{"a ship recorded as done", "gate exit 0; decision ship", "done", false},
		{"a stop recorded as blocked", "decision BLOCKED — corpus saturated", "blocked", false},
		{"no decision named at all", "ran the gate, it looked fine", "done", true},
		{"a word outside the vocabulary", "decision maybe", "done", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			failed := signOffProblem(c.entry, c.status) != ""
			if failed != c.wantFail {
				t.Errorf("entry %q with status %q: caught=%v, want %v\n"+
					"Without this the gate above passes over a clean corpus whatever its body says.",
					c.entry, c.status, failed, c.wantFail)
			}
		})
	}
}

// signOffProblem is the whole judgement, in one place, so the falsifiability
// half below drives THIS code rather than a copy of it. The first draft had the
// subtest reimplement the comparison; severing the real one then left the subtest
// green, which is the "a test cannot pin its own reporting" trap AGENTS.md names.
// Returns the empty string when the sign-off and the index agree.
func signOffProblem(entry, status string) string {
	d := decisionRE.FindStringSubmatch(entry)
	if d == nil {
		return "names no decision"
	}
	decision := strings.ToLower(d[1])
	want, known := statusForDecision[decision]
	if !known {
		return fmt.Sprintf("decision %q is not one of %s", decision, decisionVocabulary())
	}
	if status != want {
		return fmt.Sprintf("decision %q requires status %q, index reads %q", decision, want, status)
	}
	return ""
}

func decisionVocabulary() string {
	return "`decision ship`, `decision withdraw`, `decision blocked`"
}

// taskIDFromPath reads the task id off the filename, which is the one place it is
// guaranteed present: a task file's H1 may or may not repeat it.
func taskIDFromPath(path string) string {
	base := filepath.Base(path)
	if i := strings.IndexByte(base, '-'); i > 0 {
		return base[:i]
	}
	return strings.TrimSuffix(base, ".md")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return fmt.Sprintf("%s…", s[:n])
}
