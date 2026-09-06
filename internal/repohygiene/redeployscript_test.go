package repohygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// redeployStages is the ordered list of stages the script announces.
//
// ⚠ A KEPT LIST, DELIBERATELY, AND THIS REPOSITORY USUALLY REJECTS THOSE. The
// argument for one here is that the failure being caught is a stage DISAPPEARING,
// and emptiness cannot see it: when #330's hunk deleted the smoke banner the two
// sections MERGED, so the surviving output sat under the previous heading and
// every "is there output under this heading" predicate stayed green. Review of PR
// #331 re-applied that exact deletion and watched checkNoEmptySections pass.
//
// The list is small and the stages are fixed, so maintaining it when the pipeline
// changes is a deliberate edit rather than a chore — which is the honest trade,
// and the reason the usual objection does not apply.
var redeployStages = []string{
	"compose chain:",
	"tests must pass before anything is built",
	"build",
	"restart",
	"wait for health",
	"version: the running server must name the stamp it was built with",
	"read the ARTIFACT that is serving, not the build log",
	"digest: the running binary against the image just built",
	"what the running server resolved",
	"smoke: one real search through the endpoint agents call",
	"otel: the smoke search must have left a trace",
	"the installed client kit, against this checkout",
	"deployed and verified",
}

// checkStageSequence fails when a stage the script announces goes missing or
// moves.
func checkStageSequence(tb testing.TB, root string) {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "redeploy.sh"))
	if err != nil {
		tb.Fatalf("read the redeploy script: %v", err)
	}
	var got []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, `echo "==> `) {
			continue
		}
		got = append(got, strings.TrimPrefix(line, `echo "==> `))
	}
	if len(got) != len(redeployStages) {
		tb.Errorf("the script announces %d stages; this list names %d. A stage that disappears "+
			"takes its heading with it, so the output of the NEXT one is filed under the "+
			"PREVIOUS heading and every emptiness check stays green — which is how #330's "+
			"deletion shipped.", len(got), len(redeployStages))
	}
	for i, want := range redeployStages {
		if i >= len(got) {
			tb.Errorf("stage %d (%q) is gone", i+1, want)
			continue
		}
		if !strings.HasPrefix(got[i], want) {
			tb.Errorf("stage %d is %q; expected it to start %q. Either a stage was removed or "+
				"they were reordered; both change what an operator reads under each banner.",
				i+1, strings.TrimSuffix(got[i], `"`), want)
		}
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

// writeRedeployFixture renders a script whose `==>` headings are exactly the
// stages named, each with a line under it, and returns a root to check.
//
// The LAST stage deliberately gets nothing under it, because that is the shape
// of the real script: its final heading is the verdict rather than a section,
// and checkNoEmptySections exempts that position on purpose. A fixture builder
// that filled it would quietly stop exercising the exemption.
func writeRedeployFixture(t *testing.T, stages []string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	for i, stage := range stages {
		b.WriteString(`echo "==> ` + stage + "\"\n")
		if i < len(stages)-1 {
			b.WriteString("docker compose ps\n")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "redeploy.sh"),
		[]byte(b.String()), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

// checkShadowWarning requires redeploy.sh's kit check to resolve a symlink hop
// and to warn when the remedy directory is shadowed.
//
// BACKLOG "redeploy.sh's kit check reads a different binary than its own remedy
// writes" (2026-09-03) recorded the defect: the check resolves the kit with
// `command -v`, which is PATH-first, while the remedy it prints writes to
// $AIAGENTMEMORY_BIN_DIR. When a second copy shadows that directory the check
// reads one file and the fix writes another, so following the advice changes
// nothing and the verdict never moves. f80e12c fixed it a day later and nothing
// was left holding the fix in place.
//
// ⚠ THE SYMLINK HOP IS HALF THE MECHANISM, NOT A DETAIL. The sanctioned layout
// on this project's own machine is ~/.claude/bin/aiagentmemory symlinked into
// the remedy directory; without the readlink the warning fires on every correct
// install, which is issue #204 — a warning that cried wolf until the file it
// named was not the problem. So both halves are asserted: the comparison must
// exist AND the link must be resolved before it.
func checkShadowWarning(tb testing.TB, root string) {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "redeploy.sh"))
	if err != nil {
		tb.Fatalf("read redeploy.sh: %v", err)
	}
	// ⚠ CODE, NOT WORDS. The first draft of this gate matched the bare strings
	// "SHADOWED" and "readlink", and a mutant that replaced the readlink CALL
	// left it green — because the word also appears in the comment three lines
	// above the code. A gate whose universe includes the prose explaining the
	// mechanism cannot see the mechanism leave.
	var code []string
	for _, line := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(line); !strings.HasPrefix(t, "#") {
			code = append(code, line)
		}
	}
	body := strings.Join(code, "\n")
	for _, want := range []struct{ needle, why string }{
		{`which is SHADOWED.`, "the kit check no longer warns when the remedy directory is " +
			"shadowed, so `command -v` can read one binary while the printed Fix writes another " +
			"and the verdict never moves however often an operator runs it"},
		{`readlink "$bin_path"`, "the shadow comparison no longer resolves a symlink hop, so the " +
			"sanctioned layout (a link into the remedy directory) is reported as a shadow on " +
			"every correct install — issue #204, a warning that fires until nobody reads it"},
	} {
		if !strings.Contains(body, want.needle) {
			tb.Errorf("scripts/redeploy.sh no longer contains %s outside its comments: %s",
				want.needle, want.why)
		}
	}
}

