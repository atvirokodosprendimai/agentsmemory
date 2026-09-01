package web

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// retiredWriteRuleClaim matches a document asserting a write-path rule that
// ADR-038, ADR-045 or ADR-046 removed.
//
// It is deliberately narrow, for the reason TestNoToolDescriptionClaimsALongMemoryCannotBeMoved
// records: the looser rule — "documents must be true" — is not gateable, and a
// matcher broad enough to catch any sentence about chunking would flag the advice
// these documents SHOULD keep giving (that content over the threshold is chunked,
// that one drawer is one vector, that an entry record is cheaper kept short). What
// is banned is the claim of a REFUSAL or an IN-PLACE guarantee that no longer holds.
var retiredWriteRuleClaim = regexp.MustCompile(
	`(?i)refuses in-place|never\s+moved|cannot be moved|relocated for life|` +
		`must fit in one chunk|served one chunk at a time|refused if it would chunk|` +
		// An unqualified "kg_add is idempotent" is retired the same way: the no-op
		// covers a CURRENT fact, and a fact filed with valid_to is not deduped.
		//
		// ⚠ NOT ANCHORED ON PUNCTUATION. The first version ended `idempotent\.`, so
		// "is idempotent, so a repeat is always a no-op" walked straight through —
		// demonstrated by a reviewer against this very gate. The claim is matched
		// however it is punctuated, and the sentence that must stay sayable is
		// excused by kgAddQualified instead.
		"`" + `am_kg_add` + "`" + ` is idempotent`)

// kgAddQualified is the sentence this gate must NOT flag: the no-op is real for a
// current fact, and only the unqualified claim is retired.
//
// A separate pattern rather than a negative lookahead in the one above, because
// Go's RE2 has no lookahead — "match X unless Y follows" is not expressible, and
// the punctuation-anchored form that stood in for it let a comma past.
var kgAddQualified = regexp.MustCompile(`(?i)idempotent\s+for a current fact`)

// protocolDocs are the agent-facing documents this repository ships: the one the
// server embeds and serves, and the ones the installer copies into an agent's
// config directory. Paths are relative to internal/web.
//
// ⚠ THE UNIVERSE IS THE POINT OF THIS GATE. TestNoToolDescriptionClaimsALongMemoryCannotBeMoved
// covers `*.go` under internal/mcpserver and says so honestly in its own name — and
// that honesty is exactly what made the hole invisible: the gate was green, its name
// was true, and nobody asked what lay outside it. The retired one-way door went on
// being taught by the document the server hands out, in a PR that edited that very
// file twice. Reported by review on PR #147.
var protocolDocs = []string{
	"bootstrap-memory.md",
	"claude-guide.md",
	filepath.Join("..", "..", "clients", "claude-code", "bootstrap.md"),
	filepath.Join("..", "..", "clients", "claude-code", "commands", "am.md"),
	filepath.Join("..", "..", "clients", "claude-code", "commands", "M.md"),
}

