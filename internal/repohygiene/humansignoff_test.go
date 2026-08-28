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

// decisionRE finds candidate outcome words in a human sign-off.
//
// ⚠ NO POSITION RULE FINDS THE VERDICT, SO THIS ONE REFUSES TO GUESS. Three were
// tried and each failed differently on entries authors really write:
//
//	first match         "…the decision is recorded in evidence/x.md; decision ship"
//	                    → "is". FALSE ALARM on a valid sign-off.
//	last match          "decision ship; the decision will be revisited later"
//	                    → "will". FALSE ALARM on the mirror.
//	last in-vocabulary  "decision BLOCKED — neither ship nor withdraw; do not record
//	                     decision ship until the corpus grows", indexed done
//	                    → "ship". FALSE PASS, on exactly the routing failure this
//	                      gate exists to catch.
//
// The third is the worst of the three, and it is why position was abandoned:
// position is standing in for grammar, and the verdict is not reliably at either
// end. So the rule is COUNT, not position — exactly one outcome word from the
// vocabulary. Zero is unnamed; two or more is ambiguous, and an entry a machine
// cannot resolve is one a reader cannot resolve either, so it is reported rather
// than guessed at. Words outside the vocabulary ("is", "will", "was") are ignored,
// which is what keeps the two false-alarm shapes above passing.
//
// Measured 2026-08-28: the corpus's only real sign-off carries exactly one.
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

