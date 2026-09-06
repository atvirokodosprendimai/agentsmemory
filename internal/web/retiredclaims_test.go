package web

import (
	"fmt"
	"io/fs"
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

// kgAddClaim is the one alternative in retiredWriteRuleClaim that kgAddQualified
// may excuse. Without it the exemption applies to whatever matched, which is how
// an unrelated retired rule rides through on a qualified line.
var kgAddClaim = regexp.MustCompile("(?i)`am_kg_add` is idempotent")

// retiredClaimIn returns the retired claim this line teaches, or "" when it
// teaches none. It is THE predicate: the loop and the falsifiability subtest both
// call it, which is the only arrangement that makes severing either pattern
// visible.
//
// ⚠ THE EXEMPTION IS DECIDED AGAINST THE MATCHED CLAIM, NOT THE LINE. Asking
// "does this line qualify the kg_add claim" excused every OTHER retired rule that
// happened to share the line — a reviewer put ADR-045's "never MOVED" on the same
// bullet as the qualified sentence and the gate stayed green. The qualifier can
// only ever excuse the claim it qualifies.
func retiredClaimIn(line string) string {
	// ⚠ EVERY MATCH, NOT THE FIRST. FindString returns the earliest one, so an
	// excused kg_add claim at the start of a bullet HID a second retired rule
	// behind it — the fixture below is exactly that line, and it passed the first
	// version of this function.
	for _, loc := range retiredWriteRuleClaim.FindAllString(line, -1) {
		if kgAddClaim.MatchString(loc) && kgAddQualified.MatchString(line) {
			continue
		}
		return loc
	}
	return ""
}

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
// ⚠ THE COMMANDS HALF IS DERIVED, NOT LISTED, AND THAT CHANGED AFTER THIS LIST
// WENT STALE. It named commands/M.md, which was retired — the gate broke loudly,
// which is the safe direction. The unsafe one is silent: a command ADDED to the
// kit would simply not be covered, and this gate's own comment above is about
// exactly that failure. Globbing the directory means a command reaches the gate
// on the commit that adds it.
func protocolDocPaths(t *testing.T) []string {
	t.Helper()
	// ⚠ WALKED, NOT LISTED, AND THAT IS THE WHOLE FIX. The list this replaced named
	// three files and missed a SECOND SERVED BUNDLE: internal/web/ai is embedded by
	// sitemap.go's //go:embed and served at /ai/*, and ai/bootstrap-memory.md
	// carried a sentence this very gate's regexp matches. The gate was green, its
	// name was true, and nobody asked what lay outside it — which is the sentence
	// the gate's own comment used about the gate it replaced (issue #155).
	//
	// So the universe is every Markdown file in this package's tree, which covers
	// everything //go:embed ships and a little more, plus the client kit. A file added to either joins the check
	// on the commit that adds it rather than when somebody remembers a list.
	var docs []string
	if err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			docs = append(docs, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk the served documents: %v", err)
	}
	// ⚠ ONLY THE BUNDLE SIDE OF THIS TRIPWIRE IS ARMED, and saying so is the point.
	// Everything outside ai/ counts as "served", which includes three CLAUDE.md
	// files that are repo instructions shipped by nothing — so `served == 0` cannot
	// fire while they exist. The bundled side is the real guard: a walk that
	// stopped before the embedded bundle is what this catches. Wider coverage is
	// the safe direction, so the walk stays as it is and the claim shrinks to what
	// is true. (Nor is every file here embedded: //go:embed ai ships the bundle and
	// guide.go embeds three named files; the CLAUDE.md files are shipped by
	// neither.) Raised in review of PR #325.
	var served, bundled int
	for _, d := range docs {
		if strings.HasPrefix(d, "ai"+string(filepath.Separator)) {
			bundled++
			continue
		}
		served++
	}
	if served == 0 || bundled == 0 {
		t.Fatalf("walked %d served page(s) and %d bundle document(s); this gate needs both, and "+
			"a zero on either side means the walk broke rather than that the corpus is small",
			served, bundled)
	}
	docs = append(docs, filepath.Join("..", "..", "clients", "claude-code", "bootstrap.md"))
	cmds, err := filepath.Glob(filepath.Join("..", "..", "clients", "claude-code", "commands", "*.md"))
	if err != nil {
		t.Fatalf("glob shipped commands: %v", err)
	}
	if len(cmds) == 0 {
		t.Fatal("no shipped commands found; this gate would be checking only the embedded docs and would say nothing about the kit")
	}
	return append(docs, cmds...)
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
			// The kg_add claim however it is punctuated — a comma walked through
			// the period-anchored first version.
			"⚠ `am_kg_add` is idempotent, so a repeat is always a no-op.",
			"**⚠ `am_kg_add` IS IDEMPOTENT**, so replacing a fact means invalidate FIRST.",
			// ⚠ AND A RETIRED RULE SHARING A LINE WITH THE QUALIFIED SENTENCE. The
			// exemption used to be decided against the whole line, so this exact
			// shape — ADR-045's claim riding on a correctly qualified kg_add bullet
			// — passed the gate that exists to catch it.
			"**⚠ `am_kg_add` IS IDEMPOTENT FOR A CURRENT FACT**, and a multi-chunk memory " +
				"can be CORRECTED but never MOVED,",
		} {
			if retiredClaimIn(retired) == "" {
				t.Errorf("the matcher does not catch a sentence this gate exists for, so it "+
					"proves nothing about the corpus:\n  %s", retired)
			}
		}
		for _, keep := range []string{
			"Content over 1600 runes is chunked into several drawers sharing a parent.",
			"One drawer is one vector, so a memory averaging many topics matches none sharply.",
			"Entry records are served WHOLE at every wake-up, so length there is paid by every session.",
			"An ENDED record cannot be relocated at all, because the first ending is the one that is true.",
			// The qualified kg_add sentence must stay sayable: the no-op is real
			// for a current fact, and only the unqualified claim is retired. This
			// is the fixture that fails if somebody deletes kgAddQualified.
			"⚠ `am_kg_add` is idempotent FOR A CURRENT FACT (a fact filed with valid_to is not deduped).",
		} {
			if loc := retiredClaimIn(keep); loc != "" {
				t.Errorf("the matcher flags advice these documents SHOULD keep giving (%q); a gate "+
					"that forbids the true sentence along with the false one gets deleted:\n  %s", loc, keep)
			}
		}
	})

	checked := 0
	for _, rel := range protocolDocPaths(t) {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read the shipped protocol %s: %v", rel, err)
		}
		checked++
		for i, line := range strings.Split(string(raw), "\n") {
			if loc := retiredClaimIn(line); loc != "" {
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
	for _, rel := range protocolDocPaths(t) {
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

// TestNoShippedProtocolClaimsTheWholeEntryRoomIsServedEagerly is the numeric
// sibling of the stale-threshold gate, for the bound that replaced a retired
// claim in PR #325.
//
// ⚠ A FROZEN NUMBER REPLACED A FROZEN CLAIM, which is the trade this repository
// keeps making by accident. "roughly 800 characters" sat in seven documents
// against a ChunkSize of 1600 and no prose gate noticed, because every sentence
// around it was true. "the first ten records" is the same shape: correct today,
// derived from palace.BootstrapEagerLimit, and typed into three shipped surfaces
// by hand.
//
// So a line claiming entry-room records are served WHOLE has to carry the current
// bound. The tool descriptions interpolate the constant and are pinned in
// internal/mcpserver; documents cannot interpolate, so they are pinned here.
func TestNoShippedProtocolClaimsTheWholeEntryRoomIsServedEagerly(t *testing.T) {
	limit := strconv.Itoa(palace.BootstrapEagerLimit)
	// The claim, however it is worded around the capitalised word this corpus uses
	// for it. Matching "WHOLE" alone would flag prose about whole memories.
	claim := regexp.MustCompile(`(?i)served WHOLE at every wake-?up`)

	checked, found := 0, 0
	for _, rel := range protocolDocPaths(t) {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read the shipped protocol %s: %v", rel, err)
		}
		checked++
		// ⚠ PARAGRAPHS, NOT LINES, and the line-based first draft found ONE of the
		// two claims in this corpus. Prose wraps: internal/web/bootstrap-memory.md
		// breaks "served WHOLE at every / wake-up" across a line, so a per-line
		// matcher saw neither half and reported a clean run over an unqualified
		// sentence. A gate whose universe is the physical line asks a question
		// about formatting, not about what the document says.
		line := 1
		for _, para := range strings.Split(string(raw), "\n\n") {
			start := line
			line += strings.Count(para, "\n") + 2
			flat := strings.Join(strings.Fields(para), " ")
			if !claim.MatchString(flat) {
				continue
			}
			found++
			if !strings.Contains(flat, limit) {
				t.Errorf("%s:%d says entry-room records are served whole and does not name the "+
					"bound (%s):\n    %s\n"+
					"  Records past it arrive as POINTERS. An unqualified sentence tells a session "+
					"the whole room is paid for at every wake-up, which is a cost model the server "+
					"does not have — and it is the shape \"roughly 800 characters\" had.",
					rel, start, limit, flat)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no protocol documents were examined")
	}
	if found == 0 {
		t.Fatal("no shipped document makes the eager-serving claim at all. Either the walk broke " +
			"or the sentence was deleted everywhere — and this gate would pass on both, so it " +
			"says so rather than reporting a clean run")
	}
	t.Logf("checked %d document(s); %d line(s) make the claim", checked, found)
}
