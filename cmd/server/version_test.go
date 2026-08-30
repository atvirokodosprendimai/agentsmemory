package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/buildinfo"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// TestVersionFlagPrintsTheStampedVersion runs the REAL root command with
// --version and asserts the printed string names the version variable. It fails
// when the wiring is removed — when cmd.Version is no longer set on the root
// command, urfave/cli v3 either rejects --version as an unknown flag or prints
// the default empty version, and either way this test goes red. That is the
// point: a version that exists only as a stamped linker symbol nobody can read
// is this repository's characteristic defect, "finished and unreachable".
func TestVersionFlagPrintsTheStampedVersion(t *testing.T) {
	cmd := rootCommand(config.Default())
	var buf bytes.Buffer
	cmd.Writer = &buf

	if err := cmd.Run(context.Background(), []string{"agentsmemory", "--version"}); err != nil {
		t.Fatalf("running --version: %v", err)
	}

	got := buf.String()
	// The resolver's answer, not the raw variable: on an unstamped build those
	// differ, and comparing against the variable is what let the CLI drift away
	// from every other surface unnoticed (see the test below).
	want := "agentsmemory version " + buildinfo.Effective(version)
	if !strings.Contains(got, want) {
		t.Errorf("--version printed %q, want it to contain %q", got, want)
	}
}

// TestEveryVersionSurfaceReadsTheSharedResolver is the test the previous one only
// looked like. TestVersionFlagPrintsTheStampedVersion pins the raw `version`
// variable, so it passed while --version and the MCP surfaces named DIFFERENT
// builds: the root command set Version: version while productionMCPServer set
// Version: buildinfo.Effective(version). Built without an ldflags tag, --version
// printed "dev" while the handshake and am_status reported dev-<commit> — four
// surfaces, two answers, which is precisely what issue #70 says one shared
// resolver exists to prevent.
//
// It checks the property in both directions. At runtime the root command's
// version must equal the resolver's answer; and no composite-literal Version
// field anywhere in main.go may be assigned the bare `version` identifier, which
// is what catches a NEW surface wiring itself straight to the linker symbol.
func TestEveryVersionSurfaceReadsTheSharedResolver(t *testing.T) {
	if got, want := rootCommand(config.Default()).Version, buildinfo.Effective(version); got != want {
		t.Errorf("the CLI reports %q and the shared resolver answers %q — an operator asking "+
			"--version and an agent reading am_status would name different builds", got, want)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Version" {
			return true
		}
		if id, ok := kv.Value.(*ast.Ident); ok && id.Name == "version" {
			t.Errorf("%s: a Version field is set from the bare `version` variable rather than "+
				"buildinfo.Effective(version). An unstamped build then reports \"dev\" on this "+
				"surface and dev-<commit> on the others, which is the disagreement issue #70 "+
				"closed", fset.Position(kv.Pos()))
		}
		return true
	})
}