// checkNeedlePreflight requires the script to reject a needle that is in no Go
// string literal, and to do it without a pipefail-poisoned pipeline.
//
// BACKLOG "A needle that is an identifier proves nothing, and a piped exit code
// hides the refusal" (2026-09-03) asked for exactly this: a bad needle should be
// caught as a bad needle rather than reported as a bad deploy. Identifiers are
// not in a compiled binary — only string literals are.
//
// ⚠ BOTH HALVES OF THIS GUARD WERE GOT WRONG WHILE WRITING IT, each reproducing
// the defect it was written for. The first grepped the .go SOURCE, which matches
// an identifier — the one thing that cannot be in the binary — so it admitted
// `evalPromptAbsent` and refused `SocketAuthority` only because that name is
// absent from the tree entirely. The second piped the extracted literals into
// `grep -q`, and under `set -o pipefail` the early exit gives the upstream grep
// SIGPIPE (141), so a needle present in four literals was refused. That is this
// entry's own second half, committed inside the fix for its first.
func checkNeedlePreflight(tb testing.TB, root string) {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "redeploy.sh"))
	if err != nil {
		tb.Fatalf("read redeploy.sh: %v", err)
	}
	var code []string
	for _, line := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(line); !strings.HasPrefix(t, "#") {
			code = append(code, line)
		}
	}
	body := strings.Join(code, "\n")
	if !strings.Contains(body, "grep -rhoE") {
		tb.Errorf("scripts/redeploy.sh no longer extracts Go string literals before checking a " +
			"needle. A plain grep over the source matches an IDENTIFIER, which is never in a " +
			"compiled binary, so a bad needle would again be reported as a bad deploy.")
	}
	if strings.Contains(body, `| grep -qF -- "$n"`) {
		tb.Errorf("the needle check pipes into `grep -q` again. Under `set -o pipefail` the early " +
			"exit gives the upstream grep SIGPIPE and the pipeline reports failure, so a needle " +
			"that IS present reads as absent and a correct deploy is refused.")
	}
	if !strings.Contains(body, "REDEPLOY_SKIP_NEEDLE_CHECK") {
		tb.Errorf("the needle check has no escape hatch. A literal built by concatenation is a " +
			"real false alarm, and a guard with no way past it is one somebody deletes.")
	}
}

