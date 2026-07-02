package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testImport = "@agentsmemory-bootstrap.md"

// TestEnsureMemoryImportFreshFile: a fresh install has no CLAUDE.md; the merge must
// create it containing exactly the managed block.
func TestEnsureMemoryImportFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")

	changed, err := ensureMemoryImport(path, testImport)
	if err != nil {
		t.Fatalf("ensureMemoryImport: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true on a fresh file")
	}
	got := readFile(t, path)
	for _, want := range []string{memBeginMarker, testImport, memEndMarker} {
		if !strings.Contains(got, want) {
			t.Errorf("CLAUDE.md missing %q\n---\n%s", want, got)
		}
	}
}

// TestEnsureMemoryImportIdempotent: re-running the installer must not duplicate the
// block and must report no change.
func TestEnsureMemoryImportIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")

	if _, err := ensureMemoryImport(path, testImport); err != nil {
		t.Fatalf("first ensureMemoryImport: %v", err)
	}
	changed, err := ensureMemoryImport(path, testImport)
	if err != nil {
		t.Fatalf("second ensureMemoryImport: %v", err)
	}
	if changed {
		t.Fatal("changed = true on second run, want false (already present)")
	}
	if n := strings.Count(readFile(t, path), memBeginMarker); n != 1 {
		t.Fatalf("BEGIN marker count = %d, want 1 (no duplicate block)", n)
	}
}

// TestEnsureMemoryImportPreservesExisting: a user's hand-written CLAUDE.md must
// survive — the block is appended, the original content is intact, and a
// timestamped backup of the original bytes is written.
func TestEnsureMemoryImportPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	original := "# My rules\n\n- Always write tests.\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := ensureMemoryImport(path, testImport)
	if err != nil {
		t.Fatalf("ensureMemoryImport: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}

	got := readFile(t, path)
	if !strings.HasPrefix(got, original) {
		t.Errorf("existing content not preserved at the top:\n%s", got)
	}
	if !strings.Contains(got, testImport) {
		t.Error("import line not appended")
	}

	// A backup of the original bytes must exist and match exactly.
	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) == 0 {
		t.Fatal("no timestamped backup written")
	}
	if b := readFile(t, backups[0]); b != original {
		t.Fatal("backup does not match the original file bytes")
	}
}

// TestEnsureMemoryImportReplacesStaleBlock: when a managed block already exists but
// carries a different import line (e.g. a renamed asset), the merge replaces just
// that block and leaves the user's surrounding content untouched.
func TestEnsureMemoryImportReplacesStaleBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	stale := "# top\n\n" + memBeginMarker + "\n@old-bootstrap.md\n" + memEndMarker + "\n\n# bottom\n"
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := ensureMemoryImport(path, testImport)
	if err != nil {
		t.Fatalf("ensureMemoryImport: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true (stale import replaced)")
	}
	got := readFile(t, path)
	if strings.Contains(got, "@old-bootstrap.md") {
		t.Error("stale import line was not replaced")
	}
	if !strings.Contains(got, testImport) {
		t.Error("new import line missing")
	}
	// The user's surrounding content on both sides must survive.
	if !strings.Contains(got, "# top") || !strings.Contains(got, "# bottom") {
		t.Errorf("surrounding content lost:\n%s", got)
	}
	if n := strings.Count(got, memBeginMarker); n != 1 {
		t.Errorf("BEGIN marker count = %d, want 1", n)
	}
}

// TestEnsureMemoryImportUnbalancedRefuses: a file with a corrupt/half-edited block
// (only one marker) must fail loudly and be left byte-for-byte untouched, never
// half-rewritten — the same stance ensureStopHook takes on malformed JSON.
func TestEnsureMemoryImportUnbalancedRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	broken := "# rules\n\n" + memBeginMarker + "\n@agentsmemory-bootstrap.md\n(no end marker)\n"
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureMemoryImport(path, testImport); err == nil {
		t.Fatal("ensureMemoryImport accepted an unbalanced block, want an error")
	}
	if got := readFile(t, path); got != broken {
		t.Fatal("file with unbalanced markers was modified; it must be left untouched")
	}
}

// readFile is a tiny test helper that reads a file as a string, failing the test on
// any read error.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
