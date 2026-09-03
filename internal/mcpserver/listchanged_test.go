package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// advertisedListChanged returns the literal this package passes to
// server.WithToolCapabilities in the given directory, and whether the call was
// found at all.
//
// The argument is not "advertise tools" despite what the call site read like for
// a long time: mcp-go creates the tools capability object unconditionally
// (server.go:541), and the bool is listChanged alone — a promise to send
// notifications/tools/list_changed when the catalogue changes.
func advertisedListChanged(t testing.TB, dir string) (lit string, found bool) {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "WithToolCapabilities" || len(call.Args) != 1 {
					return true
				}
				lo := fset.Position(call.Args[0].Pos()).Offset
				hi := fset.Position(call.Args[0].End()).Offset
				lit, found = string(src[lo:hi]), true
				return false
			})
		}
	}
	return lit, found
}

// sendsAnyNotification reports whether anything in this package pushes to a
// client. It is the other half of the question: an advertisement is only honest
// if some code path can make good on it.
func sendsAnyNotification(t testing.TB, dir string) bool {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		// The string appears in a doc comment in status.go explaining that this
		// server does NOT use it, so a naive grep would answer yes. Only a call
		// counts, which is what the "(" requires.
		if strings.Contains(string(b), "SendNotificationToClient(") ||
			strings.Contains(string(b), "SendNotificationToAllClients(") {
			return true
		}
	}
	return false
}

// TestNoCapabilityIsAdvertisedThatNothingCanDeliver holds the handshake to what
// the server can actually do.
//
// Measured 2026-09-03 against the running container: `initialize` answered
// `"capabilities":{"tools":{"listChanged":true}}` while no code path in this
// package calls SendNotificationToClient and the transport is mounted with
// WithStateLess(true), which keeps no session to push down. So the promise could
// not be kept by any route, and a client that trusts it waits for a message that
// is never sent — silently, which is why nothing had ever contradicted it.
//
// This is the §Reachability defect with the arrow reversed. The usual case is a
// capability that works and nothing selects; this is a capability SELECTED in the
// handshake and backed by nothing. Both are invisible to a suite that tests the
// component, because in this direction there is no component to test.
func TestNoCapabilityIsAdvertisedThatNothingCanDeliver(t *testing.T) {
	lit, found := advertisedListChanged(t, ".")
	if !found {
		t.Fatal("no server.WithToolCapabilities call found — the handshake is built somewhere this gate cannot see, so move the gate rather than deleting it")
	}

	sends := sendsAnyNotification(t, ".")
	if lit == "true" && !sends {
		t.Errorf("WithToolCapabilities(%s) promises notifications/tools/list_changed, but nothing in this package sends a notification — advertise false until something does", lit)
	}
	if lit == "false" && sends {
		t.Errorf("something here sends notifications but WithToolCapabilities(%s) tells clients not to expect them", lit)
	}

	// A tree with zero offenders cannot exercise the branch that reports one, so
	// the falsifiability case drives the SAME extractor over a fixture that IS an
	// offender. Sharing the extractor is the point: a copy would stay green if
	// the real call site were severed.
	t.Run("a true advertisement with no sender is caught", func(t *testing.T) {
		dir := t.TempDir()
		const fixture = `package mcpserver

func build() {
	_ = server.NewMCPServer("x", "v", server.WithToolCapabilities(true))
}
`
		if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(fixture), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got, ok := advertisedListChanged(t, dir)
		if !ok || got != "true" {
			t.Fatalf("extractor read (%q, %v) from a fixture that advertises true; it cannot notice a false promise", got, ok)
		}
		if sendsAnyNotification(t, dir) {
			t.Fatal("the sender detector found a send in a fixture that has none")
		}
	})
}
