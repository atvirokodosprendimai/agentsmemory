package repohygiene

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// humanEntryRE matches an adr-verify --human sign-off line in a task's
// Verification Log. adr-verify writes the date and the marker; everything after
// them is the text the operator supplied, which is where the outcome lands.
var humanEntryRE = regexp.MustCompile(`(?m)^- (\d{4}-\d{2}-\d{2}) · human-observed · (.+)$`)

// decisionRE finds the outcome word a human sign-off is required to name.
//
// ⚠ THE LAST MATCH WINS, NOT THE FIRST. A realistic entry mentions the word
// "decision" more than once — T3's own template pairs the verdict with "recorded
// in evidence/…", giving `…the decision is recorded in evidence/x.md; decision
// ship`. Taking the first match captures "is" and REJECTS A VALID SIGN-OFF, and a
// false alarm is the worst failure mode a hygiene gate has: it is how a gate gets
// switched off. The verdict is the clause an author writes last, so read the last.
var decisionRE = regexp.MustCompile(`(?i)\bdecision[:\s]+([a-z]+)`)

// readmeRowRE pulls a task's id and status cell out of a tasks/README.md row.
var readmeRowRE = regexp.MustCompile(`(?m)^\|\s*(T\d+)\s*\|[^|]*\|\s*([^|]*?)\s*\|`)

// statusForDecision maps the three outcomes a human-observed task can reach onto
// the README status that reports each one.
//
// The vocabulary is three words rather than two because a run can END WITHOUT
// DECIDING: ADR-001 T3 ran its gate, found the corpus saturated and therefore
// unfit to decide, and recorded "neither ship nor withdraw". Its own acceptance
// hint offered only `decision <ship|withdraw>`, so the real outcome had nowhere to
// go and landed in free text. That is not a one-off — ADR-004's supersession gate
// reached the same third state on 2026-08-24 ("REFUSED — NOT 'no'; the gate could
// not answer"), which is what issue #34 is still open about.
//
// ⚠ `blocked` NOW CARRIES THREE MEANINGS ACROSS THREE TOOLS, and this is where the
// mapping is defined: `adr-next --all` prints it for a task whose DEPENDENCIES are
// unmet, `adr-lint` treats it as externally blocked with a green fence, and here it
// means the task RAN and its verdict was stop. They do not conflict today because
// no task is in two of those states at once; a reader comparing tools should know
// the word is overloaded.
var statusForDecision = map[string]string{
	"ship":     "done",
	"withdraw": "failed",
	"blocked":  "blocked",
}

// TestAHumanObservedSignOffAgreesWithTheIndex closes the one acceptance path whose
// OUTCOME is prose.
//
// ⚠ A HUMAN SIGN-OFF IS COUNTED BY ITS GRAMMAR, NEVER BY WHAT IT SAYS. Every other
// acceptance route reports a verdict the tooling reads: a tool-written entry
// carries an exit code and a fence digest, and a task is done only when both match.
// The human route carries neither. `adr-next`'s `is_done` accepts a human entry on
// the date-and-marker pattern alone, so ANY text after the marker reads as success
// — including text that says the work must stop.
//
// Measured 2026-08-28: ADR-001 T3 signed off "decision BLOCKED — neither ship nor
// withdraw … T4/T5/T6 not started", and `adr-next` answered `done T3` / `READY T1`
// while `tasks/README.md` still said `pending`. `adr-lint` passed over the
// divergence, and `work-next` named ADR-001's remaining tasks as the next work in
// the whole repository — routing an executor into building exactly what T3 forbade.
//
// The half that is ours is not the regex, which lives in another tree. It is that
// the schema had no representation for "ran, and the answer is stop".
func TestAHumanObservedSignOffAgreesWithTheIndex(t *testing.T) {
	checkHumanSignOffs(t, repoRoot(t))
}

