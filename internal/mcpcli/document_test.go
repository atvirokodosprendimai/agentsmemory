package mcpcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// updateSkill mirrors the live am_update_skill schema closely enough to exercise
// the document path: two required arguments, one of which is the body, plus the
// optional description whose absence the server treats as a blanking write.
func updateSkill() mcp.Tool {
	return mcp.NewTool("am_update_skill",
		mcp.WithString("name", mcp.Required()),
		mcp.WithString("content", mcp.Required()),
		mcp.WithString("description"),
		mcp.WithReadOnlyHintAnnotation(false),
	)
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseDocumentReadsScalarFrontmatterAndLeavesTheBodyVerbatim(t *testing.T) {
	doc := ParseDocument("---\nname: start-here\ndescription: \"the way in, and out\"\n---\n\n# Body\n\nkey: not frontmatter\n")

	if doc.Fields["name"] != "start-here" {
		t.Fatalf("name = %q", doc.Fields["name"])
	}
	// Quotes are the file format's, not the value's — a description stored with
	// them is what every session then reads in am_list_skills.
	if doc.Fields["description"] != "the way in, and out" {
		t.Fatalf("description = %q", doc.Fields["description"])
	}
	// The body is a memory: it must survive byte for byte, including a line that
	// looks like frontmatter but is below the fence.
	if doc.Body != "# Body\n\nkey: not frontmatter\n" {
		t.Fatalf("body = %q", doc.Body)
	}
}

func TestParseDocumentTreatsAnythingItCannotModelAsBody(t *testing.T) {
	for name, tc := range map[string]struct {
		raw       string
		wantField string // "" ⇒ no fields at all
		wantBody  string
	}{
		"no frontmatter": {
			raw:      "# Just prose\n",
			wantBody: "# Just prose\n",
		},
		"opening fence never closed is a horizontal rule": {
			raw:      "---\n# Just prose\n",
			wantBody: "---\n# Just prose\n",
		},
		"a byte-order mark does not hide the fence": {
			raw:       bom + "---\nname: x\n---\nbody\n",
			wantField: "x",
			wantBody:  "body\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := ParseDocument(tc.raw)
			if doc.Body != tc.wantBody {
				t.Fatalf("body = %q, want %q", doc.Body, tc.wantBody)
			}
			if tc.wantField == "" && len(doc.Fields) != 0 {
				t.Fatalf("fields = %v, want none", doc.Fields)
			}
			if tc.wantField != "" && doc.Fields["name"] != tc.wantField {
				t.Fatalf("name = %q, want %q", doc.Fields["name"], tc.wantField)
			}
		})
	}
}

func TestParseDocumentSkipsStructureItWouldHaveToGuessAt(t *testing.T) {
	// A list and a nested map are skipped rather than flattened: sending half of
	// a structure as a tool argument is worse than not reading it at all.
	doc := ParseDocument("---\nname: x\nallowed:\n  - one\n  - two\nmeta:\n  type: user\n---\nbody\n")

	if doc.Fields["name"] != "x" {
		t.Fatalf("scalar lost: %v", doc.Fields)
	}
	for _, key := range []string{"type", "one", "two"} {
		if _, present := doc.Fields[key]; present {
			t.Fatalf("nested key %q was read as top-level: %v", key, doc.Fields)
		}
	}
	// "allowed:" and "meta:" have no scalar value, so they carry nothing.
	if value := doc.Fields["allowed"]; value != "" {
		t.Fatalf("allowed = %q, want empty", value)
	}
}

func TestDocumentArgsFillsTheBodyAndReportsWhatItIgnored(t *testing.T) {
	tool := updateSkill()
	doc := ParseDocument("---\nname: start-here\ndescription: d\nallowed-tools: Read\nlicense: MIT\n---\nthe body\n")
	args := map[string]any{}

	ignored := DocumentArgs(tool, doc, args)

	if args["name"] != "start-here" || args["description"] != "d" {
		t.Fatalf("frontmatter not applied: %v", args)
	}
	if args["content"] != "the body\n" {
		t.Fatalf("content = %q", args["content"])
	}
	// Real skill files carry keys this CLI knows nothing about. Refusing them
	// would make the feature unusable on the files it exists to push, so they are
	// dropped — but reported, because a MISTYPED key is otherwise silent.
	want := []string{"allowed-tools", "license"}
	if strings.Join(ignored, ",") != strings.Join(want, ",") {
		t.Fatalf("ignored = %v, want %v (sorted)", ignored, want)
	}
}