// offersWord matches an outcome word on a WORD BOUNDARY, one compiled matcher per
// vocabulary entry rather than one per word per file. `withdrawn` must not satisfy
// `withdraw` — the rule AGENTS.md already records from `stale` and "staleness", and
// the real T3 says "the ADR is withdrawn" in prose.
var offersWord = func() map[string]*regexp.Regexp {
	m := make(map[string]*regexp.Regexp, len(statusForDecision))
	for w := range statusForDecision {
		m[w] = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(w) + `\b`)
	}
	return m
}()

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
		// ⚠ THE FENCED TEMPLATE, NOT THE FILE AND NOT EVEN THE SECTION. An operator
		// copies the command out of the code fence; prose around it is not something
		// they paste. Two earlier versions of this check missed the defect it exists
		// to prevent — reverting ONLY the template line to two values left the gate
		// green, first because the whole file was searched, then because the
		// explanatory paragraph THIS TEST'S OWN PR added lives inside the very
		// section the search was narrowed to. A mechanism reporting success while
		// the thing it guards is broken, twice, which is the shape of the blocker it
		// fixes one layer out. Measured both times by reverting line 39 alone.
		//
		// ⚠ AND ON A WORD BOUNDARY, for the reason AGENTS.md already records about
		// `stale` and "staleness": a substring check credits "withdrawn" to
		// `withdraw`, and `T3-run-the-gate.md` says "the ADR is withdrawn" in prose
		// that has nothing to do with the sign-off template.
		acceptance := fencedIn(sectionOf(text, "Acceptance"))
		acceptance = strings.TrimSpace(acceptance)
		if acceptance == "" {
			problems++
			tb.Errorf("%s (%s): declares human-observed acceptance but its `## Acceptance` section "+
				"holds no fenced command\n"+
				"The fence is the sign-off template an operator copies; without one there is nothing "+
				"telling them what to write.", rel, tid)
		}
		for word := range statusForDecision {
			if acceptance == "" {
				break // already reported above; do not pile three more onto one cause
			}
			if !offersWord[word].MatchString(acceptance) {
				problems++
				tb.Errorf("%s (%s): its Acceptance section never offers %q, but the sign-off gate "+
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
//
// ⚠ IT COUNTS DISTINCT VERDICT WORDS; IT DOES NOT PARSE ENGLISH, and both limits
// below are deliberate rather than undiscovered.
//
// The COST: a sign-off naming two different outcomes is reported even when a human
// resolves it in one pass — "decision blocked — saturated; the decision withdraw
// option was considered and rejected" is unambiguous to a reader and rejected here,
// because "was considered and rejected" is exactly the clause a machine cannot
// read. That shape is a deliberate casualty, not an oversight; the earlier claim
// that an entry a machine cannot resolve is one a reader cannot resolve either was
// too strong, and this file's own fixture is the counter-example.
//
// The FLOOR: the count only sees clauses written in the `decision <word>` template
// form. A verdict in prose beside one template mention — "the decision is blocked;
// do not record decision ship until the corpus grows" — resolves to `ship` and
// passes. The remedy is to state the verdict in template form, and this function
// cannot tell the operator that, because the shape it would have to recognise is
// the shape it cannot read.
func signOffProblem(entry, status string) string {
	all := decisionRE.FindAllStringSubmatch(entry, -1)
	if len(all) == 0 {
		return "names no decision"
	}
	// ⚠ DISTINCT outcomes, not occurrences. Counting occurrences rejected a
	// CORRECT sign-off that stated one verdict twice — "decision ship; recorded in
	// evidence/x.md; per the stop condition T4 starts only on a decision ship" —
	// which is the shape an author writes when the entry mentions the index it just
	// updated. A false alarm on a correct entry is the failure this comparison has
	// already been rewritten three times to avoid, and it is worse than the miss it
	// would prevent: nobody switches a gate off for missing something.
	var saw []string
	seen := map[string]bool{}
	var outcomes []string
	for _, m := range all {
		w := strings.ToLower(m[1])
		saw = append(saw, w)
		if statusForDecision[w] == "" || seen[w] {
			continue
		}
		seen[w] = true
		outcomes = append(outcomes, w)
	}
	switch len(outcomes) {
	case 0:
		return fmt.Sprintf("names no decision from %s (saw %q)", decisionVocabulary(), strings.Join(saw, ", "))
	case 1: // the only resolvable shape
	default:
		// ⚠ NO POSITIONAL ADVICE HERE. "say it once, last" taught the operator a
		// rule this function abandoned: first-match and last-match were both tried
		// and both raised false alarms, which is why it counts instead.
		return fmt.Sprintf("names %d different decisions (%s) and a reader cannot tell which is "+
			"the verdict — state one", len(outcomes), strings.Join(outcomes, ", "))
	}
	decision := outcomes[0]
	want := statusForDecision[decision]
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
			// ⚠ The MIRROR of the case above: a valid verdict followed by a later
			// "decision" that is not one. Last-match alone rejects these, which is a
			// false alarm on a correct sign-off — so the reader takes the last
			// IN-VOCABULARY candidate, not simply the last.
			{"a ship followed by a later mention", "decision ship; the decision will be revisited once the corpus grows", "done", false},
			{"a ship followed by a passive mention", "decision ship — T4 may start; this decision was taken with the reranker live", "done", false},
			{"a stop wrapped on both sides", "the decision is recorded in evidence/x.md; decision blocked — saturated; the decision is final", "blocked", false},
			// ⚠ THE FALSE PASSES the position rules admitted. Each names two outcome
			// words; under "last in-vocabulary" the trailing one won and a BLOCKED
			// verdict indexed `done` passed the gate — the exact routing failure
			// this file exists to close. Reported as ambiguous now, never guessed.
			{"a stop with a later ship mention, indexed done", "corpus saturated; decision BLOCKED — neither ship nor withdraw; do not record decision ship until the corpus grows", "done", true},
			{"a stop with a trailing conditional ship", "decision blocked; T4 blocked until a later decision ship", "done", true},
			{"a withdraw with a hypothetical ship", "gate exit 1; decision withdraw; a future decision ship would need a fresh corpus", "done", true},
			{"two outcomes, correctly indexed for the first", "decision blocked — saturated; the decision withdraw option was considered and rejected", "blocked", true},
			// ⚠ ONE VERDICT STATED TWICE IS NOT AMBIGUOUS, and counting occurrences
			// rejected both of these. The second is what an author writes when the
			// entry mentions the index it just updated. Rejecting a CORRECT sign-off
			// is the failure mode that killed first-match and last-match, and it
			// would have arrived here through the fix for the other two.
			{"one verdict stated twice", "decision ship; recorded in evidence/x.md; per the stop condition T4 starts only on a decision ship", "done", false},
			{"a stop restated when the index is named", "decision blocked — corpus saturated; the sibling README now reads the decision blocked state", "blocked", false},
			// The floor the doc comment names: a verdict in prose beside one template
			// mention resolves to the template's word. Recorded as a case so the
			// limit is a pinned property rather than a sentence in a comment.
			{"a prose verdict beside one template mention is not read", "corpus saturated; the decision is blocked — neither ship nor withdraw; do not record decision ship until the corpus grows", "done", false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if failed := signOffProblem(c.entry, c.status) != ""; failed != c.wantFail {
					t.Errorf("entry %q with status %q: caught=%v, want %v", c.entry, c.status, failed, c.wantFail)
				}
			})
		}
	})

	// ⚠ THE TWO EXTRACTORS ARE PINNED HERE, NOT THROUGH THE CORPUS. Every real task
	// file has a flat `## Acceptance` and a well-formed fence, so the corpus cannot
	// reach either branch: reverting the depth rule or the empty-fence guard left the
	// whole suite at exit 0. New logic nothing selects is this repository's
	// characteristic defect, and it arrived inside the fix for it.
	t.Run("the extractors", func(t *testing.T) {
		doc := "## Acceptance\n\nbefore\n\n### A subsection\n\ninside\n\n## Verification Log\n\nafter\n"
		sec := sectionOf(doc, "Acceptance")
		if !strings.Contains(sec, "inside") {
			t.Errorf("a `###` subsection inside `## Acceptance` was cut off: the section stops at the "+
				"next SAME-OR-SHALLOWER heading, so a template split under a subheading would go "+
				"unread and the vocabulary check would pass over it.\n  got: %q", sec)
		}
		if strings.Contains(sec, "after") {
			t.Errorf("the section ran past `## Verification Log` into the next section: a verdict "+
				"already logged would then satisfy a check about what the TEMPLATE offers.\n  got: %q", sec)
		}

		if got := fencedIn("## Acceptance\n\n```text\n\n```\n"); strings.TrimSpace(got) != "" {
			t.Errorf("a whitespace-only fence yielded %q, not an empty string: the empty-fence guard "+
				"reads this value, and a fence carrying only newlines has to reach it as empty or "+
				"the guard reports a template that is present-and-blank as present", got)
		}
		if got := fencedIn("prose ```one two``` prose\n"); got != "" {
			t.Errorf("an inline span was read as a fence: %q. A fence opens with a newline after its "+
				"info string; without that the check would read backticked prose as the template", got)
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
		// ⚠ THE PROSE SENTENCE IS LOAD-BEARING. It mirrors the real T3, whose
		// explainer paragraph names all three words inside `## Acceptance` — which is
		// exactly why the vocabulary check has to read the FENCE and not the section.
		// Without this line, dropping fencedIn left the whole suite at exit 0 and the
		// two-value template was invisible again. Same for the word boundary
		// (`withdrawn` would satisfy `withdraw`) and the empty-fence guard.
		task := "# Task T1: a fixture\n\n## Acceptance\n\n" +
			"This section discusses ship, withdraw and blocked in prose, and the withdrawn case " +
			"at length; the FENCE below is the template an operator copies, not this sentence.\n\n" +
			"Acceptance is human-observed: nothing hermetic can stand in for it. Sign-off step —\n\n" +
			"```text\nadr-verify T1-fixture.md --human \"...; decision <ship|withdraw|blocked>\"\n```\n\n" +
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

		// ⚠ THE VOCABULARY CHECK NEEDS ITS OWN CELL, AND IT RUNS LAST ON PURPOSE. Its
		// first version was exercised only by the live corpus, so no-opping it left
		// `go test ./...` at exit 0 — "a test for X must fail when X is removed", and
		// it did not. This cell runs after the index is made to AGREE, so a
		// disagreement cannot supply the error and make the cell pass for the wrong
		// reason; the first draft of it did exactly that.
		twoValues := strings.Replace(task, "<ship|withdraw|blocked>", "<ship|withdraw>", 1)
		if err := os.WriteFile(filepath.Join(dir, "T1-fixture.md"), []byte(twoValues), 0o644); err != nil {
			t.Fatal(err)
		}
		short := &recordingTB{}
		checkHumanSignOffs(short, root)
		if short.errors == 0 {
			t.Error("a template offering two of the three outcome words was not reported.\n" +
				"That is the original defect: an operator writes what the template shows them, so a " +
				"template that cannot express the outcome they reached sends it into free text.")
		}
		// ⚠ A FENCE SAYING `withdrawn` DOES NOT OFFER `withdraw`. Without this cell,
		// reverting the word-boundary match to strings.Contains left the whole suite
		// at exit 0 — and the real T3 says "the ADR is withdrawn" in prose, which is
		// the instance that motivated the boundary. AGENTS.md records the same rule
		// from `stale` and "staleness".
		inflected := strings.Replace(task, "<ship|withdraw|blocked>", "<ship|withdrawn|blocked>", 1)
		if err := os.WriteFile(filepath.Join(dir, "T1-fixture.md"), []byte(inflected), 0o644); err != nil {
			t.Fatal(err)
		}
		infl := &recordingTB{}
		checkHumanSignOffs(infl, root)
		if infl.errors == 0 {
			t.Error("a fence offering `withdrawn` was accepted as offering `withdraw`.\n" +
				"A substring check credits an inflection to the word it contains, which is how an " +
				"operator ends up with a template that cannot express the outcome they reached.")
		}

		// ⚠ AND A SECTION WITH NO FENCE AT ALL must be reported, not skipped. The
		// vocabulary check reads the fence; with no fence there is nothing to read,
		// and no-opping that guard left the suite green.
		fenceStart := strings.Index(task, "```text")
		fenceEnd := strings.Index(task[fenceStart:], "```\n") + fenceStart + len("```\n")
		fenceless := task[:fenceStart] + task[fenceEnd:] // fence removed, sign-off kept
		if err := os.WriteFile(filepath.Join(dir, "T1-fixture.md"), []byte(fenceless), 0o644); err != nil {
			t.Fatal(err)
		}
		nofence := &recordingTB{}
		checkHumanSignOffs(nofence, root)
		if nofence.errors == 0 {
			t.Error("a human-observed task whose Acceptance section holds no fenced command was " +
				"not reported.\nThe fence is the template; without one there is nothing telling an " +
				"operator what to write.")
		}

		if err := os.WriteFile(filepath.Join(dir, "T1-fixture.md"), []byte(task), 0o644); err != nil {
			t.Fatal(err)
		}

	})
}