// TestNoShippedProtocolTeachesARetiredWriteRule is the doc-side sibling of the
// mcpserver description gate.
//
// A tool description is read by a caller who already has the tool list. These
// documents are read BEFORE that — the served one is what a new agent is handed
// first — so a false sentence here costs more, not less, and reaches a reader with
// less to check it against.
func TestNoShippedProtocolTeachesARetiredWriteRule(t *testing.T) {
	// The falsifiability half is a SUBTEST rather than a sibling: it must sit inside
	// the one command an acceptance fence runs, or a mutation campaign can report
	// "killed" from a fence that never executed it. A corpus with zero offenders
	// cannot exercise the branch that reports one, so the branch is driven over a
	// fixture that IS an offender — through the same regexp, not a copy of it.
	t.Run("the matcher catches the sentences this gate was written against", func(t *testing.T) {
		for _, retired := range []string{
			"`am_update_drawer` refuses in-place content edits to anything multi-chunk.",
			"a multi-chunk memory can be CORRECTED but never MOVED",
			"A memory created at or under 1600 runes stays ONE row and can be relocated for life.",
			"## 10. ⚠ A document you intend to maintain must fit in one chunk",
			"Keep it under 1600 runes: the entry tier is served one chunk at a time",
			"a memory filed into llm_init is REFUSED if it would chunk",
		} {
			if !retiredWriteRuleClaim.MatchString(retired) {
				t.Errorf("the matcher does not catch a sentence this gate exists for, so it "+
					"proves nothing about the corpus:\n  %s", retired)
			}
		}
		for _, keep := range []string{
			"Content over 1600 runes is chunked into several drawers sharing a parent.",
			"One drawer is one vector, so a memory averaging many topics matches none sharply.",
			"Entry records are served WHOLE at every wake-up, so length there is paid by every session.",
			"An ENDED record cannot be relocated at all, because the first ending is the one that is true.",
		} {
			if retiredWriteRuleClaim.MatchString(keep) {
				t.Errorf("the matcher flags advice these documents SHOULD keep giving; a gate "+
					"that forbids the true sentence along with the false one gets deleted:\n  %s", keep)
			}
		}
	})

	checked := 0
	for _, rel := range protocolDocs {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read the shipped protocol %s: %v", rel, err)
		}
		checked++
		for i, line := range strings.Split(string(raw), "\n") {
			if loc := retiredWriteRuleClaim.FindString(line); loc != "" && !kgAddQualified.MatchString(line) {
				t.Errorf("%s:%d teaches a retired write rule (%q).\n"+
					"  A shipped protocol is the only route by which most sessions learn what "+
					"the server accepts, so a false one does not merely mislead — it unships a "+
					"capability, because nobody uses what the document in front of them forbids.",
					rel, i+1, loc)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no protocol documents were examined, so this gate passed without looking at " +
			"anything — the shape that makes a green run meaningless")
	}
	t.Logf("examined %d shipped protocol document(s)", checked)
}

// TestNoShippedProtocolAdvertisesAStaleChunkThreshold catches the other half of the
// same drift, which is numeric rather than grammatical.
//
// The served protocol advertised "roughly 800 characters" as the chunk threshold in
// seven places while palace.ChunkSize had been 1600 for far longer — wrong in a way
// no prose gate notices, because every sentence around it is true. The number is
// derived from the constant here so the two cannot drift again: change ChunkSize and
// this fails until the documents follow.
func TestNoShippedProtocolAdvertisesAStaleChunkThreshold(t *testing.T) {
	real := strconv.Itoa(palace.ChunkSize)
	// Any three- or four-digit number sitting next to "chars"/"runes"/"characters"
	// is claiming to BE the threshold. Matching bare numbers would flag every date
	// and count in the document.
	threshold := regexp.MustCompile(`(?i)(\d{3,4})\s*(?:\+\s*)?(?:chars?|runes?|characters?)`)

	checked := 0
	for _, rel := range protocolDocs {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read the shipped protocol %s: %v", rel, err)
		}
		checked++
		for i, line := range strings.Split(string(raw), "\n") {
			for _, m := range threshold.FindAllStringSubmatch(line, -1) {
				if m[1] == real {
					continue
				}
				// 128 is MaxKGValueLen and 4096 is the skill-description cap: both are
				// real limits that are not the chunk size, so they are named rather
				// than matched loosely.
				if m[1] == strconv.Itoa(palace.MaxKGValueLen) || m[1] == "4096" {
					continue
				}
				t.Errorf("%s:%d advertises %q as a size limit while palace.ChunkSize is %s.\n"+
					"  %s",
					rel, i+1, m[0], real,
					fmt.Sprintf("A stale threshold is the drift no prose gate sees: every "+
						"sentence around it reads true, and an agent sizing a record to %s "+
						"splits documents that never needed splitting.", m[1]))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no protocol documents were examined, so this gate is asserting nothing")
	}
	t.Logf("examined %d document(s) against ChunkSize=%s", checked, real)
}
