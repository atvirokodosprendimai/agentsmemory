package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenCollectiveActivationIsReachable is ADR-042's Enforced-by gate, and it
// exists for this repo's signature defect: a capability that is finished, tested,
// and selected by nothing. T2, T3 and T4 each ship a component whose own tests pass
// in full while no payment is ever activated, because activation only happens if the
// composition root constructs the reconciler AND starts its loop.
//
// The universe is derived from the source rather than hardcoded, so a rename joins
// the check on the same commit instead of quietly falling out of it: the gate parses
// main.go and asks whether SOME call path constructs a billing reconciler and passes
// it to something that drives it. Deleting either half must turn this red — that is
// the mutation recorded in T5's Mutation Log.
func TestOpenCollectiveActivationIsReachable(t *testing.T) {
	// The universe is the whole composition-root PACKAGE, not one file. An earlier
	// draft parsed only main.go and failed while the wiring was present in a sibling
	// file — a gate whose scope is narrower than the thing it guards reports a defect
	// that is not there, and would just as happily miss one that is.
	dir := filepath.Join(repoRoot(t), "cmd", "server")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	// The reconciler's variable name is DERIVED from the assignment, and Run is then
	// required on that identifier specifically.
	//
	// ⚠ An earlier draft just asked "is any .Run( called in this package?". That
	// passed while the reconciler was constructed and never driven, because
	// cmd/server already contains four unrelated Run calls — rootCommand().Run,
	// mcpcli.Run, and the embedworker and mergejob background loops. A gate can be
	// green because it is looking at the wrong thing, and this one was.
	var reconcilerVar string
	var constructs, drives, starts bool
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				if as, ok := n.(*ast.AssignStmt); ok {
					for i, rhs := range as.Rhs {
						// Search the WHOLE right-hand side, not just its outermost call.
						// The construction is legitimately wrapped in builder calls
						// (`NewReconciler(...).WithLedger(...)`), and an earlier version of
						// this gate inspected only the top level — so adding the ledger made
						// it report the wiring as absent when the wiring was right there.
						// A gate should fail when the WIRING goes, not when its SHAPE changes.
						found := false
						ast.Inspect(rhs, func(inner ast.Node) bool {
							if isCallTo(inner, "billing", "NewReconciler") {
								found = true
								return false
							}
							return true
						})
						if found && i < len(as.Lhs) {
							constructs = true
							if id, ok := as.Lhs[i].(*ast.Ident); ok {
								reconcilerVar = id.Name
							}
						}
					}
				}
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "startOpenCollectiveReconciler" {
						starts = true
					}
				}
				return true
			})
		}
	}
	if reconcilerVar != "" {
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				ast.Inspect(file, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if isCallTo(call, reconcilerVar, "Run") {
							drives = true
						}
					}
					return true
				})
			}
		}
	}

	if !constructs {
		t.Error("nothing in cmd/server calls billing.NewReconciler: every OpenCollective payment would be read by nobody and no plan would ever activate")
	}
	if !drives {
		t.Error("nothing in cmd/server calls the reconciler's Run: a constructed reconciler that is never driven activates nothing")
	}
	if !starts {
		t.Error("nothing calls startOpenCollectiveReconciler: the wiring exists but the composition root never reaches it")
	}
}

// isCallTo reports whether n is a call of the form <recv>.<method>(...). Matching
// on BOTH halves is the point: the receiver is what separates the reconciler's Run
// from the four unrelated Run calls this package already contains.
func isCallTo(n ast.Node, recv, method string) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != method {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == recv
}

// TestBillingConfigReadsOpenCollectiveReconcileVars pins the operator surface: all
// four knobs must reach billing.Config, or an operator who sets them changes
// nothing. ADR-006 is the governing decision — a setting must be read in the MODE
// THAT IS RUNNING, so these are asserted on the opencollective branch.
func TestBillingConfigReadsOpenCollectiveReconcileVars(t *testing.T) {
	for _, kv := range [][2]string{
		{"BILLING_PROVIDER", "opencollective"},
		{"OPENCOLLECTIVE_CHECKOUT_PRO_MONTHLY", "https://opencollective.example/m"},
		{"OPENCOLLECTIVE_PERSONAL_TOKEN", "tok-123"},
		{"OPENCOLLECTIVE_COLLECTIVE_SLUG", "ai-agents-memory"},
		{"OPENCOLLECTIVE_API_URL", "https://api.example/graphql/v2"},
		{"OPENCOLLECTIVE_RECONCILE_INTERVAL", "3m"},
	} {
		t.Setenv(kv[0], kv[1])
	}

	cfg := billingConfig()
	if cfg.OpenCollectivePersonalToken != "tok-123" {
		t.Errorf("OPENCOLLECTIVE_PERSONAL_TOKEN did not reach Config: %q", cfg.OpenCollectivePersonalToken)
	}
	if cfg.OpenCollectiveSlug != "ai-agents-memory" {
		t.Errorf("OPENCOLLECTIVE_COLLECTIVE_SLUG did not reach Config: %q", cfg.OpenCollectiveSlug)
	}
	if cfg.OpenCollectiveAPIURL != "https://api.example/graphql/v2" {
		t.Errorf("OPENCOLLECTIVE_API_URL did not reach Config: %q", cfg.OpenCollectiveAPIURL)
	}
	if cfg.ReconcileInterval.String() != "3m0s" {
		t.Errorf("OPENCOLLECTIVE_RECONCILE_INTERVAL did not reach Config: %v", cfg.ReconcileInterval)
	}
}

// TestBillingConfigDefaultsTheReconcileKnobs: an operator who sets only the token
// and slug must get a working reconciler, not one pointed at an empty URL with a
// zero interval that would spin.
func TestBillingConfigDefaultsTheReconcileKnobs(t *testing.T) {
	t.Setenv("BILLING_PROVIDER", "opencollective")
	t.Setenv("OPENCOLLECTIVE_CHECKOUT_PRO_MONTHLY", "https://opencollective.example/m")
	t.Setenv("OPENCOLLECTIVE_PERSONAL_TOKEN", "tok")
	t.Setenv("OPENCOLLECTIVE_COLLECTIVE_SLUG", "slug")
	t.Setenv("OPENCOLLECTIVE_API_URL", "")
	t.Setenv("OPENCOLLECTIVE_RECONCILE_INTERVAL", "")

	cfg := billingConfig()
	if cfg.OpenCollectiveAPIURL == "" {
		t.Error("OPENCOLLECTIVE_API_URL unset must default to the public endpoint, not to an empty URL")
	}
	if cfg.ReconcileInterval <= 0 {
		t.Errorf("OPENCOLLECTIVE_RECONCILE_INTERVAL unset must default to a positive period, got %v: a zero interval would spin the loop", cfg.ReconcileInterval)
	}
}
