package palace

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestAddSanitisesItsRoom pins that a room name is a NAME, checked like every
// other name this package accepts.
//
// ⚠ IT WAS THE ONE NAME NOTHING CHECKED, AND A ROOM IS ENCODED INTO A GRAPH
// SUBJECT. DerivedEdgeSubject builds "room:<wing>/<room>" by unescaped join, and
// BackfillWingRoots recovers the wing from that string by stripping affixes — on
// the assumption, stated in its own comment, that "a wing name is sanitised and
// carries no slash". True of the wing. The room was never sanitised at all.
//
// Measured 2026-09-02 over MCP, which is the only surface most callers have: one
// am_add_drawer into room "evil/llm_init" produced the subject
// "room:wing_acme/evil/llm_init", satisfied BackfillWingRoots's
// HasSuffix("/llm_init"), and on the next boot minted `wing_acme/evil.root` — a
// by-name root for a wing that does not exist. That is the failure the backfill's
// own ⚠ comment was written to prevent, arriving through the field nobody checked
// rather than the wildcard it anticipated. Rooms cannot be deleted, so both the
// room and its phantom root are permanent.
//
// The inconsistency was the tell: am_create_tunnel sanitises source_room and
// target_room, am_mine sanitises room, and SanitizeName's own doc comment calls it
// "a wing/room/agent/topic name" — only the write every session makes skipped it.
func TestAddSanitisesItsRoom(t *testing.T) {
	svc := newTestService(t)
	const teamID = "team-roomname"

	for _, tc := range []struct {
		name, room, wants string
	}{
		{"a slash breaks the graph subject's encoding", "evil/llm_init", "invalid path characters"},
		{"parent traversal", "../../../escape", "invalid path characters"},
		{"a backslash is the same encoding hazard", `evil\llm_init`, "invalid path characters"},
		{"a null byte", "room\x00null", "null bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Add(t.Context(), teamID, AddInput{
				Wing: "wing_acme", Room: tc.room, Content: "probe content",
			})
			if err == nil {
				t.Fatalf("Add accepted room %q — a room is encoded into a graph subject, so this "+
					"is how a phantom wing root gets minted", tc.room)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("error is not ErrInvalidInput, so the MCP layer cannot surface it as a "+
					"tool error: %v", err)
			}
			if !strings.Contains(err.Error(), "room") {
				t.Errorf("the error does not name the offending field: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error = %v, want it to say %q", err, tc.wants)
			}
		})
	}

	// The half that keeps the rule honest: every room this palace actually holds
	// must still be accepted. Checked against the live corpus before enforcing —
	// of 46 distinct rooms, only the two the probe created are rejected.
	t.Run("ordinary room names are still accepted", func(t *testing.T) {
		for _, room := range []string{
			"decisions", "llm_init", "llm_open_threads", "audit-scratch",
			"scratch-correction-test", "contract-testing", "ci_gotchas", "tool_use",
		} {
			if _, err := svc.Add(t.Context(), teamID, AddInput{
				Wing: "wing_acme", Room: room, Content: "probe " + room,
			}); err != nil {
				t.Errorf("Add refused the real room %q: %v", room, err)
			}
		}
	})
}

// TestPrepareWriteSanitisesBothNamesItStores is an AST gate on the one function
// every drawer write passes through.
//
// ⚠ ITS SCOPE IS prepareWrite AND IT SAYS SO. A gate whose name claims more than
// it covers is worse than a narrower one, and a universe derived from "every
// function that takes a room" would need an exemption list for the readers. What
// this pins is the property that was actually missing: the single chokepoint where
// a drawer's wing and room are accepted must run both through SanitizeName, so a
// refactor that drops either one fails here rather than in a corpus six weeks
// later. The behavioural cases above prove the check works; this proves it is
// still called.
func TestPrepareWriteSanitisesBothNamesItStores(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "service.go", nil, 0)
	if err != nil {
		t.Fatalf("parse service.go: %v", err)
	}

	sanitised := map[string]bool{}
	var found bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "prepareWrite" || fn.Body == nil {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "SanitizeName" || len(call.Args) < 2 {
				return true
			}
			if lit, ok := call.Args[1].(*ast.BasicLit); ok {
				sanitised[strings.Trim(lit.Value, `"`)] = true
			}
			return true
		})
	}
	if !found {
		t.Fatal("prepareWrite is not in service.go — this gate is now pinning nothing; " +
			"point it at whatever replaced it rather than deleting it")
	}
	for _, field := range []string{"wing", "room"} {
		if !sanitised[field] {
			t.Errorf("prepareWrite does not call SanitizeName(%q): a drawer's %s is encoded "+
				"into the graph subject \"room:<wing>/<room>\", and BackfillWingRoots recovers "+
				"the wing by stripping affixes on the assumption that neither carries a slash",
				field, field)
		}
	}
}