// fencedIn returns only the fenced code blocks of a section, joined. It is what an
// operator COPIES, as opposed to the prose explaining it — and prose is exactly
// what let two earlier versions of the vocabulary check pass over a template that
// had lost a value.
func fencedIn(section string) string {
	var out []string
	for _, m := range regexp.MustCompile("(?s)```[a-zA-Z]*\n(.*?)```").FindAllStringSubmatch(section, -1) {
		out = append(out, m[1])
	}
	return strings.Join(out, "\n")
}

// sectionOf returns the body of one `## <name>` section, so a check about what a
// TEMPLATE offers cannot be satisfied by prose elsewhere in the file.
func sectionOf(text, name string) string {
	head := regexp.MustCompile(`(?m)^##+\s+` + regexp.QuoteMeta(name) + `\s*$`)
	loc := head.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	rest := text[loc[1]:]
	// Stop at the next SAME-OR-SHALLOWER heading, not any deeper one: a `###`
	// subsection inside `## Acceptance` belongs to it. No task file has one today —
	// all 94 use a flat `## Acceptance` — so this is correctness ahead of an
	// instance rather than after one.
	depth := strings.Count(strings.Fields(text[loc[0]:loc[1]])[0], "#")
	if next := regexp.MustCompile(`(?m)^#{1,` + fmt.Sprint(depth) + `}\s+\S`).FindStringIndex(rest); next != nil {
		return rest[:next[0]]
	}
	return rest
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
