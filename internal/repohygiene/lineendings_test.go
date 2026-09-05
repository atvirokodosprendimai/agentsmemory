package repohygiene

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// TestSourceFilesAreCheckedOutWithLFEndings asks GIT what the effective `eol`
// attribute is, rather than reading the files or trusting .gitattributes to say
// what it means.
//
// That distinction is the whole point. This gate cannot observe its own failure
// mode: on a POSIX host every source file is LF whatever the attributes say, so a
// check that read the bytes would pass on Linux and macOS while the property it
// claims to hold was absent — and the hosts where it matters are exactly the ones
// that never run it. `git check-attr` reports what a CHECKOUT would do, and it
// answers the same on every platform.
//
// The class this closes (#163): with core.autocrlf=true, Git for Windows' default,
// and no .gitattributes, a Windows working tree carries CRLF Go sources. This
// repository's gates read their own source text — internal/doclint,
// internal/archguard, the reachability checks in internal/mcpserver and cmd/server
// — and their matchers hard-code "\n". Measured there:
//
//	--- FAIL: TestSearchResultRendersTheCorrectionMark
//	    recallanswers_reach_test.go:95: could not bound the searchHitView literal
//
// over a literal that was present and correct. One gate was failing; the class is
// every gate that reads source, and patching them one matcher at a time would
// leave the next one to be found the same way.
//
// Shell scripts carry a sharper consequence than a false alarm: a CRLF shebang
// makes the interpreter path end in \r, so the script fails to execute naming a
// binary nobody can find.
func TestSourceFilesAreCheckedOutWithLFEndings(t *testing.T) {
	root := repoRoot(t)

	// A representative file per extension that a gate reads or an interpreter
	// runs. Named rather than swept, because the claim is about the ATTRIBUTE
	// rule, and one file per rule proves the rule applies; sweeping thousands
	// would prove the same thing slower.
	probes := []string{
		"internal/mcpserver/drawers.go",
		"clients/claude-code/hooks/agentsmemory-recall-hook.sh",
		"docker-compose.yml",
	}

	for _, rel := range probes {
		out, err := testexec.Command(t, "git", "-C", root, "check-attr", "eol", "--", rel).Output()
		if err != nil {
			t.Fatalf("git check-attr for %s: %v", rel, err)
		}
		// "<path>: eol: lf" — the value is the last field.
		line := strings.TrimSpace(string(out))
		fields := strings.Fields(line)
		got := ""
		if len(fields) > 0 {
			got = fields[len(fields)-1]
		}
		if got != "lf" {
			t.Errorf("git reports eol=%q for %s, want \"lf\".\n"+
				"  Unset means a Windows checkout with core.autocrlf=true materialises CRLF, and "+
				"every gate in this repository that reads source text hard-codes \"\\n\" — they "+
				"false-alarm there on properties that hold (#163). A .sh with CRLF is worse: the "+
				"shebang gains a \\r and the script will not run at all.", got, rel)
		}
	}
}
