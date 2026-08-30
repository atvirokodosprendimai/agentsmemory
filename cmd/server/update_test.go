package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

// TestServeChecksForAnUpdate pins the SELECTION, which is the half that goes
// missing here. internal/updatecheck can be entirely correct and never run: this
// repository's characteristic defect is a finished capability that nothing
// selects, and an update check nobody calls is precisely that shape (AGENTS.md
// §Reachability).
//
// It derives its universe from run's own body rather than from a list kept
// beside it, so deleting the `go announceUpdate(...)` line turns this red — the
// falsifiability the protocol asks for. Asserting that announceUpdate merely
// exists, or that it returns without error, would pass just as happily with the
// call site removed.
func TestServeChecksForAnUpdate(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	run := funcNamed(file, "run")
	if run == nil {
		t.Fatal("no func run in main.go — this check has stopped checking anything")
	}

	var called, inGoroutine bool
	ast.Inspect(run, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			if callsFunc(node.Call, "announceUpdate") {
				called, inGoroutine = true, true
			}
		case *ast.CallExpr:
			if callsFunc(node, "announceUpdate") {
				called = true
			}
		}
		return true
	})

	if !called {
		t.Fatal("run never calls announceUpdate, so the server can never tell an operator that " +
			"the build they are running has been superseded — issue #115 in one missing line")
	}
	if !inGoroutine {
		t.Error("announceUpdate is called synchronously in run: startup would then wait on GitHub, " +
			"which the issue rules out explicitly — it must run in a goroutine")
	}
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
