package repohygiene

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// taskTestRow matches one row of a task file's Tests table: a backticked test
// name followed by a backticked repository path. Rows without both are the
// non-test forms the template allows (a human proof, a dash) and are skipped.
var taskTestRow = regexp.MustCompile("^\\| `(Test[A-Za-z0-9_]+)` \\| `([^`]+\\.go)` \\|")

// TestEveryTaskTableTestExists walks every ADR task file's Tests table and
// resolves each `TestName` / `path` pair against the Go source with go/parser.
//
// It exists because the pipeline's own gate did not run here. adr-lint checks
// this exact thing, but it is a plugin binary invoked by hand on the record
// being edited — nothing ran it over the whole corpus in CI — and on 2026-09-05 a
// quality-harness session pointing it at this tree found ADR-049 T1 naming three
// tests that existed nowhere: commit 71455db (ADR-054 T1) had rewritten
// internal/auth/origin_test.go and dropped them without saying so, the rebind
// guard kept serving with no behaviour test for a week, and the task's fence
// stayed green because its -run alternation also named four tests that did
// exist. A `-run` pattern over a missing name is not an error to `go test`.
// The spec-side sibling is TestEverySpecBindingNamesATestThatExists; this is
// the same check over the other place a test name is written down as proof.
func TestEveryTaskTableTestExists(t *testing.T) {
	root := repoRoot(t)
	tasks, err := filepath.Glob(filepath.Join(root, "docs", "adr", "ADR-*", "tasks", "T*.md"))
	if err != nil {
		t.Fatalf("glob tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("no task files under docs/adr/*/tasks — the universe moved, not emptied")
	}
	declared := map[string]map[string]bool{}
	testsIn := func(path string) (map[string]bool, error) {
		if got, ok := declared[path]; ok {
			return got, nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil, err
		}
		names := map[string]bool{}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				names[fn.Name.Name] = true
			}
		}
		declared[path] = names
		return names, nil
	}
	rows, bad := 0, []string{}
	for _, task := range tasks {
		n, findings := unresolvedTaskTests(task, root, testsIn)
		rows += n
		bad = append(bad, findings...)
	}
	if rows == 0 {
		t.Fatal("no Tests-table rows parsed across every task file; the row pattern no longer matches the template")
	}
	t.Logf("resolved %d task-table test rows across %d task files", rows, len(tasks))
	for _, b := range bad {
		t.Error(b)
	}
	// Falsifiability, INSIDE the fence: the same function over a fixture that is
	// broken, because a corpus with zero offenders cannot exercise the branch.
	t.Run("a task row naming a missing test is caught", func(t *testing.T) {
		dir := t.TempDir()
		src := "package x\n\nimport \"testing\"\n\nfunc TestRealOne(t *testing.T) {}\n"
		if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		task := filepath.Join(dir, "T1.md")
		body := "## Verification Log\n\n- 2026-09-05 · abc1234 · exit 0 · `x` · acceptance-sha256:0\n\n" +
			"## Tests\n\n| Test name | File | Verifies | Covers | Steps |\n|---|---|---|---|---|\n" +
			"| `TestRealOne` | `sample_test.go` | ok | — | S1 |\n" +
			"| `TestRenamedAway` | `sample_test.go` | gone | — | S1 |\n" +
			"| `TestNoSuchFile` | `absent_test.go` | gone | — | S1 |\n"
		if err := os.WriteFile(task, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		n, findings := unresolvedTaskTests(task, dir, testsIn)
		if n != 3 || len(findings) != 2 {
			t.Fatalf("rows=%d findings=%v; want 3 rows and exactly the two broken ones reported", n, findings)
		}
		// And a task with no passing evidence is not held to its table at all.
		pending := filepath.Join(dir, "T2.md")
		if err := os.WriteFile(pending, []byte(strings.Replace(body, "· exit 0 ·", "· exit 1 ·", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if n, findings := unresolvedTaskTests(pending, dir, testsIn); n != 0 || len(findings) != 0 {
			t.Fatalf("a pending task was held to its table: rows=%d findings=%v", n, findings)
		}
	})
}

// unresolvedTaskTests parses one task file's Tests rows and returns how many it
// read and a finding per row whose file lacks the named func.
func unresolvedTaskTests(task, root string, testsIn func(string) (map[string]bool, error)) (int, []string) {
	raw, err := os.ReadFile(task)
	if err != nil {
		return 0, []string{fmt.Sprintf("%s: %v", task, err)}
	}
	rows, out := 0, []string{}
	rel, _ := filepath.Rel(root, task)
	// Only a task that CLAIMS a passing run is held to its table: a pending task
	// names the tests it will write, and that is the template working, not a
	// finding. The claim is a tool-written `· exit 0 ·` row in its Verification
	// Log — the same evidence adr-lint keys on.
	if !strings.Contains(string(raw), "· exit 0 ·") {
		return 0, nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		m := taskTestRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rows++
		names, err := testsIn(filepath.Join(root, m[2]))
		if err != nil {
			out = append(out, fmt.Sprintf("%s names %s in %s, and that file cannot be read: %v", rel, m[1], m[2], err))
			continue
		}
		if !names[m[1]] {
			out = append(out, fmt.Sprintf("%s names %s in %s, which declares no such func — the row describes a test no acceptance run can have exercised", rel, m[1], m[2]))
		}
	}
	return rows, out
}
