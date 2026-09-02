package mcpcli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
)

// documentSuffixes are the positional extensions that make a CLI argument a
// DOCUMENT rather than a literal value. The extension is what makes the
// behaviour predictable: `mcp update_skill start-here.md` reads a file, while
// `mcp search "notes.md"` still searches for that string, because only the
// first names a markdown document.
var documentSuffixes = []string{".md", ".markdown"}

// bom is a UTF-8 byte-order mark. Some editors write one ahead of the opening
// fence, where it is invisible and would otherwise make the frontmatter
// unreadable for no reason a human could see.
var bom = string(rune(0xFEFF))

// bodyArg is the argument a document's body fills when the tool has one. Every
// tool that takes prose from a human names it "content" — add_drawer,
// update_drawer, update_skill, diary_write's alias — so the body lands there
// rather than in whatever happens to be first in the required list. Falling
// back to PrimaryArg keeps a tool the convention has not reached callable.
const bodyArg = "content"

// chunkRunes is the size a single memory is stored at before the server splits
// it across several drawers. It is reported, never enforced: the CLI has no
// business refusing a write the server accepts, but an author who is about to
// file a memory is almost always trying to stay under this, and measuring it
// here saves sending the body through an agent's context window just to count
// it. Kept in sync with the server's own threshold by nothing but this comment
// — it is a diagnostic, so drift makes the note stale rather than the write
// wrong.
const chunkRunes = 1600

// Document is one markdown file resolved into tool arguments: scalar
// frontmatter keys become named arguments, and everything after the closing
// fence becomes the body.
type Document struct {
	Fields map[string]string // frontmatter key → value, in the file's own words
	Body   string            // everything after the frontmatter, verbatim
}

// IsDocumentPath reports whether a positional token names a markdown document.
//
// It tests the NAME only, deliberately. A token that looks like a document but
// is not on disk must fail loudly rather than be sent as a literal string: the
// call would otherwise succeed and file the path itself as the memory, which
// reads as a successful write and is impossible to notice afterwards.
func IsDocumentPath(token string) bool {
	lower := strings.ToLower(token)
	for _, suffix := range documentSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// LoadDocument reads a markdown file and splits its frontmatter from its body.
func LoadDocument(path string) (Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read %s: %w", path, err)
	}
	return ParseDocument(string(raw)), nil
}

// ParseDocument splits YAML-style frontmatter from a markdown body.
//
// Only SCALAR `key: value` lines are understood, which covers every frontmatter
// this CLI has a use for (a skill's name and description, a drawer's wing and
// room) and nothing else. A list or a nested map is skipped rather than
// guessed at — sending a half-understood structure as a tool argument would be
// worse than not reading it, and a real parser is a dependency this repository
// does not otherwise carry.
//
// A file with no frontmatter is not an error: the whole file is the body, which
// is the common case for a drawer written as plain prose.
func ParseDocument(raw string) Document {
	doc := Document{Fields: map[string]string{}, Body: raw}

	rest, ok := cutFence(raw)
	if !ok {
		return doc
	}
	frontmatter, body, ok := cutClosingFence(rest)
	if !ok {
		// An opening fence with no closing one is not frontmatter — it is a
		// horizontal rule at the top of a document. Treat the file as all body.
		return doc
	}
	doc.Body = body

	for _, line := range strings.Split(frontmatter, "\n") {
		trimmed := strings.TrimSpace(line)
		// A comment, a blank line, or a list item ("- x") carries no scalar.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
			continue
		}
		// Indentation means the line belongs to a nested structure, which this
		// parser does not model; skipping it keeps a nested value from being
		// read as a top-level one.
		if line != strings.TrimLeft(line, " \t") {
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		doc.Fields[key] = unquote(value)
	}
	return doc
}

// cutFence removes a leading "---" line, reporting whether there was one.
func cutFence(raw string) (string, bool) {
	// A byte-order mark ahead of the fence is invisible in an editor and would
	// otherwise make the frontmatter silently unreadable.
	trimmed := strings.TrimPrefix(raw, bom)
	for _, opener := range []string{"---\n", "---\r\n"} {
		if rest, ok := strings.CutPrefix(trimmed, opener); ok {
			return rest, true
		}
	}
	return "", false
}

// cutClosingFence splits at the first "---" line on its own, returning the
// frontmatter before it and the body after it.
func cutClosingFence(rest string) (frontmatter, body string, ok bool) {
	lines := strings.Split(rest, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "---" {
			continue
		}
		frontmatter = strings.Join(lines[:i], "\n")
		body = strings.Join(lines[i+1:], "\n")
		// The blank line conventionally separating frontmatter from prose is
		// punctuation, not content, so it does not become part of the memory.
		body = strings.TrimPrefix(body, "\n")
		return frontmatter, body, true
	}
	return "", "", false
}

