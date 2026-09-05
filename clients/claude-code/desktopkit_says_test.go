package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// agentValuesAcceptedBy reads the --agent values a resolver accepts out of its
// own switch, so a value added tomorrow joins the check on the same commit. The
// universe is the SOURCE, never a list kept beside it: issue #197 is what a list
// beside the truth looks like — the parser accepted seven values and the usage
// string named five.
func agentValuesAcceptedBy(t *testing.T, src, funcName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "agentkit.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						consts[name.Name] = v
					}
				}
			}
		}
	}
	var values []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != funcName {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, e := range cc.List {
				switch x := e.(type) {
				case *ast.Ident:
					if v, ok := consts[x.Name]; ok && v != "" {
						values = append(values, v)
					}
				case *ast.BasicLit:
					if v, _ := strconv.Unquote(x.Value); v != "" {
						values = append(values, v)
					}
				}
			}
			return true
		})
	}
	return values
}

func usageOfAgentFlag(t *testing.T, root *cli.Command, path ...string) string {
	t.Helper()
	cmd := root
	for _, name := range path {
		var next *cli.Command
		for _, c := range cmd.Commands {
			if c.Name == name {
				next = c
			}
		}
		if next == nil {
			t.Fatalf("no subcommand %q under %q", name, cmd.Name)
		}
		cmd = next
	}
	for _, fl := range cmd.Flags {
		if sf, ok := fl.(*cli.StringFlag); ok && sf.Name == "agent" {
			return sf.Usage
		}
	}
	t.Fatalf("%s has no --agent flag", strings.Join(path, " "))
	return ""
}

// TestEveryAcceptedAgentIsNamedInTheUsage is issue #197: `install --agent` and
// `update-skill --agent` accepted cursor and claude-desktop and named neither,
// so two install targets were discoverable only by typing a wrong value. The
// value is wired, reached and tested; the sentence beside it is what this pins.
func TestEveryAcceptedAgentIsNamedInTheUsage(t *testing.T) {
	src, err := os.ReadFile("agentkit.go")
	if err != nil {
		t.Fatal(err)
	}
	values := agentValuesAcceptedBy(t, string(src), "resolveAgentKits")
	if len(values) < 5 {
		t.Fatalf("read %d --agent values out of resolveAgentKits; the extractor broke and this check is vacuous", len(values))
	}
	t.Run("the extractor reads a case list", func(t *testing.T) {
		const fixture = "package p\nconst (\n\tagentX = \"x\"\n\tagentY = \"y-z\"\n)\nfunc r(n string) int {\n\tswitch n {\n\tcase \"\", agentX:\n\t\treturn 1\n\tcase agentY:\n\t\treturn 2\n\t}\n\treturn 0\n}\n"
		got := agentValuesAcceptedBy(t, fixture, "r")
		if strings.Join(got, ",") != "x,y-z" {
			t.Fatalf("extractor read %v from a fixture whose switch accepts x and y-z", got)
		}
	})

	root := rootCommand()
	for _, cmd := range [][]string{{"install"}, {"update-skill"}} {
		usage := usageOfAgentFlag(t, root, cmd...)
		for _, v := range values {
			if !regexp.MustCompile(`(^|[^a-z-])` + regexp.QuoteMeta(v) + `([^a-z-]|$)`).MatchString(usage) {
				t.Errorf("%s --agent accepts %q and its usage does not name it: %q", strings.Join(cmd, " "), v, usage)
			}
		}
	}
	// doctor checks one install at a time, so it names the single agents only —
	// but all of them, including the two that were missing everywhere.
	usage := usageOfAgentFlag(t, root, "doctor")
	for _, v := range values {
		if v == agentBoth || v == agentAll {
			continue
		}
		if !regexp.MustCompile(`(^|[^a-z-])` + regexp.QuoteMeta(v) + `([^a-z-]|$)`).MatchString(usage) {
			t.Errorf("doctor --agent accepts %q and its usage does not name it: %q", v, usage)
		}
	}
}

