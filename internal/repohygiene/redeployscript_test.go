package repohygiene

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Three defects in the redeploy path, all reported from Windows on one day, and
// none of them visible from a Mac or from CI: this repository's own deploys run
// on macOS, so a portability defect in the one script an operator must run is
// found by an operator, once, in the middle of a release.
//
// Each is a SHAPE rather than a behaviour, which is why they are gated here
// rather than by running the script: a bash script cannot be unit-tested into
// telling you that its separator is wrong on a platform the test host is not.

// retiredRedeployShapes are the exact constructs these issues were filed against.
//
// ⚠ MATCHED AS RETIRED CLAUSES, not as topics, following
// TestNoToolDescriptionClaimsALongMemoryCannotBeMoved: the surrounding code must
// stay writable. `IFS` is fine, `curl` is fine, `|| echo` is fine — the
// conflations are not.
var retiredRedeployShapes = []struct {
	pattern *regexp.Regexp
	issue   string
	why     string
}{
	{
		regexp.MustCompile(`IFS=['"]?:,`),
		"#328",
		"splitting the compose chain on ':' AND ',' at once cuts a Windows path at its " +
			"drive letter, so the guard looks for a compose file named 'C' and every " +
			"redeploy on that host is refused. The separator belongs to the chain's SOURCE: " +
			"',' for the container label, the platform's path separator for COMPOSE_FILE",
	},
	{
		regexp.MustCompile(`name="?\$\(basename `),
		"#328 (second half)",
		"basename(1) knows only '/', so on a Windows path it returns the WHOLE string — the " +
			"chain split at ',' correctly and then put the drive letter straight back, moving " +
			"the refusal one line down. `${f##*[/\\\\]}` strips to the last separator of either " +
			"kind, which is the one thing basename cannot do",
	},
	{
		regexp.MustCompile(`case "\$chain" in \*';'\*`),
		"#328 (second half)",
		"reading the separator OFF THE VALUE is right for a Windows chain of two files and " +
			"wrong for a chain of one, where there is no separator to find and the drive letter " +
			"splits again. Compose decides this by platform plus COMPOSE_PATH_SEPARATOR",
	},
	{
		regexp.MustCompile(`%\{http_code\}[\s\S]{0,600}?\|\|\s*echo\s+000\s*\)`),
		"#329",
		"`code=$(curl … || echo 000)` APPENDS on failure, so curl reporting 200 and then " +
			"exiting 23 (a write error) yields `200000` and fails a deploy that succeeded. " +
			"Capture the exit status separately: a body that did not land is a real finding, " +
			"and a different one from 'the endpoint did not answer'",
	},
}

// checkRedeployScript is the verdict, through a substitutable testing.TB.
func checkRedeployScript(tb testing.TB, root string) {
	tb.Helper()
	path := filepath.Join(root, "scripts", "redeploy.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read the redeploy script: %v", err)
	}
	// ⚠ CODE ONLY, NOT COMMENTS, and the first version of this gate flagged the
	// comment that explains the fix. A script must be able to say why a construct
	// was retired — that sentence is the most useful thing in the file for whoever
	// meets the platform next — and a gate forbidding the explanation along with
	// the offence is one somebody deletes. Same shape as the tool-description gate
	// matching a retired CLAUSE while its advice keeps being said.
	var code []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	src := strings.Join(code, "\n")
	if strings.TrimSpace(src) == "" {
		tb.Fatal("the redeploy script has no executable line; this gate would pass over anything")
	}
	for _, shape := range retiredRedeployShapes {
		if loc := shape.pattern.FindString(src); loc != "" {
			tb.Errorf("scripts/redeploy.sh carries the construct issue %s was filed against "+
				"(%q).\n  %s", shape.issue, strings.TrimSpace(loc), shape.why)
		}
	}
}

// checkNoEmptySections requires every `==>` heading the script prints to be
// followed by something that produces output.
//
// ⚠ WRITTEN AFTER I DELETED FOUR LINES AND MERGED IT. A hunk meant to replace the
// smoke capture took the tail of a comment, the `docker logs` line it explained,
// and the next heading with it — so the script printed "what the running server
// resolved" with nothing under it, and filed the smoke result beneath that
// heading. A heading with no output reads as "it resolved nothing", which is a
// worse answer than the heading not being there.
//
// Nothing caught it: `bash -n` is clean because a truncated comment is valid
// bash, and no test read that region. Found by review of PR #330, after merge.
func checkNoEmptySections(tb testing.TB, root string) {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "redeploy.sh"))
	if err != nil {
		tb.Fatalf("read the redeploy script: %v", err)
	}
	heading := regexp.MustCompile(`^echo "==> `)
	lines := strings.Split(string(raw), "\n")
	// ⚠ THE LAST HEADING IS THE VERDICT, NOT A SECTION. `==> deployed and verified`
	// IS the output — there is nothing to print under it — and the first version of
	// this gate flagged it, which would have made the check something to work
	// around on its first run. The cost is stated rather than hidden: a deletion
	// that truncated the script at its final heading looks identical from here, and
	// that position is the one where the heading is the message.
	last := -1
	for i, line := range lines {
		if heading.MatchString(line) {
			last = i
		}
	}
	sections := 0
	for i, line := range lines {
		if !heading.MatchString(line) || i == last {
			continue
		}
		sections++
		produces := false
		for _, next := range lines[i+1:] {
			t := strings.TrimSpace(next)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if heading.MatchString(next) {
				break
			}
			produces = true
			break
		}
		if !produces {
			tb.Errorf("scripts/redeploy.sh:%d prints a heading with nothing under it:\n    %s\n"+
				"  An operator reads the promise and sees a blank, then reads the NEXT step's "+
				"output filed beneath it. That is worse than no heading at all.", i+1, line)
		}
	}
	if sections == 0 {
		tb.Fatal("the script prints no ==> heading at all; this gate is checking nothing")
	}
}