// unquote strips one layer of matching quotes, so `description: "a, b"` yields
// the value a YAML reader would give rather than one carrying its own quotes.
func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	for _, quote := range []byte{'"', '\''} {
		if value[0] == quote && value[len(value)-1] == quote {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// DocumentArgs folds a document into the argument map for one tool: recognised
// frontmatter keys become arguments and the body fills the tool's content
// argument.
//
// ⚠ UNRECOGNISED FRONTMATTER KEYS ARE IGNORED, and that is the deliberate half.
// Real skill files carry keys this CLI knows nothing about (allowed-tools,
// license, model), and refusing them would make the feature unusable on exactly
// the files it exists to push. The cost is that a MISTYPED key is silent, which
// is why resolveDocument reports every key it dropped.
//
// Nothing here overwrites a value already in args: an explicit -a flag is the
// operator's last word and beats what the file says.
func DocumentArgs(tool mcp.Tool, doc Document, args map[string]any) (ignored []string) {
	// ⚠ THE BODY IS CLAIMED BEFORE THE FRONTMATTER LOOP RUNS, and that ordering is
	// the whole of this fix. The loop below skips a key already in args so an
	// explicit -a wins — but it cannot tell an operator's -a from the file's OWN
	// frontmatter, so a document carrying a `content:` key silently REPLACED its
	// entire body while the stderr note went on reporting "N runes → content".
	// Measured 2026-09-02: a 47-rune body filed as the string "12345" from a
	// frontmatter key, exit 0, no warning. A write that quietly stores something
	// other than what it was handed is the failure this document path already
	// fixed once for the path placeholder.
	//
	// An explicit -a still wins over both: it is set in args before this is called,
	// so the body assignment below is skipped exactly as a frontmatter key would be.
	if body := strings.TrimSpace(doc.Body); body != "" {
		if key := bodyKey(tool); key != "" {
			if _, set := args[key]; !set {
				args[key] = doc.Body
			}
		}
	}

	for key, value := range doc.Fields {
		if _, declared := tool.InputSchema.Properties[key]; !declared {
			ignored = append(ignored, key)
			continue
		}
		if _, set := args[key]; set {
			continue
		}
		args[key] = coerce(tool.InputSchema.Properties[key], value)
	}
	// Map iteration order is random, and this list is printed: an unstable
	// diagnostic reads as the tool behaving differently on each run.
	sort.Strings(ignored)

	return ignored
}

// bodyKey names the argument a document's body fills, or "" when the tool
// declares nowhere to put prose.
func bodyKey(tool mcp.Tool) string {
	if _, ok := tool.InputSchema.Properties[bodyArg]; ok {
		return bodyArg
	}
	return PrimaryArg(tool)
}

// DocumentNote describes what a document contributed, for a human reading
// stderr before a write lands. The rune count is the point: it is the number an
// author is checking a memory against, and counting it here is what saves
// sending the body through an agent twice.
func DocumentNote(path string, doc Document, bodyKey string, ignored []string) string {
	var note strings.Builder
	fmt.Fprintf(&note, "%s: %d runes", path, utf8.RuneCountInString(doc.Body))
	if runes := utf8.RuneCountInString(doc.Body); runes > chunkRunes {
		fmt.Fprintf(&note, " (over %d — the server will store this as several drawers)", chunkRunes)
	}
	if bodyKey != "" {
		fmt.Fprintf(&note, " → %s", bodyKey)
	}
	if len(doc.Fields) > 0 {
		fmt.Fprintf(&note, "; frontmatter %s", strings.Join(sortedKeys(doc.Fields), ", "))
	}
	if len(ignored) > 0 {
		fmt.Fprintf(&note, "; IGNORED (not an argument of this tool): %s", strings.Join(ignored, ", "))
	}
	// ⚠ A SEPARATE CLAUSE, BECAUSE THE REASON IS DIFFERENT AND THE OTHER LABEL WOULD
	// BE FALSE. `content` IS an argument of add_drawer — it is skipped because the
	// body already fills it, not because the tool does not take it. Reported at all
	// because the alternative is what this fix removed: a `content:` line that
	// silently replaced the whole document body while stderr said the body was used.
	if bodyKey != "" {
		if _, collides := doc.Fields[bodyKey]; collides {
			fmt.Fprintf(&note, "; frontmatter %s ignored — the document body fills it", bodyKey)
		}
	}
	return note.String()
}

// applyDocument folds a markdown positional into the call's arguments, if the
// invocation named one. It is a no-op for every ordinary call.
//
// The positional has already been folded into the tool's primary argument by
// ParseArgs, which cannot know a path from a value, so that placeholder is
// removed first — otherwise `mcp update_skill start-here.md` would file the
// STRING "start-here.md" as the skill's name and succeed.
func applyDocument(tool mcp.Tool, invocation Invocation, args map[string]any) error {
	path := documentPositional(invocation.Tail)
	if path == "" {
		return nil
	}
	doc, err := LoadDocument(path)
	if err != nil {
		return err
	}

	if primary := PrimaryArg(tool); primary != "" && args[primary] == path {
		delete(args, primary)
	}

	ignored := DocumentArgs(tool, doc, args)
	if invocation.Log != nil {
		fmt.Fprintln(invocation.Log, "aiagentmemory: "+DocumentNote(path, doc, bodyKey(tool), ignored))
	}
	return nil
}

// documentPositional returns the markdown path among the positional tokens, or
// "" when there is none. It mirrors ParseArgs's own scan — the same tokens are
// consumed by -a and the same key=value pairs are skipped — so it sees exactly
// the tokens ParseArgs considers positional.
//
// ⚠ IT SCANS EVERY POSITIONAL, NOT JUST THE FIRST, and that is the whole
// correctness of the update path. An earlier version stopped at the first bare
// non-document token, which broke every tool whose primary argument is an id:
// `mcp update_drawer <id> note.md` put the id first, so the document was never
// looked for. Observed 2026-09-01 against the live server — the call sent only
// the id, the server had no new content to apply, and it answered 200 with the
// UNCHANGED drawer. A correction that silently does nothing and prints the old
// text as if it were the new one is the worst shape this could have failed in.
func documentPositional(rawTail []string) string {
	for i := 0; i < len(rawTail); i++ {
		token := rawTail[i]
		switch {
		case token == "-a" || token == "--arg":
			i++ // the next token is that flag's value, never a positional
		case strings.Contains(token, "="):
		case IsDocumentPath(token):
			return token
		}
	}
	return ""
}

// sortedKeys returns a map's keys in a stable order, so a printed diagnostic
// does not change between two runs over the same file.
func sortedKeys(fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