// TestClaudeDesktopKitCommentDoesNotDenyTheProtocol is issue #198: the kit's
// doc comment said Desktop receives "no protocol at all". It receives the
// handshake protocol — 1198 characters through the real bridge, measured
// 2026-09-04 — and the sibling comment in installer.go says so. A comment that
// went false one file over from the one that is true is the class
// TestNoToolDescriptionClaimsALongMemoryCannotBeMoved exists for.
func TestClaudeDesktopKitCommentDoesNotDenyTheProtocol(t *testing.T) {
	src, err := os.ReadFile("agentkit.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "no protocol at") {
		t.Fatal("agentkit.go still says Claude Desktop receives no protocol; the MCP handshake delivers one (ADR-021 T1, issue #198)")
	}
	if !strings.Contains(string(src), "handshake") {
		t.Fatal("agentkit.go's claudeDesktopKit comment does not say how the protocol reaches Desktop (the handshake)")
	}
}

// TestClaudeDesktopDryRunPrintsWhatTheInstallRegisters is issue #225: the
// rehearsal rendered its own string from the URL alone while the registration
// carried --wing and --token, so a --dry-run under-reported every install that
// set either. One rendering, from the same args the entry is built from; the
// token redacted the way the installer already redacts a bearer header.
func TestClaudeDesktopDryRunPrintsWhatTheInstallRegisters(t *testing.T) {
	inst, _, _ := newTestInstallerFor(t, claudeDesktopKit, false)
	inst.serverBin = fakeBuiltServerBin(t)
	inst.wing = "wing_acme"
	inst.token = "sk-secret-token"
	inst.dryRun = true
	if err := inst.registerAgentsMemoryMCP(); err != nil {
		t.Fatalf("dry-run register: %v", err)
	}
	out := inst.out.(interface{ String() string }).String()
	for _, want := range []string{"mcp-stdio", "--url " + inst.mcpURL, "--wing wing_acme", "--token ***"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry-run does not show %q, which the real install registers:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk-secret-token") {
		t.Errorf("the dry-run printed the token in clear:\n%s", out)
	}
}

// TestClaudeDesktopInstallFailsWhenItsOnlyStepFails is issue #208: for this kit
// the MCP registration is the whole install, and a failed one exited 0 with a
// "Next steps" block written as though it had succeeded.
func TestClaudeDesktopInstallFailsWhenItsOnlyStepFails(t *testing.T) {
	t.Run("no server binary", func(t *testing.T) {
		inst, _, _ := newTestInstallerFor(t, claudeDesktopKit, false)
		inst.serverBin = ""
		err := inst.run()
		out := inst.out.(interface{ String() string }).String()
		if err == nil {
			t.Fatalf("a Desktop install that registered nothing exited 0:\n%s", out)
		}
		if strings.Contains(out, "Next steps") {
			t.Errorf("a failed install printed its success summary:\n%s", out)
		}
	})
	t.Run("the bridge binary cannot be placed", func(t *testing.T) {
		// A directory where the binary goes makes the rename fail the way a
		// running Claude Desktop does on Windows, where the image it spawned is
		// held open (issue #208's second half).
		inst, _, dir := newTestInstallerFor(t, claudeDesktopKit, false)
		inst.serverBin = fakeBuiltServerBin(t)
		dest := filepath.Join(dir, "bin", installedServerBinFile())
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		err := inst.run()
		if err == nil {
			t.Fatal("placing the bridge over a directory succeeded")
		}
		if !strings.Contains(err.Error(), "Claude Desktop") {
			t.Errorf("the error does not tell the operator that a running Claude Desktop holds the bridge open:\n%v", err)
		}
	})
	// The other kits keep the old behaviour: their MCP is one part of a larger
	// install and a failed registration is warned, not fatal.
	t.Run("a Claude Code install still survives a failed registration", func(t *testing.T) {
		inst, _, _ := newTestInstallerFor(t, claudeKit, false)
		inst.mcpURL = "://not a url"
		if err := inst.run(); err != nil {
			t.Fatalf("a Claude Code install failed outright on its MCP step: %v", err)
		}
	})
}

var _ = context.Background