// checkDocumentedClone requires the procedure AGENTS.md prints to survive a
// checkout that fails halfway.
func checkDocumentedClone(tb testing.TB, root string) {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		tb.Fatalf("read AGENTS.md: %v", err)
	}
	src := string(raw)
	clone := regexp.MustCompile(`(?m)^\s*git clone .*--no-local.*$`)
	line := clone.FindString(src)
	if line == "" {
		tb.Fatal("AGENTS.md no longer prints a clone command, so this gate is checking nothing. " +
			"If the redeploy procedure moved, this check moves with it")
	}
	if !strings.Contains(line, "core.longpaths=true") {
		tb.Errorf("the documented clone does not set core.longpaths (issue #327):\n    %s\n"+
			"  On Windows git fails to write files past the filename limit, prints "+
			"\"unable to checkout working tree\", and EXITS 0.", strings.TrimSpace(line))
	}
	if !strings.Contains(src, "status --porcelain") {
		tb.Errorf("the documented procedure never checks that the clone is WHOLE (issue #327). " +
			"The ref guard compares commits, and \"which commit\" and \"is the tree complete\" " +
			"are different questions — a deploy built from a tree missing five files passed " +
			"every guard in the printed procedure.")
	}
}

// TestTheRedeployPathKeepsItsWindowsFixes is the gate.
func TestTheRedeployPathKeepsItsWindowsFixes(t *testing.T) {
	root := repoRoot(t)
	checkRedeployScript(t, root)
	checkNoEmptySections(t, root)
	checkDocumentedClone(t, root)

	t.Run("each retired construct is caught", func(t *testing.T) {
		// The offenders as they were written, so this fails if a matcher is
		// loosened into uselessness or tightened past the real thing.
		fixture := t.TempDir()
		if err := os.MkdirAll(filepath.Join(fixture, "scripts"), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(fixture, "scripts", "redeploy.sh"), []byte(`#!/usr/bin/env bash
IFS=':,' read -r -a chain_parts <<< "$chain"
chain_sep=':'
case "$chain" in *';'*) chain_sep=';' ;; esac
name="$(basename "$f")"
code=$(curl -s -o /tmp/redeploy-smoke.json -w '%{http_code}' -m 60 -X POST "$BASE/mcp" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0"}' || echo 000)
`), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		rec := &recordingTB{}
		checkRedeployScript(rec, fixture)
		if rec.errors != len(retiredRedeployShapes) {
			t.Errorf("the gate reported %d of %d retired constructs over a fixture carrying all "+
				"of them", rec.errors, len(retiredRedeployShapes))
		}

		// And the CURRENT script's shapes must not be flagged, or the fix is what
		// goes red and somebody reverts it.
		clean := t.TempDir()
		if err := os.MkdirAll(filepath.Join(clean, "scripts"), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(clean, "scripts", "redeploy.sh"), []byte(`#!/usr/bin/env bash
IFS="$chain_sep" read -r -a chain_parts <<< "$chain"
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) chain_sep=';' ;; *) chain_sep=':' ;; esac
name="${f##*[/\\]}"
smoke_rc=0
code=$(curl -s -o /tmp/redeploy-smoke.json -w '%{http_code}' -m 60 -X POST "$BASE/mcp" \
  -d '{"jsonrpc":"2.0"}') || smoke_rc=$?
`), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		quiet := &recordingTB{}
		checkRedeployScript(quiet, clean)
		if quiet.errors != 0 {
			t.Errorf("the gate flags the repaired script (%d finding(s)); a gate that goes red on "+
				"the fix is one somebody reverts", quiet.errors)
		}
	})

	t.Run("a clone without the long-path flag is caught", func(t *testing.T) {
		fixture := t.TempDir()
		if err := os.WriteFile(filepath.Join(fixture, "AGENTS.md"), []byte(
			"    git clone -q --no-local --branch <tag> . \"$DIR\"\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		rec := &recordingTB{}
		checkDocumentedClone(rec, fixture)
		if rec.errors != 2 {
			t.Errorf("the gate reported %d finding(s) over a procedure with neither the long-path "+
				"flag nor a wholeness check; both are the issue", rec.errors)
		}
	})
}

// TestTheRedeployGateIsAppliedToTheTree is the rung the gate cannot reach:
// delete the two lines that point it at this repository and its fixtures keep
// passing, with the gate's own name printed as PASS. Demonstrated on a sibling
// gate in review of PR #316.
func TestTheRedeployGateIsAppliedToTheTree(t *testing.T) {
	src, err := os.ReadFile("redeployscript_test.go")
	if err != nil {
		t.Fatalf("read this file: %v", err)
	}
	body := string(src)
	for _, call := range []string{
		"checkRedeployScript(t, root)",
		"checkNoEmptySections(t, root)",
		"checkDocumentedClone(t, root)",
	} {
		if !strings.Contains(body, call) {
			t.Errorf("TestTheRedeployPathKeepsItsWindowsFixes never runs %s against the "+
				"repository root; its fixtures would still pass and the package would still "+
				"report PASS over a tree carrying the retired constructs", call)
		}
	}
}
