package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
)

// planCaps stands in for the tenant repository: a plan-derived lookup that is
// recognisably not the override, so a test can tell which one was returned.
type planCaps struct{ cap int }

func (p planCaps) MonthlyCap(context.Context, string) (int, error) { return p.cap, nil }

// TestCapLookupHonoursTheOverride covers the three documented values. The zero
// value is the one worth reading: an operator who configures nothing must get
// today's behaviour, not a new default nobody chose.
func TestCapLookupHonoursTheOverride(t *testing.T) {
	plans := planCaps{cap: 10_000}

	cases := []struct {
		name       string
		configured int
		want       int
	}{
		{"unset leaves the plan deciding", 0, 10_000},
		{"a positive value caps every workspace", 500, 500},
		{"a negative value uncaps them", -1, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := capLookupFor(config.Config{MonthlyRequestCap: tc.configured}, plans).
				MonthlyCap(context.Background(), "any-team")
			if err != nil {
				t.Fatalf("MonthlyCap: %v", err)
			}
			if got != tc.want {
				t.Errorf("cap = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestTheUnsetOverrideReturnsThePlanLookupItself is stricter than the value
// check above, and deliberately so: returning usage.FixedCap(10000) would give
// the same NUMBER for this plan and be wrong for every other workspace, since a
// fixed cap stops consulting plans at all. The identity is the property.
func TestTheUnsetOverrideReturnsThePlanLookupItself(t *testing.T) {
	plans := planCaps{cap: 10_000}
	got := capLookupFor(config.Config{}, plans)

	if _, isFixed := got.(usage.FixedCap); isFixed {
		t.Fatal("an unconfigured process replaced the plan lookup with a fixed cap, so every " +
			"workspace would share one limit whatever its plan says")
	}
	if got != usage.CapLookup(plans) {
		t.Error("the unset path did not return the plan lookup unchanged")
	}
}

// TestTheMeteringServiceConsultsTheOverride pins the SELECTION, which is the
// half this repository loses (AGENTS.md §Reachability). capLookupFor can be
// entirely correct and never called: the cap that is actually enforced is
// whichever CapLookup buildServicesWith hands to usage.NewService, and before
// this change that was the tenant repo unconditionally.
//
// Source-derived because the alternative needs a migrated database and a real
// tenant repo, and neither would make the assertion stronger — the question is
// which argument is passed, and that is answerable from the source. Deleting
// the call turns this red.
func TestTheMeteringServiceConsultsTheOverride(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var found, wired bool
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "NewService" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "usage" {
			return true
		}
		found = true
		if len(call.Args) < 2 {
			return false
		}
		inner, ok := call.Args[1].(*ast.CallExpr)
		if !ok {
			return false
		}
		if id, ok := inner.Fun.(*ast.Ident); ok && id.Name == "capLookupFor" {
			wired = true
		}
		return false
	})

	if !found {
		t.Fatal("no usage.NewService call in main.go — this check has stopped checking anything")
	}
	if !wired {
		t.Error("usage.NewService is not given capLookupFor's result, so the configured cap " +
			"override reaches nothing and a self-hosted operator is still held to the plan's " +
			"limit however they configure the process — issue #10 in one missing call")
	}
}