// checkHumanSignOffs is the whole verdict path — walk, read the index, report — in
// one substitutable place.
//
// ⚠ IT TAKES A testing.TB SO THE SUBTEST BELOW CAN SUBSTITUTE ONE. An earlier
// version put this body inline in the gate and pinned only the comparison helper.
// Severing the CALL to that helper then left the whole suite at exit 0 while the
// gate printed "1 human-observed task(s), 1 signed off, all agreeing with their
// index" — over a corpus where the index and the sign-off did not agree. A disabled
// gate that stays quiet is bad; one that affirmatively reports success is worse,
// which this package already knew (`checkCitations`) and this file had to relearn.
func checkHumanSignOffs(tb testing.TB, root string) {
	tb.Helper()
	tasks, err := filepath.Glob(filepath.Join(root, "docs", "adr", "*", "tasks", "T*.md"))
	if err != nil {
		tb.Fatalf("glob task files: %v", err)
	}
	if len(tasks) == 0 {
		tb.Fatal("no task files under docs/adr/*/tasks — this gate derives its universe from " +
			"the corpus, so an empty result means the layout moved, not that there is nothing to check")
	}

	readmeCache := map[string]map[string]string{}
	statusOf := func(dir, tid string) (string, bool) {
		rows, ok := readmeCache[dir]
		if !ok {
			rows = map[string]string{}
			if b, err := os.ReadFile(filepath.Join(dir, "README.md")); err == nil {
				for _, m := range readmeRowRE.FindAllStringSubmatch(string(b), -1) {
					// A README carries a dependency table using the same row shape;
					// keep the first row per id, which is the status table.
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

	humanTasks, signedOff, problems := 0, 0, 0
	for _, path := range tasks {
		body, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		text := string(body)
		if !strings.Contains(text, "Acceptance is human-observed") {
			continue
		}
		humanTasks++
		rel, _ := filepath.Rel(root, path)
		tid := taskIDFromPath(path)

		// ⚠ THE ARTIFACT AN OPERATOR READS MUST OFFER THE VOCABULARY THE GATE DEMANDS.
		// This is the defect that created the finding, one level up: T3's acceptance
		// hint prescribed `decision <ship|withdraw>` while the run reached a third
		// state, so the honest outcome had nowhere to go. A gate requiring three
		// values beside a template offering two reproduces the dead end for the next
		// operator, and nothing connected the two until this check.
		for word := range statusForDecision {
			if !strings.Contains(text, word) {
				problems++
				tb.Errorf("%s (%s): its acceptance section never mentions %q, but the sign-off gate "+
					"requires one of %s\n"+
					"An operator writes what the template shows them. If the template cannot express "+
					"the outcome they reached, it goes into free text where no tool reads it.",
					rel, tid, word, decisionVocabulary())
			}
		}

		entries := humanEntryRE.FindAllStringSubmatch(text, -1)
		if len(entries) == 0 {
			// ⚠ NOT SILENT. A task carrying the marker but no parseable entry is
			// either unsigned (adr-lint's case) or the format drifted under us —
			// and the format is written by a tool in ANOTHER REPOSITORY, so it can
			// change without a commit here. Distinguish the two.
			if strings.Contains(text, "· human-observed ·") {
				problems++
				tb.Errorf("%s (%s): contains `· human-observed ·` but humanEntryRE matched nothing\n"+
					"The sign-off format is written by adr-verify, which lives outside this repo, so "+
					"it can drift without a commit here. Update the pattern — do not let this gate "+
					"pass vacuously over an entry it can no longer read.", rel, tid)
			}
			continue
		}
		// The LAST sign-off is the standing one; an earlier superseded run is
		// history, exactly like a Verification Log's earlier red entry.
		last := entries[len(entries)-1]
		signedOff++

		got, ok := statusOf(filepath.Dir(path), tid)
		if !ok {
			problems++
			tb.Errorf("%s (%s): it has a human sign-off but the sibling README has no row for it\n"+
				"The index is where an executor and every routing tool read this task's state.", rel, tid)
			continue
		}
		if problem := signOffProblem(last[2], got); problem != "" {
			problems++
			tb.Errorf("%s (%s): %s\n"+
				"  entry: %s\n"+
				"A human-observed entry is counted done by its DATE AND MARKER alone, so text saying "+
				"the work must stop reads as success. The outcome has to be named from %s and the "+
				"index has to report the same thing — otherwise it is recorded where only a human "+
				"reads it, while every tool that routes work reads the index.",
				rel, tid, problem, truncate(last[2], 160), decisionVocabulary())
		}
	}

	// Two vacuity guards, because this gate's universe is small and its input format
	// is owned elsewhere. Either number reaching zero means the extraction broke,
	// not that the corpus went quiet.
	if humanTasks == 0 {
		tb.Fatal("found no task declaring `Acceptance is human-observed` across docs/adr/*/tasks — " +
			"a green run here would mean the phrase changed, not that every sign-off agrees with its index")
	}
	if signedOff == 0 {
		tb.Fatalf("%d human-observed task(s) and NOT ONE parseable sign-off — the gate is passing "+
			"vacuously. Either every such task is unsigned, or humanEntryRE stopped matching the "+
			"format adr-verify writes.", humanTasks)
	}
	// ⚠ Report only on a clean verdict. "all agreeing with their index" printed
	// underneath a failure is the disabled-gate-announcing-all-clear shape.
	if problems == 0 {
		tb.Logf("%d human-observed task(s), %d signed off, all agreeing with their index", humanTasks, signedOff)
	}
}

// signOffProblem is the comparison, split out so the table-driven half below drives
// THIS code rather than a copy. Returns the empty string when sign-off and index
// agree.
func signOffProblem(entry, status string) string {
	all := decisionRE.FindAllStringSubmatch(entry, -1)
	if len(all) == 0 {
		return "names no decision"
	}
	decision := strings.ToLower(all[len(all)-1][1])
	want, known := statusForDecision[decision]
	if !known {
		return fmt.Sprintf("decision %q is not one of %s", decision, decisionVocabulary())
	}
	if status != want {
		return fmt.Sprintf("decision %q requires status %q, index reads %q", decision, want, status)
	}
	return ""
}

// TestASignOffThatSaysStopIsCaught is the falsifiability half, in two parts.
//
// A corpus with zero disagreements cannot exercise the branch that reports one, so
// the gate would pass identically with its body deleted. The comparison table pins
// signOffProblem; the fixture subtest pins the PATH THROUGH IT, by running
// checkHumanSignOffs over a corpus built to be wrong and asserting through a
// substitutable testing.TB that it reported. Without the second part, severing the
// call site leaves everything green — verified, and that is why it exists.
func TestASignOffThatSaysStopIsCaught(t *testing.T) {
	t.Run("the comparison", func(t *testing.T) {
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
			// ⚠ The regex must read the LAST clause. This entry is the shape T3's own
			// template produces, and the first-match version rejected it as decision "is".
			{"a valid ship after the word decision appears earlier", "gate exit 0; the decision is recorded in evidence/abstain-gate.md; decision ship", "done", false},
			{"a stop after an earlier mention", "the decision is in the log below; decision blocked", "blocked", false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if failed := signOffProblem(c.entry, c.status) != ""; failed != c.wantFail {
					t.Errorf("entry %q with status %q: caught=%v, want %v", c.entry, c.status, failed, c.wantFail)
				}
			})
		}
	})

	t.Run("the path through it", func(t *testing.T) {
		root := t.TempDir()
		// Not named ADR-<n>: TestEveryCitedADRResolves scans this file for ADR
		// citations and a fixture number would read as a pointer to a record that
		// does not exist. It caught exactly that on the first draft.
		dir := filepath.Join(root, "docs", "adr", "fixture-record", "tasks")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		task := "# Task T1: a fixture\n\nAcceptance is human-observed: nothing hermetic can stand in " +
			"for it. Sign off with `decision <ship|withdraw|blocked>`.\n\n" +
			"## Verification Log\n- 2026-08-28 · human-observed · gate exit 1; decision BLOCKED — neither ship nor withdraw\n"
		if err := os.WriteFile(filepath.Join(dir, "T1-fixture.md"), []byte(task), 0o644); err != nil {
			t.Fatal(err)
		}
		readme := "| ID | Title | Status | Covers | Acceptance |\n|----|-------|--------|--------|------------|\n" +
			"| T1 | a fixture | pending | — | human-observed |\n"
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
			t.Fatal(err)
		}

		rec := &recordingTB{}
		checkHumanSignOffs(rec, root)
		if rec.errors == 0 {
			t.Error("a sign-off saying decision BLOCKED against an index reading `pending` was not " +
				"reported.\nThis is the cell that matters: severing the call site inside " +
				"checkHumanSignOffs leaves the comparison table green and the whole suite at exit 0, " +
				"while the gate prints that everything agrees.")
		}

		// The negative half, without which "reports" is satisfied by reporting always.
		fixed := strings.Replace(readme, "| pending |", "| blocked |", 1)
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(fixed), 0o644); err != nil {
			t.Fatal(err)
		}
		clean := &recordingTB{}
		checkHumanSignOffs(clean, root)
		if clean.errors != 0 {
			t.Errorf("an index agreeing with its sign-off was reported anyway (%d error(s)) — "+
				"a gate that fires on everything is one people switch off", clean.errors)
		}
	})
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

// truncate cuts to at most n BYTES without splitting a rune, because the text it
// shortens is an operator's prose and this repo's corpus is full of em dashes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
