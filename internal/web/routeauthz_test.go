package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// authzCoverage binds every workspace-scoped POST route to the test that proves a
// caller without the role is refused, and names the package that test lives in —
// because the gate is not always in this package. postShareRequest and
// postMergeRequest carry no handler-level check at all: the service they call
// returns ErrForbidden, so the covering test is one layer down, and a rule that
// demanded a test in internal/web would be answered by writing a worse one here.
//
// The test names are resolved against the source, not merely stored, so renaming
// or deleting one fails here rather than leaving a claim of coverage behind.
var authzCoverage = map[string]struct{ test, dir string }{
	"postSkill":              {"TestSkillWriteRefusesAReadOnlyMember", "."},
	"postAddMember":          {"TestAddMemberRefusesANonAdmin", "."},
	"postSetMemberRole":      {"TestSetMemberRoleRefusesANonAdmin", "."},
	"postRemoveMember":       {"TestRemoveMemberRefusesANonAdmin", "."},
	"postWingImport":         {"TestWingImportRefusesAReadOnlyMember", "."},
	"postUpgrade":            {"TestUpgradeRefusesANonAdmin", "."},
	"postManageSubscription": {"TestManageSubscriptionRefusesANonAdmin", "."},
	"postShareRequest":       {"TestRequestRejectsReadOnlyMember", "../share"},
	"postShareAccept":        {"TestAcceptRequiresDestAdmin", "../share"},
	"postShareDecline":       {"TestDeclineRequiresAdmin", "../share"},
	"postMergeRequest":       {"TestEnqueueRejectsReadOnlyMember", "../mergejob"},
}

// authzExempt lists the workspace-scoped POST routes that deliberately have no
// role gate, each with the reason. An entry is a written decision, which is the
// point: the alternative to naming it here is a handler nobody ever notices is
// open.
var authzExempt = map[string]string{
	"postRotateKey": "any member may rotate their OWN key by design (see the doc comment on postRotateKey): " +
		"the handler derives the key's owner from the authenticated caller, so there is no other member's " +
		"credential to reach and nothing to escalate.",
}

// routePostHandler matches a chi POST registration for a workspace-scoped route
// and captures the handler's method name. The `{teamID}` in the path is what
// makes the route workspace-scoped, and therefore what makes a role check
// meaningful: the account and auth routes above it have no workspace to be a
// member of.
var routePostHandler = regexp.MustCompile(`r\.Post\("(/projects/\{teamID\}[^"]*)",\s*s\.(\w+)\)`)

