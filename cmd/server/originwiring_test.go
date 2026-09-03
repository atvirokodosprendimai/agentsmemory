package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// boundArgOfLocalTenant returns the source text of the third argument every
// auth.LocalTenant call in a file passes, in source order.
//
// It reads the SOURCE rather than driving serveLocal because serveLocal needs a
// database, a vector namespace and a listener before it reaches the line under
// test, and a check that expensive gets skipped on a busy day. The thing that
// can silently break here is not the guard's logic — origin_test.go covers that
// exhaustively — but the one argument that selects it, which is exactly the
// "finished and unreachable" defect AGENTS.md §Reachability records four times.
func boundArgOfLocalTenant(t testing.TB, path string) []string {
	t.Helper()

	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var args []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "LocalTenant" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "auth" {
			return true
		}
		if len(call.Args) < 3 {
			args = append(args, "")
			return true
		}
		lo := fset.Position(call.Args[2].Pos()).Offset
		hi := fset.Position(call.Args[2].End()).Offset
		args = append(args, string(src[lo:hi]))
		return true
	})
	return args
}

// TestTheLocalEndpointIsMountedBehindTheRebindGuard is the reachability half.
//
// auth.LocalTenant takes the guard as a parameter, so the compiler forces every
// caller to pass SOMETHING — and a caller that passes a literal false compiles,
// passes every behaviour test in internal/auth, serves happily, and leaves the
// whole palace reachable from any web page. That is a green suite over a feature
// that does nothing, which is the defect this repository keeps shipping. The
// argument must be the derived predicate, not a constant.
func TestTheLocalEndpointIsMountedBehindTheRebindGuard(t *testing.T) {
	args := boundArgOfLocalTenant(t, "main.go")
	if len(args) == 0 {
		t.Fatal("no auth.LocalTenant call found in main.go — the local endpoint is mounted somewhere this gate cannot see, so move the gate rather than deleting it")
	}
	for i, got := range args {
		if got != "localBoundary(cfg)" {
			t.Errorf("auth.LocalTenant call %d passes machineBounded = %q, want %q", i, got, "localBoundary(cfg)")
		}
	}

	// A corpus with zero offenders cannot exercise the branch that reports one,
	// so the falsifiability case drives the SAME extractor over a fixture that IS
	// broken. A copy of the loop here would pin nothing: severing the real call
	// site would leave this subtest green.
	t.Run("an unwired call is caught", func(t *testing.T) {
		dir := t.TempDir()
		bad := filepath.Join(dir, "bad.go")
		const fixture = `package main

func f() {
	local := auth.LocalTenant(t, cfg.LocalToken, false)
	_ = local
}
`
		if err := os.WriteFile(bad, []byte(fixture), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := boundArgOfLocalTenant(t, bad)
		if len(got) != 1 || got[0] != "false" {
			t.Fatalf("extractor read %v from a call that hard-codes false; it cannot notice an unwired guard", got)
		}
	})
}

// TestTheGuardAgreesWithTheExposureWarning pins localBoundary to the same three
// conditions serveLocal's exposure-warning switch treats as bounded.
//
// The two must not drift apart in either direction. A guard stricter than the
// warning refuses a deployment the operator was told at boot was fine — a socket
// or a container publish — and does it with a 403 that names a header rather
// than a policy. A guard looser than the warning trusts an address the operator
// was explicitly warned about, which puts the credential-free endpoint back on a
// routable port with nothing in front of it.
func TestTheGuardAgreesWithTheExposureWarning(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config.Config
		published string
		want      bool
	}{
		{name: "loopback bind is bounded", cfg: config.Config{Addr: config.LocalAddr}, want: true},
		{name: "localhost bind is bounded", cfg: config.Config{Addr: "localhost:8080"}, want: true},
		{name: "ipv6 loopback bind is bounded", cfg: config.Config{Addr: "[::1]:8080"}, want: true},
		{name: "a unix socket is bounded", cfg: config.Config{Addr: "0.0.0.0:8080", SocketPath: "/tmp/am.sock"}, want: true},
		{name: "a loopback-published container is bounded", cfg: config.Config{Addr: ":8080"}, published: "1", want: true},

		{name: "a bare routable bind is not", cfg: config.Config{Addr: "0.0.0.0:8080"}, want: false},
		{name: "a lan bind is not", cfg: config.Config{Addr: "192.168.1.5:8080"}, want: false},
		{name: "a token does not make it bounded", cfg: config.Config{Addr: "0.0.0.0:8080", LocalToken: "s3cret"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv also clears it for the cases that leave it empty, so an
			// ambient AGENTSMEMORY_PUBLISHED_LOOPBACK on the developer's machine
			// cannot turn a false case green.
			t.Setenv("AGENTSMEMORY_PUBLISHED_LOOPBACK", tc.published)
			if got := localBoundary(tc.cfg); got != tc.want {
				t.Errorf("localBoundary(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

// TestTheRebindGuardIsNamedInOperatorDocs keeps the refusal explicable.
//
// A 403 naming a Host header is the kind of message an operator meets while
// something else is already broken, and the first move is to search the repo for
// it. Documentation is load-bearing here in the same sense §Reachability gives
// the word: the guard changes what a deployment accepts, so a deployment doc
// that does not mention it describes a server that no longer exists.
func TestTheRebindGuardIsNamedInOperatorDocs(t *testing.T) {
	const needle = "does not address this machine"
	docs := []string{
		filepath.Join("..", "..", "README.md"),
	}
	for _, doc := range docs {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("read %s: %v", doc, err)
		}
		if !strings.Contains(string(b), needle) {
			t.Errorf("%s does not carry the refusal text %q an operator will paste into a search", doc, needle)
		}
	}
}
