package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// originExport matches the export a searching hook must carry.
var originExport = regexp.MustCompile(`AGENTSMEMORY_ORIGIN=["']?hook:`)

// TestEveryRecallHookDeclaresItsOrigin is ADR-054 T2's gate over the hooks
// directory: every shipped script that performs a search must first export
// AGENTSMEMORY_ORIGIN=hook:<its basename>, so the row the palace writes says a
// hook asked and the to-write list never carries its query. The universe is the
// DIRECTORY, not a list kept beside it: a hook added tomorrow is asked the same
// question on the same commit, and one that stops searching leaves the check
// without an edit.
func TestEveryRecallHookDeclaresItsOrigin(t *testing.T) {
	scripts, err := filepath.Glob(filepath.Join("hooks", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	searching := 0
	for _, path := range scripts {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := string(src)
		if !strings.Contains(body, "mcp search") {
			continue
		}
		searching++
		// The shipped form is `export AGENTSMEMORY_ORIGIN="hook:$(basename "$0")"`;
		// the quote is optional so a hook written without one still passes.
		if !originExport.MatchString(body) {
			t.Errorf("%s performs a search and never exports AGENTSMEMORY_ORIGIN=hook:<name>; its recalls "+
				"will be recorded as a person's and reach am_recall_stats' to-write list", filepath.Base(path))
		}
	}
	// A universe of zero is a gate that cannot fail: two shipped hooks search
	// today, and zero means the directory or the pattern moved under this test.
	if searching == 0 {
		t.Fatal("no hook under hooks/ contains `mcp search` — the universe is empty and this check proved nothing")
	}
}
