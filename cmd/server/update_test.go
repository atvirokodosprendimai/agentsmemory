package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

// TestTheUpdateCheckLaunchesFromTheListeningSeam pins the SELECTION and the
// ORDERING, which are two different failures and only one of them was covered.
//
// Selection is this repository's characteristic defect: internal/updatecheck can
// be entirely correct and never run, and an update check nobody calls is exactly
// that shape (AGENTS.md §Reachability).
//
// Ordering is what an earlier version of this test could not see. It asserted
// only that SOME goroutine call existed anywhere in run, and it stayed green
// while the launch sat at the top of run — before the database was opened and
// before either listening line. Issue #115 requires the notice AFTER the server
// is listening: launched earlier, a fast answer from GitHub prints ahead of the
// line an operator is waiting for, and a startup that fails later still
// announces an update for a server that never served.
//
// So the check has two halves. The launch must live in serveHTTP and nowhere
// else — that is the one seam both serving paths reach only after listenerFor
// succeeded — and every call to serveHTTP must be preceded, in its own function
// body, by the listening line. Move the call back into run and the first half
// fails; print the listening line after serving instead of before and the second
// does.
func TestTheUpdateCheckLaunchesFromTheListeningSeam(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	// Half one: which function launches it, and asynchronously.
	var launchedIn []string
	var inGoroutine bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				if callsFunc(node.Call, "announceUpdate") {
					launchedIn = append(launchedIn, fn.Name.Name)
					inGoroutine = true
				}
			case *ast.CallExpr:
				if callsFunc(node, "announceUpdate") {
					launchedIn = append(launchedIn, fn.Name.Name)
				}
			}
			return true
		})
	}

	if len(launchedIn) == 0 {
		t.Fatal("nothing calls announceUpdate, so the server can never tell an operator that the " +
			"build they are running has been superseded — issue #115 in one missing line")
	}
	if !inGoroutine {
		t.Error("announceUpdate is called synchronously: startup would then wait on GitHub, which " +
			"the issue rules out explicitly — it must run in a goroutine")
	}
	for _, where := range launchedIn {
		if where != "serveHTTP" {
			t.Errorf("announceUpdate is launched from %s; it must be launched from serveHTTP, the "+
				"one seam both serving paths reach only after the listener exists and the "+
				"listening line has been printed. Launched anywhere earlier, the notice can "+
				"print before that line, and a startup that fails afterwards still announces "+
				"an update for a server that never served (issue #115)", where)
		}
	}

	// Half two: every serveHTTP call site prints the listening line first.
	// Position-ordered rather than merely co-present: "both statements exist in
	// this function" is satisfied by the reverse order, which is the bug.
	var sites int
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name == "serveHTTP" || fn.Body == nil {
			continue
		}
		var listeningAt, serveAt token.Pos
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if callsFunc(call, "serveHTTP") && serveAt == token.NoPos {
				serveAt = call.Pos()
			}
			if listeningAt == token.NoPos && logsListening(call) {
				listeningAt = call.Pos()
			}
			return true
		})
		if serveAt == token.NoPos {
			continue
		}
		sites++
		if listeningAt == token.NoPos || listeningAt > serveAt {
			t.Errorf("%s calls serveHTTP without printing the listening line first, so the update "+
				"notice serveHTTP launches can appear before it", fn.Name.Name)
		}
	}
	if sites == 0 {
		t.Fatal("no function calls serveHTTP — this check has stopped checking anything")
	}
}

// logsListening reports whether a call is the startup line announcing the bound
// address. Matched on the format string rather than on the logger, because it is
// the SENTENCE an operator waits for that has to come first; which logging call
// prints it is not the property under test.
func logsListening(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	return strings.Contains(lit.Value, "listening on")
}

// TestAnnounceUpdateSkipsABuildWithNoTag covers the property that keeps this test
// binary — and every developer's local server — off the network: a build with no
// comparable tag makes no request at all. Left in, `go test ./cmd/server` would
// hit api.github.com on every run.
//
// A hung deadline is used as the probe because it fails LOUDLY if a request is
// ever made: an already-cancelled context makes any real round trip return
// immediately, so the assertion is that announceUpdate returns without consulting
// the network rather than that it happened to be fast.
func TestAnnounceUpdateSkipsABuildWithNoTag(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // any request made from here would fail instantly and silently

	done := make(chan struct{})
	go func() {
		defer close(done)
		announceUpdate(ctx, "dev-df4857d01234")
	}()

	select {
	case <-done:
	case <-time.After(updateCheckTimeout):
		t.Fatal("announceUpdate blocked on a dev build — it must return before making any request")
	}
}

// funcNamed returns the top-level function declaration with this name.
func funcNamed(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// callsFunc reports whether a call expression invokes this package-level function.
func callsFunc(call *ast.CallExpr, name string) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == name
}
