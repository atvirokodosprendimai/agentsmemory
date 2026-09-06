package repohygiene

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// governsLine matches the header a record uses to name the code it decides.
var governsLine = regexp.MustCompile(`(?m)^\*\*Governs:\*\*[ \t]*(.*)$`)

// governsNone is the established spelling for a record whose scope lives in its
// tasks rather than in a path list. Matched case-insensitively on the first word
// only, because the tail is prose and four records already vary it in whitespace.
var governsNone = regexp.MustCompile(`(?i)^none\b`)

// recordStatus reads the record's own Status header. A record is a work order
// only once it is Accepted, and that distinction decides what an unresolvable
// path MEANS rather than merely how loudly to report it.
var recordStatus = regexp.MustCompile(`(?m)^\*\*Status:\*\*\s*(\w+)`)

// governsPaths returns the paths one record's Governs header names.
//
// It accepts BOTH spellings, and that is not tolerance for its own sake: of the
// 24 records carrying the header, 11 wrap their paths in backticks and 13 do not.
// A parser that read only the backticked form — which is how the shape was first
// described — would silently skip more than half the corpus and report a clean
// tree, which is this repository's own "universe drawn from the covered set"
// defect (#300) arriving in a new file.
//
// The second return says the header was PRESENT. Absent and empty are different
// states: a record with no header has not made a claim, while a header with
// nothing after it promises a pointer and delivers none. Only the caller can tell
// them apart, so this does not collapse them.
func governsPaths(text string) (paths []string, present bool) {
	loc := governsLine.FindStringSubmatchIndex(text)
	if loc == nil {
		return nil, false
	}
	body := strings.TrimSpace(text[loc[2]:loc[3]])

	// ⚠ AN EMPTY HEADER LINE IS NOT AN EMPTY HEADER. Two records declare their
	// paths in the adrkit TYPED BLOCK that follows the header:
	//
	//	**Governs:**
	//	- type: path
	//	  pattern: "internal/billing/**"
	//
	// The first version of this parser read the line only, concluded those records
	// governed nothing, and a two-line "fix" wrote `None` above four live
	// declarations that `adr-context` still resolves. Caught in review of #307. The
	// line and the block are one header, and reading half of it is the same
	// universe hole this gate was written against.
	if body == "" {
		return typedGovernsPaths(text[loc[1]:]), true
	}
	if governsNone.MatchString(body) {
		return nil, true
	}
	for _, raw := range strings.Split(body, ",") {
		p := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "`"))
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, true
}

// governsPattern matches one `pattern: "…"` line of the adrkit typed block.
var governsPattern = regexp.MustCompile(`(?m)^\s*pattern:\s*"([^"]+)"\s*$`)

// typedGovernsPaths reads the `- type: path` block that follows an empty Governs
// header, stopping at the first line that is neither part of the list nor blank.
//
// Bounded deliberately: the block is followed by an HTML comment carrying the
// class definition, and a reader that ran to the end of the document would take
// patterns out of prose that only discusses them.
func typedGovernsPaths(rest string) []string {
	var out []string
	for _, line := range strings.Split(rest, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "- type:") || strings.HasPrefix(t, "pattern:") {
			if m := governsPattern.FindStringSubmatch(line); m != nil {
				out = append(out, m[1])
			}
			continue
		}
		break
	}
	return out
}