func TestAnExplicitFlagBeatsTheFile(t *testing.T) {
	tool := updateSkill()
	doc := ParseDocument("---\nname: from-file\n---\nfrom file\n")
	args := map[string]any{"name": "from-flag", "content": "from flag"}

	DocumentArgs(tool, doc, args)

	if args["name"] != "from-flag" || args["content"] != "from flag" {
		t.Fatalf("the file overwrote an explicit -a: %v", args)
	}
}

func TestTheBodyFallsBackToThePrimaryArgumentWhenThereIsNoContentArgument(t *testing.T) {
	// A tool the "content" convention has not reached must still be callable
	// from a file rather than silently dropping the body.
	tool := mcp.NewTool("am_mine", mcp.WithString("text", mcp.Required()))
	args := map[string]any{}

	DocumentArgs(tool, ParseDocument("prose\n"), args)

	if args["text"] != "prose\n" {
		t.Fatalf("body did not reach the primary argument: %v", args)
	}
}

func TestADocumentPositionalIsReadAsAFileRatherThanFiledAsItsOwnPath(t *testing.T) {
	// The failure this prevents is silent and unrecoverable: ParseArgs folds any
	// positional into the primary argument, so without the document path the
	// string "skill.md" is filed as the skill's NAME and the call SUCCEEDS.
	path := writeFile(t, "skill.md", "---\nname: start-here\ndescription: d\n---\nthe real body\n")

	var sent map[string]any
	endpoint := Endpoint{
		ListTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{updateSkill(), mcp.NewTool("am_search", mcp.WithReadOnlyHintAnnotation(true))}, nil
		},
		CallTool: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sent, _ = req.Params.Arguments.(map[string]any)
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: `{"ok":true}`}}}, nil
		},
	}

	var out, log bytes.Buffer
	err := Run(t.Context(), &out, endpoint, Invocation{
		Tool: "update_skill", Tail: []string{path}, AllowWrites: true, Log: &log,
	})
	if err != nil {
		t.Fatal(err)
	}

	if sent["name"] != "start-here" {
		t.Fatalf("name = %v, want the frontmatter value (the path is not a name)", sent["name"])
	}
	if sent["content"] != "the real body\n" {
		t.Fatalf("content = %v, want the file body", sent["content"])
	}
	// The description is what am_list_skills shows every session, and the server
	// blanks it on a body-only write, so the document path must carry it.
	if sent["description"] != "d" {
		t.Fatalf("description = %v; a body-only update_skill blanks it server-side", sent["description"])
	}
	// The rune count is the number the author is checking against the chunk
	// threshold; reporting it here is what saves measuring the body separately.
	if !strings.Contains(log.String(), "14 runes") {
		t.Fatalf("no rune count on the log channel: %q", log.String())
	}
	if out.String() == "" || strings.Contains(out.String(), "runes") {
		t.Fatalf("diagnostics leaked into the piped result: %q", out.String())
	}
}

func TestAMissingDocumentFailsRatherThanBeingSentAsAString(t *testing.T) {
	endpoint := Endpoint{
		ListTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{updateSkill(), mcp.NewTool("am_search", mcp.WithReadOnlyHintAnnotation(true))}, nil
		},
		CallTool: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			t.Fatal("a call was made for a document that does not exist")
			return nil, nil
		},
	}

	var out bytes.Buffer
	err := Run(t.Context(), &out, endpoint, Invocation{
		Tool: "update_skill", Tail: []string{"no-such-file.md"}, AllowWrites: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no-such-file.md") {
		t.Fatalf("err = %v, want a read failure naming the file", err)
	}
}

func TestANonDocumentPositionalIsStillALiteralValue(t *testing.T) {
	// `mcp search "notes.md"` must keep searching for that string. Only the
	// extension distinguishes the two, so this is the boundary worth pinning.
	if IsDocumentPath("auth bug") {
		t.Fatal("a plain query was treated as a document")
	}
	if !IsDocumentPath("Notes.MD") {
		t.Fatal("the extension test is case-sensitive")
	}
	if documentPositional([]string{"-a", "wing=x.md", "query=y"}) != "" {
		t.Fatal("a value consumed by -a was mistaken for a positional document")
	}
	if got := documentPositional([]string{"note.md"}); got != "note.md" {
		t.Fatalf("documentPositional = %q", got)
	}
}

