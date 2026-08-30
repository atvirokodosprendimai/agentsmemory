//go:build contractaxis

package mcptest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/contractaxis"
)

// TestMCPContractAxis evaluates the live hosted MCP surface and then proves its
// three class selectors with compiling, nonce-attested wire cuts. The mutation
// assertions run without the contractaxis tag, so their child go tests cannot
// recursively start this orchestrator.
func TestMCPContractAxis(t *testing.T) {
	ctx := context.Background()
	fixture := newMCPContractFixture(t, true)
	axis := fixture.axis(ctx)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository path: %v", err)
	}
	axis.MutationTarget, axis.MutationError = contractaxis.ResolveMutationTarget(ctx, repo)
	if axis.MutationError == nil {
		for _, spec := range mcpContractMutationSpecs() {
			evidence, mutationErr := contractaxis.RunMutation(ctx, repo, spec)
			axis.Mutants = append(axis.Mutants, evidence)
			if mutationErr != nil {
				axis.MutationError = errors.Join(axis.MutationError, mutationErr)
				break
			}
		}
	}

	report := contractaxis.Evaluate(ctx, time.Now().UTC(), axis)
	if err := contractaxis.WriteReport(os.Stdout, report); err != nil {
		t.Fatalf("write MCP contract-axis report: %v", err)
	}
	if !report.Complete() {
		t.Fatal("MCP contract axis is not completely enforced")
	}
}

func mcpContractMutationSpecs() []contractaxis.MutationSpec {
	compile := contractaxis.Command{
		Name: "go",
		Args: []string{
			"test", "./internal/mcpserver", "./internal/mcptest",
			"-run", "^$", "-count=1",
		},
		Env: contractMutationGoEnv(),
	}
	assertion := func(name string) contractaxis.Command {
		return contractaxis.Command{
			Name: "go",
			Args: []string{
				"test", "./internal/mcptest", "-run", "^" + name + "$", "-count=1", "-v",
			},
			Env: contractMutationGoEnv(),
		}
	}
	return []contractaxis.MutationSpec{
		{
			ID:              "mcp-readonly-hint-wire-cut",
			Axis:            mcpContractAxisID,
			Item:            "*",
			Case:            "*",
			Patch:           classifyToolMutationPatch,
			Compile:         compile,
			Assertion:       assertion("TestMCPContractReadOnlyHintAssertion"),
			ExpectedFailure: readOnlyHintMutationFailure,
		},
		{
			ID:              "mcp-star-scope-wire-cut",
			Axis:            mcpContractAxisID,
			Item:            "*",
			Case:            "star-scope",
			Patch:           searchWingMutationPatch,
			Compile:         compile,
			Assertion:       assertion("TestMCPContractStarScopeAssertion"),
			ExpectedFailure: starScopeMutationFailure,
		},
		{
			ID:              "mcp-write-guard-wire-cut",
			Axis:            mcpContractAxisID,
			Item:            "*",
			Case:            "member-refuse",
			Patch:           writeGuardMutationPatch,
			Compile:         compile,
			Assertion:       assertion("TestMCPContractWriteGuardAssertion"),
			ExpectedFailure: writeGuardMutationFailure,
		},
	}
}

func contractMutationGoEnv() []string {
	return []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off"}
}

// classifyToolMutationPatch cuts readOnlyHint out of the chokepoint, to prove the
// wire assertion still notices.
//
// ⚠ IT IS A PATCH, SO IT PINS THE SURROUNDING LINES TOO. Adding openWorldHint
// between the two stamped hints changed this hunk's CONTEXT, not just its offset,
// and git apply refused it — which the axis reported as an INVALID mutant rather
// than a passing one, correctly: a mutation that cannot be applied has proved
// nothing. Re-cut it whenever classifyTool's body moves, and note that plain
// go test ./... never runs this: the axis is behind the contractaxis build tag.
const classifyToolMutationPatch = `diff --git a/internal/mcpserver/server.go b/internal/mcpserver/server.go
--- a/internal/mcpserver/server.go
+++ b/internal/mcpserver/server.go
@@ -190,5 +190,4 @@
 func classifyTool(tool mcp.Tool, write bool) mcp.Tool {
-	tool.Annotations.ReadOnlyHint = mcp.ToBoolPtr(!write)
 	tool.Annotations.OpenWorldHint = mcp.ToBoolPtr(false)
 	if !write {
 		// Both are defined by MCP only for a tool that writes. A read tool is
`

const searchWingMutationPatch = `diff --git a/internal/mcpserver/server.go b/internal/mcpserver/server.go
--- a/internal/mcpserver/server.go
+++ b/internal/mcpserver/server.go
@@ -292,7 +292,4 @@
 func searchWingFor(ctx context.Context, passed string, scoped bool) (string, error) {
-	if allWings(passed) {
-		return "", nil
-	}
 	if w := strings.TrimSpace(passed); w != "" {
 		// "*" asks for every wing the caller can see. Scoping made the empty
 		// argument mean "my project", which silently removed the only way to ask
@@ -304,9 +301,6 @@ func searchWingFor(ctx context.Context, passed string, scoped bool) (string, error) {
 	if !scoped {
 		return "", nil
 	}
-	if allWings(auth.DefaultWingFrom(ctx)) {
-		return "", nil
-	}
 	if def := auth.DefaultWingFrom(ctx); def != "" {
 		return palace.SanitizeName(def, "wing")
 	}
`

const writeGuardMutationPatch = `diff --git a/internal/mcpserver/server.go b/internal/mcpserver/server.go
--- a/internal/mcpserver/server.go
+++ b/internal/mcpserver/server.go
@@ -98,7 +98,7 @@
 func (r *registrar) addWrite(tool mcp.Tool, handler server.ToolHandlerFunc) {
 	tool = classifyTool(tool, true)
 	// traceTool wraps OUTSIDE writeGuard so a role refusal is a visible
 	// failed_closed span rather than a silent drop. Argument payloads stay off
 	// the span: ADR-025 forbids dumping tool inputs into telemetry.
-	r.srv.AddTool(tool, traceTool(tool.Name, writeGuard(tool.Name, handler)))
+	r.srv.AddTool(tool, traceTool(tool.Name, handler))
 	r.catalog = append(r.catalog, CatalogEntry{Name: tool.Name, Description: tool.Description, Write: true})
 }
`