// inlineGoverns returns the text on the Governs header LINE, which is where the
// None sentinel lives. Separated from governsPaths because the two questions
// differ: what does this record govern, and did it say so on the line or in the
// block beneath it.
func inlineGoverns(text string) string {
	m := governsLine.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// unresolvedGoverns returns every path that names nothing in the tree.
//
// Three forms are legal and all three are in use today:
//
//	internal/palace/service.go                  a literal file
//	clients/claude-code/hooks/**                a directory, which must be NON-EMPTY
//	db/migrations/*_drawers_content_key.sql     a glob, which must match at least one file
//
// The `/**` form resolves as "the directory exists and holds something" rather
// than as a literal path, because that is what the record means by it: a rename
// of the directory un-governs the corpus, an empty one means the code moved out.
//
// It returns offenders instead of asserting on them so the falsifiability subtest
// can drive the SAME function over a fixture that IS broken. A negative case that
// reimplements the resolution pins nothing — severing the real call site would
// leave it green, which this corpus has recorded twice
// (TestASpecBindingThatNamesNothingIsCaught, TestAHumanObservedSignOffAgreesWithTheIndex).
func unresolvedGoverns(root string, paths []string) []string {
	var bad []string
	for _, p := range paths {
		// ONE call covers all three forms, and that is a measured simplification
		// rather than a guess. A `/**` suffix had its own branch here — read the
		// directory, fail when it is missing or empty — and a mutant disabling that
		// branch SURVIVED, because filepath.Glob already answers identically:
		//
		//	full dir   Glob(dir/**)=1  ReadDir=1
		//	empty dir  Glob(dir/**)=0  ReadDir=0
		//	missing    Glob(dir/**)=0  ReadDir errors
		//
		// A branch nothing can distinguish from the code beside it is a branch that
		// will drift untested, so it is gone. A literal path with no metacharacters
		// matches itself when it exists, so the same call serves that case too.
		matches, err := filepath.Glob(filepath.Join(root, p))
		if err != nil || len(matches) == 0 {
			bad = append(bad, p)
		}
	}
	return bad
}

// TestEveryGovernsPathResolves closes the third pointer class in this corpus.
//
// An ADR id cited in Go is gated by TestEveryCitedADRResolves, one cited in docs
// by TestEveryCitedADRResolvesInDocsToo, and a spec binding by
// TestEverySpecBindingNamesATestThatExists. A `Governs:` path — the route from a
// decision to the code it decides — was gated by nothing: adr-lint validates the
// header's SHAPE and never opens the paths.
//
// The failure is silent and it is not hypothetical. An adopter of this lifecycle
// reported a directory move un-governing their entire corpus: seven records naming
// paths that no longer existed, their context reader answering "none governs", and
// the lint green for two days. `git mv` reports success and no suite goes red. We
// have already been bitten by the same class through .gitattributes (#163).
//
// This corpus resolves clean today, so this is a gate against recurrence rather
// than a cleanup — the footing TestNoDocCitesItsOwnLineNumbers was added on. It
// answers ONE question, deliberately: does the path resolve. Whether it is the
// RIGHT path is a judgement, and internal/doclint's own comment records why a
// noisy gate does not survive.
func TestEveryGovernsPathResolves(t *testing.T) {
	root := repoRoot(t)
	records, err := filepath.Glob(filepath.Join(root, "docs", "adr", "ADR-*.md"))
	if err != nil {
		t.Fatalf("glob records: %v", err)
	}
	// ⚠ A floor, not a nicety. "Zero offenders" and "the walk found nothing" are
	// the same green, and this session recorded a monitor that was armed, running
	// and structurally unable to fire for exactly that reason. A corpus this size
	// having fewer than ten records means the layout moved and the gate failed to
	// look.
	if len(records) < 10 {
		t.Fatalf("found %d ADR records — the corpus layout moved, so every check below "+
			"is vacuous rather than clean", len(records))
	}

	withHeader, pathsSeen, forward := 0, 0, 0
	for _, path := range records {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		paths, present := governsPaths(string(body))
		if !present {
			continue // a record that makes no claim is not this gate's business
		}
		withHeader++
		pathsSeen += len(paths)

		// ⚠ A PROPOSED RECORD MAY NAME A PATH ITS TASKS WILL CREATE, and refusing
		// that would be refusing the corpus's own workflow. ADR-043 (Proposed)
		// governs internal/repohygiene/entryroom_test.go, and its T1 lists that file
		// as `add` — the record declares what it WILL govern, which is the whole
		// point of writing the record before the code. Holding it to the tree as it
		// stands today would force an author to either omit the pointer or land the
		// file first, and `adr-next` already draws this exact line: a record is a
		// work order only once it is Accepted.
		//
		// An ACCEPTED record is a different claim. Its paths are the route to code
		// that exists, and one that resolves to nothing is the silent rot this gate
		// was written for.
		status := ""
		if m := recordStatus.FindStringSubmatch(string(body)); m != nil {
			status = strings.ToLower(m[1])
		}
		if status != "accepted" {
			forward += len(unresolvedGoverns(root, paths))
			continue
		}

		// ⚠ "EMPTY" MEANS NO PATHS AND NO SENTINEL — not an empty header LINE.
		// The first version of this check read the line alone and reported ADR-042
		// as empty while its typed block declared two live paths. That is the same
		// misreading that produced the two-line "fix" review rejected, surviving
		// inside the gate written to prevent it.
		if len(paths) == 0 && !governsNone.MatchString(strings.TrimSpace(inlineGoverns(string(body)))) {
			t.Errorf("%s: the Governs header names nothing — no path, no typed block, and not "+
				"the `None — declared by its tasks` sentinel.\n"+
				"  It promises the route from this decision to the code it decides and gives "+
				"none, which is this gate's subject in its most complete form.", rel)
		}
		for _, bad := range unresolvedGoverns(root, paths) {
			t.Errorf("%s: Governs names %q, which resolves to nothing.\n"+
				"  A reader following this record to its code lands nowhere, and `adr-lint` "+
				"cannot see it — it checks the header's shape and never opens the path. Update "+
				"the record, or the move that broke it is silent.", rel, bad)
		}
	}

	if withHeader < 10 || pathsSeen < 20 {
		t.Errorf("examined %d records with a Governs header and %d paths — too few for this "+
			"corpus, so the parser stopped matching rather than the tree being clean",
			withHeader, pathsSeen)
	}
	t.Logf("resolved %d Governs paths across %d records; %d forward declaration(s) in "+
		"records that are not yet Accepted", pathsSeen, withHeader, forward)

	// A corpus with zero unresolvable paths cannot exercise the branch that reports
	// one, so the negative case is a SUBTEST driving the same resolver over inputs
	// that ARE broken — inside the fence, because the acceptance runs one test name.
	t.Run("an unresolvable path is caught", func(t *testing.T) {
		got := unresolvedGoverns(root, []string{
			"internal/palace/service.go",              // exists
			"clients/claude-code/hooks/**",            // non-empty directory
			"db/migrations/*_drawers_content_key.sql", // glob with a match
			"internal/palace/no_such_file.go",         // gone
			"internal/no_such_dir/**",                 // gone
			"db/migrations/*_no_such_migration.sql",   // matches nothing
		})
		if len(got) != 3 {
			t.Fatalf("the resolver reported %v over three real and three broken paths; "+
				"a gate that cannot see an offender cannot report a clean corpus either", got)
		}
	})

	// Both spellings, because 13 of 24 records omit the backticks and a parser that
	// read one form would skip them silently while reporting success.
	t.Run("both spellings parse", func(t *testing.T) {
		for _, tc := range []struct{ name, body string }{
			{"backticked", "**Governs:** `a/b.go`, `c/d.go`\n"},
			{"bare", "**Governs:** a/b.go, c/d.go\n"},
		} {
			paths, present := governsPaths(tc.body)
			if !present || len(paths) != 2 || paths[0] != "a/b.go" || paths[1] != "c/d.go" {
				t.Errorf("%s: parsed %v (present=%v), want [a/b.go c/d.go]", tc.name, paths, present)
			}
		}
		if paths, present := governsPaths("**Governs:** None — declared by its tasks\n"); !present || len(paths) != 0 {
			t.Errorf("the None sentinel parsed as %v; it must be present with no paths", paths)
		}
		if _, present := governsPaths("# a record with no header\n"); present {
			t.Error("a record with no Governs header reported present — absent and empty must stay distinct")
		}
	})
}