func TestADocumentIsFoundAfterAnIdPositional(t *testing.T) {
	// ⚠ REGRESSION, observed live 2026-09-01. Tools whose primary argument is an
	// id take the document SECOND: `mcp update_drawer <id> note.md`. A scan that
	// stopped at the first bare non-document token never looked past the id, so
	// the call went out carrying no content — and the server answered 200 with
	// the UNCHANGED drawer, printing the old text as if it were the new one.
	tool := mcp.NewTool("am_update_drawer",
		mcp.WithString("id", mcp.Required()),
		mcp.WithString("content"),
		mcp.WithString("reason"),
		mcp.WithReadOnlyHintAnnotation(false),
	)
	path := writeFile(t, "fix.md", "the corrected body\n")

	var sent map[string]any
	endpoint := Endpoint{
		ListTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{tool, mcp.NewTool("am_search", mcp.WithReadOnlyHintAnnotation(true))}, nil
		},
		CallTool: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sent, _ = req.Params.Arguments.(map[string]any)
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: `{"ok":true}`}}}, nil
		},
	}

	var out bytes.Buffer
	if err := Run(t.Context(), &out, endpoint, Invocation{
		Tool: "update_drawer", Tail: []string{"abc123", path}, ArgFlags: []string{"reason=r"}, AllowWrites: true,
	}); err != nil {
		t.Fatal(err)
	}

	if sent["id"] != "abc123" {
		t.Fatalf("id = %v, want the first positional", sent["id"])
	}
	if sent["content"] != "the corrected body\n" {
		t.Fatalf("content = %v — a correction carrying no content silently does nothing", sent["content"])
	}
	if sent["reason"] != "r" {
		t.Fatalf("reason = %v", sent["reason"])
	}
}

// TestAFrontmatterKeyDoesNotSilentlyReplaceTheBody pins that the document body
// fills the body argument even when the frontmatter names that argument too.
//
// ⚠ IT USED TO LOSE THE WHOLE BODY, SILENTLY, AND SAY OTHERWISE. DocumentArgs set
// frontmatter keys first and the body assignment was guarded by "already set" — a
// guard written so an explicit -a wins, which cannot tell an operator's flag from
// the file's own frontmatter. So a document carrying `content:` stored that scalar
// and discarded every line of prose, exit 0, while stderr reported "N runes →
// content". Found by stress-testing the document path 2026-09-02: a 47-rune body
// filed as the string "12345".
//
// An explicit -a must still win over both, because that IS the operator's last
// word — it is in args before DocumentArgs runs, so the body assignment skips it
// exactly as a frontmatter key would.
func TestAFrontmatterKeyDoesNotSilentlyReplaceTheBody(t *testing.T) {
	tool := updateSkill()
	doc := Document{
		Fields: map[string]string{"content": "12345", "name": "stress"},
		Body:   "the prose that must survive",
	}

	t.Run("the body wins over a frontmatter key of the same name", func(t *testing.T) {
		args := map[string]any{}
		DocumentArgs(tool, doc, args)
		if got := args["content"]; got != "the prose that must survive" {
			t.Errorf("content = %q, want the body — a frontmatter key silently replaced it", got)
		}
		if got := args["name"]; got != "stress" {
			t.Errorf("name = %q, want the frontmatter value: only the BODY key collides", got)
		}
	})

	t.Run("an explicit -a still beats both", func(t *testing.T) {
		args := map[string]any{"content": "from the flag"}
		DocumentArgs(tool, doc, args)
		if got := args["content"]; got != "from the flag" {
			t.Errorf("content = %q, want the flag's value — -a is the operator's last word", got)
		}
	})

	t.Run("the collision is reported rather than swallowed", func(t *testing.T) {
		note := DocumentNote("m.md", doc, "content", nil)
		if !strings.Contains(note, "content") || !strings.Contains(note, "body fills it") {
			t.Errorf("the note does not say the frontmatter content key was ignored: %s", note)
		}
		// ⚠ AND IT MUST NOT REUSE THE UNDECLARED-KEY LABEL, which would be false:
		// content IS an argument of this tool.
		if strings.Contains(note, "not an argument of this tool") {
			t.Errorf("the note calls a declared argument undeclared: %s", note)
		}
	})
}