// TestEveryWorkspaceMutatingRouteHasAnAuthzTest derives its universe from web.go's
// own route table, so a handler registered tomorrow joins this check on the same
// commit rather than whenever somebody remembers.
//
// ⚠ IT EXISTS BECAUSE THE HAND-WRITTEN VERSION WAS NOT ENOUGH. A mutation audit
// on 2026-09-01 neutered the role gate in six of these handlers one at a time and
// the whole repository stayed green — including postSetMemberRole, where the
// missing refusal lets a read-only member hand itself the admin role. Each of the
// six now has a test; nothing but this stopped the seventh.
//
// It checks a BINDING, not behaviour: that every route is either bound to a test
// that exists, or exempt with a reason. Whether that test actually refuses is the
// test's own business — this is the rung above, the one no individual test can
// occupy, and it is the same shape as TestEverySpecBindingNamesATestThatExists.
func TestEveryWorkspaceMutatingRouteHasAnAuthzTest(t *testing.T) {
	src, err := os.ReadFile("web.go")
	if err != nil {
		t.Fatalf("read web.go: %v", err)
	}
	routes := routePostHandler.FindAllStringSubmatch(string(src), -1)
	if len(routes) == 0 {
		t.Fatal("no workspace-scoped POST routes found in web.go — the route table's shape changed and this " +
			"gate is now deriving its universe from nothing, which reads as every route being covered")
	}

	for _, m := range routes {
		path, handler := m[1], m[2]
		cover, bound := authzCoverage[handler]
		if reason, exempt := authzExempt[handler]; exempt {
			if bound {
				t.Errorf("%s (%s) is listed as both covered and exempt; one of the two is stale", handler, path)
			}
			if len(strings.TrimSpace(reason)) < 40 {
				t.Errorf("%s (%s) is exempt with no real reason written down: %q", handler, path, reason)
			}
			continue
		}
		if !bound {
			t.Errorf("%s (%s) is a workspace-scoped mutating route with no authorization test bound to it. "+
				"Add one — refusing a caller without the role, driven through the real membership lookup — "+
				"and bind it in authzCoverage; or, if it is deliberately open to any member, say why in "+
				"authzExempt. A role check that no test exercises is indistinguishable from one that was "+
				"never written, which is how six of these shipped.", handler, path)
			continue
		}
		if !testFuncExists(t, cover.dir, cover.test) {
			t.Errorf("%s (%s) is bound to %s in %s, and no such test function exists there — the binding "+
				"reads as coverage while proving nothing", handler, path, cover.test, cover.dir)
		}
	}

	for handler := range authzCoverage {
		if !registered(routes, handler) {
			t.Errorf("authzCoverage binds %s, which no longer appears in web.go's route table — remove the "+
				"entry so the list stays a description of the routes that exist", handler)
		}
	}
	for handler := range authzExempt {
		if !registered(routes, handler) {
			t.Errorf("authzExempt excuses %s, which no longer appears in web.go's route table — remove the "+
				"entry rather than leaving a standing exemption for nothing", handler)
		}
	}
}

// registered reports whether a handler name is among the routes parsed from
// web.go. It keeps the two lists from outliving the routes they describe: an
// exemption for a deleted handler is a permission nobody reviews again.
func registered(routes [][]string, handler string) bool {
	for _, m := range routes {
		if m[2] == handler {
			return true
		}
	}
	return false
}

// testFuncExists resolves a test function by name in a package directory with
// go/parser rather than by running it, so a binding to a test that is failing, or
// skipped, or behind a build tag is checked exactly like a passing one. What is
// being verified is that the pointer resolves.
func testFuncExists(t *testing.T, dir, name string) bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// TestARouteWithNoAuthzTestIsCaught is the falsifiability half: a corpus in which
// every route is bound cannot exercise the branch that reports one that is not, so
// without this the gate could be severed and would go on announcing that
// everything is covered. It drives the same two lookups the gate does over a
// fabricated route table rather than reimplementing them.
func TestARouteWithNoAuthzTestIsCaught(t *testing.T) {
	table := `r.Post("/projects/{teamID}/danger", s.postSomethingNobodyTested)`
	routes := routePostHandler.FindAllStringSubmatch(table, -1)
	if len(routes) != 1 {
		t.Fatalf("the route matcher found %d routes in a table holding one; the gate's universe is derived "+
			"with this regexp, so a matcher that misses a registration silently covers nothing", len(routes))
	}
	handler := routes[0][2]
	if _, bound := authzCoverage[handler]; bound {
		t.Fatalf("%s is bound in authzCoverage; the fixture must name a handler that is not", handler)
	}
	if _, exempt := authzExempt[handler]; exempt {
		t.Fatalf("%s is excused in authzExempt; the fixture must name a handler that is not", handler)
	}
	if testFuncExists(t, ".", "TestNoSuchTestFunctionExistsHere") {
		t.Fatal("testFuncExists resolved a test that does not exist, so every binding it checks resolves " +
			"and the resolution half of the gate proves nothing")
	}
	if !testFuncExists(t, ".", "TestEveryWorkspaceMutatingRouteHasAnAuthzTest") {
		t.Fatal("testFuncExists failed to resolve a test that is in this very file, so it would report every " +
			"real binding as broken")
	}
}