// TestTheRedeployPathKeepsItsWindowsFixes is the gate.
func TestTheRedeployPathKeepsItsWindowsFixes(t *testing.T) {
	root := repoRoot(t)
	checkShadowWarning(t, root)
	checkNeedlePreflight(t, root)
	checkRedeployScript(t, root)
	checkNoEmptySections(t, root)
	checkStageSequence(t, root)
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

	t.Run("a needle check that greps the source or pipes into grep -q is caught", func(t *testing.T) {
		write := func(t *testing.T, body string) string {
			t.Helper()
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o750); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "scripts", "redeploy.sh"),
				[]byte(body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			return dir
		}
		const hatch = "REDEPLOY_SKIP_NEEDLE_CHECK=\n"
		const extract = "found=$(grep -rhoE --include='*.go' 'x' . | grep -cF -- \"$n\" || true)\n"

		// Greps the SOURCE rather than its literals: the identifier hole, which
		// admits a Go constant name that can never be in a compiled binary.
		rec := &recordingTB{}
		checkNeedlePreflight(rec, write(t, "#!/usr/bin/env bash\ngrep -rqF -- \"$n\" .\n"+hatch))
		if rec.errors != 1 {
			t.Errorf("reported %d finding(s) over a check that greps the source rather than its "+
				"string literals", rec.errors)
		}

		// Pipes into grep -q: under pipefail the SIGPIPE makes a present needle
		// read as absent, refusing a correct deploy.
		rec2 := &recordingTB{}
		checkNeedlePreflight(rec2, write(t, "#!/usr/bin/env bash\n"+
			"grep -rhoE --include='*.go' 'x' . | grep -qF -- \"$n\"\n"+hatch))
		if rec2.errors != 1 {
			t.Errorf("reported %d finding(s) over a check piping into grep -q under pipefail",
				rec2.errors)
		}

		// No escape hatch: a literal built by concatenation is a real false alarm.
		rec3 := &recordingTB{}
		checkNeedlePreflight(rec3, write(t, "#!/usr/bin/env bash\n"+extract))
		if rec3.errors != 1 {
			t.Errorf("reported %d finding(s) over a check with no escape hatch", rec3.errors)
		}

		quiet := &recordingTB{}
		checkNeedlePreflight(quiet, write(t, "#!/usr/bin/env bash\n"+extract+hatch))
		if quiet.errors != 0 {
			t.Errorf("reported %d finding(s) over a correct check; a gate that cannot pass is one "+
				"somebody deletes", quiet.errors)
		}
	})

	t.Run("a kit check without the shadow warning is caught", func(t *testing.T) {
		// The real script carries both halves, so the reporting branch cannot run
		// against it. Driven over fixtures that ARE offenders — and one that is
		// not, because a check that flags everything pins nothing either.
		write := func(t *testing.T, body string) string {
			t.Helper()
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o750); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "scripts", "redeploy.sh"),
				[]byte(body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			return dir
		}

		// ⚠ EACH OFFENDER KEEPS THE WORDS IN A COMMENT, which is the shape that
		// defeated this gate's first draft: it matched the bare words, and a
		// mutant replacing the readlink CALL left it green because the comment
		// three lines above still said "readlink".
		noWarn := write(t, `#!/usr/bin/env bash
# one hop of readlink is resolved, and we warn when the dir is SHADOWED.
real_path="$(readlink "$bin_path")"
`)
		rec := &recordingTB{}
		checkShadowWarning(rec, noWarn)
		if rec.errors != 1 {
			t.Errorf("reported %d finding(s) over a script whose SHADOWED warning survives only "+
				"in a comment; that is the substring hole this gate was rewritten for", rec.errors)
		}

		noLink := write(t, `#!/usr/bin/env bash
# a symlink hop is resolved with readlink before comparing.
echo "    ⚠ PATH resolves aiagentmemory to $bin_path; the Fix below writes to $remedy_dir, which is SHADOWED."
`)
		rec2 := &recordingTB{}
		checkShadowWarning(rec2, noLink)
		if rec2.errors != 1 {
			t.Errorf("reported %d finding(s) over a script that warns but never resolves the "+
				"symlink hop — issue #204's every-install false alarm", rec2.errors)
		}

		whole := write(t, `#!/usr/bin/env bash
real_path="$(readlink "$bin_path")"
echo "    ⚠ PATH resolves aiagentmemory to $bin_path; the Fix below writes to $remedy_dir, which is SHADOWED."
`)
		quiet := &recordingTB{}
		checkShadowWarning(quiet, whole)
		if quiet.errors != 0 {
			t.Errorf("reported %d finding(s) over a script carrying both halves; a gate that "+
				"cannot pass is one somebody deletes", quiet.errors)
		}
	})

	t.Run("a heading with nothing under it is caught", func(t *testing.T) {
		// #330's deletion reduced to its shape: a hunk took a heading's body and
		// left the banner. The corpus is intact, so the branch that reports one
		// cannot run against it — without this the check is a name that has never
		// executed the line it exists for.
		empty := t.TempDir()
		if err := os.MkdirAll(filepath.Join(empty, "scripts"), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(empty, "scripts", "redeploy.sh"), []byte(
			`#!/usr/bin/env bash
echo "==> build"
docker build .
echo "==> what the running server resolved"
echo "==> deployed and verified"
`), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		emptyRec := &recordingTB{}
		checkNoEmptySections(emptyRec, empty)
		if emptyRec.errors != 1 {
			t.Errorf("the gate reported %d finding(s) over a script whose middle heading has "+
				"nothing under it; that is the deletion it was written for", emptyRec.errors)
		}

		// And the other direction, which also pins the exemption: the SAME script
		// with the body restored must be silent, final heading and all. A gate that
		// flagged the verdict banner would be one somebody works around.
		whole := writeRedeployFixture(t, []string{
			"build", "what the running server resolved", "deployed and verified",
		})
		wholeRec := &recordingTB{}
		checkNoEmptySections(wholeRec, whole)
		if wholeRec.errors != 0 {
			t.Errorf("the gate reported %d finding(s) over a script where every section produces "+
				"output and only the closing verdict stands alone", wholeRec.errors)
		}
	})

	t.Run("a stage that disappears, moves or is added is caught", func(t *testing.T) {
		// THREE fixtures, because this check has two branches that fail
		// independently and a single fixture pins only one of them. A missing stage
		// trips the count AND the order, so it cannot tell them apart; an appended
		// one trips ONLY the count; a renamed one trips ONLY the order. An earlier
		// draft used the missing case alone, and severing the ordering loop left it
		// green.
		missing := append(append([]string{}, redeployStages[:3]...), redeployStages[4:]...)
		missingRec := &recordingTB{}
		checkStageSequence(missingRec, writeRedeployFixture(t, missing))
		if missingRec.errors < 2 {
			t.Errorf("the gate reported %d finding(s) over a script missing stage 4 (%q); it should "+
				"see both the short count and the stages that shifted up into its place",
				missingRec.errors, redeployStages[3])
		}

		appended := append(append([]string{}, redeployStages...), "a stage nobody declared")
		appendedRec := &recordingTB{}
		checkStageSequence(appendedRec, writeRedeployFixture(t, appended))
		if appendedRec.errors != 1 {
			t.Errorf("the gate reported %d finding(s) over a script announcing one stage more than "+
				"this list names; every declared stage is still in place, so the count is the only "+
				"thing that can see it", appendedRec.errors)
		}

		renamed := append([]string{}, redeployStages...)
		renamed[4] = "hold until the container reports healthy"
		renamedRec := &recordingTB{}
		checkStageSequence(renamedRec, writeRedeployFixture(t, renamed))
		if renamedRec.errors != 1 {
			t.Errorf("the gate reported %d finding(s) over a script whose stage 5 was reworded; the "+
				"count still agrees, so the ordering comparison is the only thing that can see it",
				renamedRec.errors)
		}

		quietRec := &recordingTB{}
		checkStageSequence(quietRec, writeRedeployFixture(t, redeployStages))
		if quietRec.errors != 0 {
			t.Errorf("the gate reported %d finding(s) over a script announcing exactly the stages "+
				"this list names; a gate that cannot pass is one somebody deletes", quietRec.errors)
		}
	})
}

