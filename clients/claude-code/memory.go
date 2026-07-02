package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Managed-block markers for the CLAUDE.md import. Everything between them is ours
// to write and rewrite; everything outside them is the user's and is preserved
// verbatim. The BEGIN marker carries a "do not edit" note so a human reading the
// file knows the block is installer-managed. The markers are HTML comments so
// they render as nothing in a Markdown viewer.
const (
	memBeginMarker = "<!-- BEGIN agentsmemory (managed — do not edit) -->"
	memEndMarker   = "<!-- END agentsmemory -->"
)

// ensureMemoryImport ensures the Claude memory file at path pulls in importLine
// (e.g. "@agentsmemory-bootstrap.md") inside a managed marker block, idempotently.
// It preserves any existing user content, backs the file up (timestamped) before
// modifying, and never duplicates the block. It returns true if it wrote a change,
// false if the block was already present and current.
//
// This mirrors ensureStopHook's contract for settings.json: an append/merge that
// is safe to re-run and never clobbers a user's hand-written file. We use a
// managed marker block (rather than the whole file) because CLAUDE.md is the
// user's own memory — a global install must add one @import line, not overwrite
// their instructions. Claude Code resolves an @import relative to the importing
// file, so importLine names a sibling of path.
func ensureMemoryImport(path, importLine string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	existing := string(raw)

	// The block we want present: markers wrapping exactly the import line.
	block := memBeginMarker + "\n" + importLine + "\n" + memEndMarker

	beginIdx := strings.Index(existing, memBeginMarker)
	endIdx := strings.Index(existing, memEndMarker)

	var next string
	switch {
	case beginIdx >= 0 && endIdx > beginIdx:
		// A well-formed managed block already exists: replace it in place so an
		// updated importLine (or marker text) is picked up, leaving the user's
		// surrounding content untouched.
		suffixStart := endIdx + len(memEndMarker)
		next = existing[:beginIdx] + block + existing[suffixStart:]
	case beginIdx >= 0 || endIdx >= 0:
		// Exactly one marker, or an END before a BEGIN: the block is corrupt or was
		// hand-edited. Refuse to guess where our region is — overwriting the wrong
		// span would be worse than failing loudly (same stance as ensureStopHook on
		// malformed JSON).
		return false, fmt.Errorf("unbalanced agentsmemory markers in %s: edit or remove the block manually, then re-run", path)
	case existing == "":
		// Fresh file (absent or empty): the block plus a trailing newline is the
		// whole content. Covers the sandbox case, where we own the config dir.
		next = block + "\n"
	default:
		// Append to existing user content, keeping a blank line of separation so the
		// managed block reads as its own section regardless of prior trailing space.
		sep := "\n\n"
		switch {
		case strings.HasSuffix(existing, "\n\n"):
			sep = ""
		case strings.HasSuffix(existing, "\n"):
			sep = "\n"
		}
		next = existing + sep + block + "\n"
	}

	// Nothing to do when the file already matches (idempotent re-run): no write, no
	// backup, report unchanged.
	if next == existing {
		return false, nil
	}

	// Back up the original bytes before modifying an existing file, mirroring
	// ensureStopHook. Nanosecond precision avoids clobbering an earlier backup on a
	// same-second re-run. A fresh/empty file has nothing worth backing up.
	if len(raw) > 0 {
		backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
		if err := os.WriteFile(backup, raw, 0o644); err != nil {
			return false, fmt.Errorf("backup %s: %w", path, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
