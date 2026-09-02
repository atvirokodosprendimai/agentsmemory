package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"

	"github.com/urfave/cli/v3"
)

// TestTelemetrySetupHasOneChokepoint fails when telemetry.Setup is called from
// anywhere but withTelemetry.
//
// This is the gate for issue #53, and it is a chokepoint check rather than a
// per-command one because the per-command shape is what failed: Setup lived in
// run() and runEval(), the other ten commands offering --otel-endpoint installed
// nothing, and every existing gate passed — the field was assigned and read
// SOMEWHERE, which is the weaker question ADR-006 already rejected. With a single
// call site, "which commands are traced" stops being a list anybody maintains and
// becomes a property of wrapTelemetry walking the tree.
func TestTelemetrySetupHasOneChokepoint(t *testing.T) {
	callers := map[string]int{}
	seams := 0
	packages, err := parser.ParseDir(token.NewFileSet(), ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse cmd/server: %v", err)
	}
	for _, file := range packages["main"].Files {
		for _, declaration := range file.Decls {
			// ⚠ THE CHOKEPOINT IS A PACKAGE-LEVEL VAR, NOT A CALL INSIDE A FUNCTION.
			// `var telemetrySetup = telemetry.Setup` is the seam a test substitutes
			// to observe that the deferred flush actually runs — without it nothing
			// in this package can see that, which a mutation audit proved by
			// deleting the whole defer and watching the suite stay green. Walking
			// only function bodies counted the seam as ZERO and failed a correct
			// tree. The property ADR-006 asked for is unchanged: exactly ONE place
			// reaches telemetry.Setup, and no command reaches it by remembering to.
			if generic, ok := declaration.(*ast.GenDecl); ok {
				ast.Inspect(generic, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "telemetry" && selector.Sel.Name == "Setup" {
						seams++
					}
					return true
				})
				continue
			}
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if ok && pkg.Name == "telemetry" && selector.Sel.Name == "Setup" {
					callers[function.Name.Name]++
				}
				return true
			})
		}
	}

	total := seams
	for name, count := range callers {
		total += count
		if name != "withTelemetry" {
			t.Errorf("%s calls telemetry.Setup directly; every command must be traced through withTelemetry, not by remembering to install a provider (issue #53)", name)
		}
	}
	if total != 1 {
		t.Errorf("telemetry.Setup call sites = %d, want exactly one seam in withTelemetry", total)
	}
}

// TestEveryActionInTheCommandTreeIsWrapped walks the real rootCommand tree and
// fails on an action that does not go through withTelemetry.
//
// It reads the tree rootCommand actually returns, not a list of command names,
// so a subcommand added tomorrow is covered on the commit that adds it. Detection
// is by the closure's own symbol name: withTelemetry returns a function literal
// declared inside it, so runtime naming distinguishes a wrapped action from a raw
// one without invoking either — which matters, because invoking these actions
// opens the database and serves.
func TestEveryActionInTheCommandTreeIsWrapped(t *testing.T) {
	checked := 0
	var walk func(path string, cmd *cli.Command)
	walk = func(path string, cmd *cli.Command) {
		if cmd.Action != nil {
			checked++
			name := runtime.FuncForPC(reflect.ValueOf(cmd.Action).Pointer()).Name()
			if !strings.Contains(name, "withTelemetry") {
				t.Errorf("%s: action is %s, not wrapped by withTelemetry — --otel-endpoint would parse here and install no provider (issue #53)", path, name)
			}
		}
		for _, sub := range cmd.Commands {
			walk(path+" "+sub.Name, sub)
		}
	}
	walk("agentsmemory", rootCommand(config.Default()))

	// A tree whose actions were all nil would pass the loop above vacuously.
	if checked == 0 {
		t.Fatal("walked the command tree and found no actions at all — the check proved nothing")
	}
}

// TestASubcommandOfferingOtelEndpointInstallsAProvider is the end-to-end half:
// it runs the real `mcp` subcommand through the real root command with
// --otel-endpoint and asserts the provider was installed.
//
// The structural gates above prove there is one seam and that every action goes
// through it. This one proves the seam FIRES on the command the issue was filed
// about, which no amount of AST reading can. It lists the catalogue rather than
// calling a tool, because listing is registration-only and opens no database.
func TestASubcommandOfferingOtelEndpointInstallsAProvider(t *testing.T) {
	var notices bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&notices)
	t.Cleanup(func() { log.SetOutput(previous) })

	err := rootCommand(config.Default()).Run(context.Background(),
		[]string{"agentsmemory", "mcp", "--otel-endpoint", "stdout"})
	if err != nil {
		t.Fatalf("mcp --otel-endpoint stdout: %v", err)
	}
	if !strings.Contains(notices.String(), "otel: exporting traces to stdout") {
		t.Errorf("mcp ran and installed no telemetry provider; log was:\n%s", notices.String())
	}
}

// TestWithTelemetryRunsTheActionWhenSetupFails pins the fail-open ADR-025 asks
// for: an endpoint that cannot be reached costs observability, never the work.
// Without this, "install a provider everywhere" would be a way to make every
// subcommand fail when a collector is down.
func TestWithTelemetryRunsTheActionWhenSetupFails(t *testing.T) {
	ran := false
	action := withTelemetry(config.Default(), func(context.Context, *cli.Command) error {
		ran = true
		return nil
	})
	cmd := &cli.Command{
		Name:   "probe",
		Flags:  dataFlags(config.Default()),
		Action: action,
	}
	// A URL no exporter can construct an endpoint from: Setup returns an error
	// rather than a provider, which is the branch under test.
	if err := cmd.Run(context.Background(), []string{"probe", "--otel-endpoint", "://not-a-url"}); err != nil {
		t.Fatalf("a failed exporter must not fail the command: %v", err)
	}
	if !ran {
		t.Error("action did not run when telemetry setup failed")
	}
}