// TestTheRedeployGateIsAppliedToTheTree is the rung the gate cannot reach:
// delete the line that points a check at this repository and its fixtures keep
// passing, with the gate's own name printed as PASS. Demonstrated on a sibling
// gate in review of PR #316.
//
// ⚠ THE UNIVERSE IS DERIVED, AND IT WAS A KEPT LIST UNTIL 2026-09-06 — WHICH WENT
// STALE TWICE IN TWO DAYS. The list named four checks. #343 added
// checkShadowWarning and #344 added checkNeedlePreflight, both with a real-tree
// call, and neither was added to the list. Measured in review of #344:
// commenting out `checkNeedlePreflight(t, root)` left the whole package at exit 0
// — this guard green, and #333's falsifiability gate green too, because the
// recorder-driven subtests still existed and it only asks whether a negative case
// is wired. So the newest check could stop being applied to the real script and
// nothing in the tree would say so, which is verbatim the defect this test's own
// first paragraph describes.
//
// Two consecutive PRs missed the same list, which is the argument: a list kept
// beside the truth goes stale, and this repository's own rules say so. The
// universe is now every predicate DECLARED IN THIS FILE taking (testing.TB,
// string), so a check joins the check on the commit that adds it.
//
// ⚠ THE COST, STATED RATHER THAN HIDDEN: a future top-level helper in this file
// with that exact signature and no business running against the tree would be
// flagged and would need renaming or an exemption. That is a loud, one-line
// problem, and it is the trade taken deliberately over the silent one this
// replaces — today the signature belongs to the six checks and nothing else.
func TestTheRedeployGateIsAppliedToTheTree(t *testing.T) {
	const self = "redeployscript_test.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, self, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", self, err)
	}

	var predicates []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 2 {
			continue
		}
		sel, ok := fn.Type.Params.List[0].Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "TB" {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "testing" {
			continue
		}
		if id, ok := fn.Type.Params.List[1].Type.(*ast.Ident); !ok || id.Name != "string" {
			continue
		}
		predicates = append(predicates, fn.Name.Name)
	}
	// A derived universe that comes back empty is the failure mode a kept list
	// cannot have: it would report a clean run over a file whose checks are all
	// unapplied.
	if len(predicates) == 0 {
		t.Fatalf("%s declares no (testing.TB, string) predicate; either they were renamed or "+
			"this extractor stopped seeing them, and both make the loop below vacuous", self)
	}

	// ⚠ THE SATISFACTION SIDE IS READ FROM THE AST TOO, AND THE FIRST DRAFT OF
	// THIS LOOP WAS A strings.Contains. A commented-out call still contains its
	// own text, so `// checkNeedlePreflight(t, root)` satisfied it and the mutant
	// that removes the call passed — measured while writing this. That is the same
	// hole checkShadowWarning records one screen up, reproduced here by reaching
	// for a substring: a gate whose universe includes the prose cannot see the
	// mechanism leave.
	const rootTest = "TestTheRedeployPathKeepsItsWindowsFixes"
	applied := map[string]bool{}
	found := false
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != rootTest || fn.Body == nil {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			a0, ok0 := call.Args[0].(*ast.Ident)
			a1, ok1 := call.Args[1].(*ast.Ident)
			if ok0 && ok1 && a0.Name == "t" && a1.Name == "root" {
				applied[id.Name] = true
			}
			return true
		})
	}
	if !found {
		t.Fatalf("%s declares no %s; the checks below are applied by nothing and this test "+
			"would report a clean run over that", self, rootTest)
	}

	for _, name := range predicates {
		if !applied[name] {
			t.Errorf("%s never runs %s(t, root) against the repository root; its fixtures would "+
				"still pass and the package would still report PASS over a tree carrying the very "+
				"defect %s exists to catch", rootTest, name, name)
		}
	}
	t.Logf("checked %d predicate(s) against the tree", len(predicates))
}
