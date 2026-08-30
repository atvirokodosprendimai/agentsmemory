package mcpserver

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Rung-3 proofs for ADR-036.
//
// Every fact bound by docs/specs/2026-08-26-a-recall-that-answers.md is bound to
// a test in package palace, and no test in package palace can observe an MCP
// render site or a tool registration. A cold review found that gap: five of the
// ADR's tasks claim "mutation: delete the render line and the test goes red"
// while naming only palace tests, which would stay green because they call the
// service directly — exactly what a caller that was never wired also does.
//
// These are the tests that go red when the render or registration line is
// deleted. They belong beside catalog_test.go and hitview_test.go, which exist
// for the same reason.

// TestKGQueryResultRendersResolutionState is ADR-036 T2's rung-3 proof: the
// absence-vs-failure signal must reach the tool RESULT, not merely the Go
// struct. A field a handler sets and no renderer emits is invisible to every
// agent, and no behavioural test can see that.
func TestKGQueryResultRendersResolutionState(t *testing.T) {
	// A SOURCE check, deliberately. Rung 3 asks whether the intended caller can
	// DISCOVER the field, and only a source or schema check can answer that: a
	// behavioural test that reads the value passes whether or not the handler
	// emits it, because the test can reach the struct directly. That is exactly
	// what a caller which was never wired also does.
	keys := renderedKeysOf(t, "kg.go", "kg_query")
	for _, want := range []string{"resolution", "unresolved"} {
		if !keys[want] {
			t.Errorf("am_kg_query's rendered result has no %q key; the state is set on the Go struct and never reaches an agent", want)
		}
	}
}

// renderedKeysOf parses an mcpserver file and returns the string keys assigned
// into map[string]any results within the named tool's handler region.
//
// It is deliberately loose about WHERE in the handler a key is set — some are set
// in the literal and some conditionally afterwards — because the question is only
// "does this key ever reach the wire", and tightening it to the literal alone
// would report a conditionally-added key as missing.
func renderedKeysOf(t *testing.T, file, tool string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := string(src)
	i := strings.Index(body, strconv.Quote(tool))
	if i < 0 {
		t.Fatalf("%s: tool %q not found — this check has stopped checking anything", file, tool)
	}
	// Bound the region at the next tool registration so keys from a neighbouring
	// handler cannot satisfy this one.
	rest := body[i+len(tool):]
	if j := strings.Index(rest, "newTool("); j >= 0 {
		rest = rest[:j]
	}
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([a-z_]+)":`).FindAllStringSubmatch(rest, -1) {
		keys[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`out\["([a-z_]+)"\]`).FindAllStringSubmatch(rest, -1) {
		keys[m[1]] = true
	}
	if len(keys) == 0 {
		t.Fatalf("%s: no rendered keys found for %q", file, tool)
	}
	return keys
}

// TestSearchResultRendersFactsAndTheSiblingPointer is ADR-036 T3's rung-3 proof.
func TestSearchResultRendersFactsAndTheSiblingPointer(t *testing.T) {
	keys := renderedKeysOf(t, "drawers.go", "search")
	for _, want := range []string{"facts", "elsewhere_wings", "unlocatable_facts"} {
		if !keys[want] {
			t.Errorf("am_search's rendered result has no %q key; the block is built and reaches no agent", want)
		}
	}
}

// TestSearchResultRendersTheCorrectionMark is ADR-036 T5's rung-3 proof.
func TestSearchResultRendersTheCorrectionMark(t *testing.T) {
	// searchHitView is the wire shape, and hitview_test.go already enforces that
	// every palace.SearchHit field reaches it or is excused. This asserts the one
	// this task adds, and that it is POPULATED — a field declared on the view and
	// never assigned is on the wire as a permanent null.
	if !viewFieldIsPopulated(t, "Corrections") {
		t.Error("searchHitView.Corrections is declared but never assigned from the hit; every rendered result would carry a null")
	}
}

// viewFieldIsPopulated reports whether the searchHitView constructor assigns the
// named field. A source check, because a behavioural test reads the domain struct
// and cannot see whether the VIEW was filled from it.
func viewFieldIsPopulated(t *testing.T, field string) bool {
	t.Helper()
	src, err := os.ReadFile("drawers.go")
	if err != nil {
		t.Fatalf("read drawers.go: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "return searchHitView{")
	if i < 0 {
		t.Fatal("searchHitView constructor not found — this check has stopped checking anything")
	}
	end := strings.Index(body[i:], "\n\t}\n")
	if end < 0 {
		t.Fatal("could not bound the searchHitView literal")
	}
	return regexp.MustCompile(`\b` + field + `:\s*h\.`).MatchString(body[i : i+end])
}

// TestAddDrawerResultReportsItsEdge is ADR-036 T6's rung-3 proof. T6 promised
// this field while naming no mcpserver file at all.
func TestAddDrawerResultReportsItsEdge(t *testing.T) {
	keys := renderedKeysOf(t, "drawers.go", "add_drawer")
	for _, want := range []string{"has_edge", "edge_derived"} {
		if !keys[want] {
			t.Errorf("am_add_drawer's rendered result has no %q key; a caller cannot tell a filed drawer from a reachable one", want)
		}
	}
}

// TestEntryPointToolIsRegisteredAndDiscoverable is ADR-036 T7's rung-3 proof:
// the tool must appear in the catalogue with its arguments, not merely exist.
func TestEntryPointToolIsRegisteredAndDiscoverable(t *testing.T) {
	// Rung 3. A handler that serves the tool is not enough: an agent consults the
	// CATALOGUE, so a tool the catalogue omits is one nobody will ever call — and
	// no behavioural test can see that, because a test that calls the tool passes
	// either way.
	src, err := os.ReadFile("kg.go")
	if err != nil {
		t.Fatalf("read kg.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, `newTool("entry_point"`) {
		t.Error("no entry_point tool is declared")
	}
	if !strings.Contains(body, "registerEntryPoint(reg,") {
		t.Error("registerEntryPoint is never called from registerKG; the tool exists and nothing registers it")
	}
	keys := renderedKeysOf(t, "kg.go", "entry_point")
	for _, want := range []string{"node", "edges", "resolution"} {
		if !keys[want] {
			t.Errorf("the entry_point result has no %q key", want)
		}
	}
	if !strings.Contains(body, `mcp.WithString("wing", mcp.Required()`) {
		t.Error("the wing argument is not advertised as required; an agent reading the schema cannot know to send it")
	}
}

// TestBootstrapToolIsRegisteredAndDiscoverable is ADR-036 T8's rung-3 proof. A
// bootstrap nobody can find is the 13-call protocol it was written to replace.
func TestBootstrapToolIsRegisteredAndDiscoverable(t *testing.T) {
	// A bootstrap nobody can find is the 13-call protocol it replaced, wearing a
	// different name. Rung 3, and only a source or schema check can see it.
	src, err := os.ReadFile("bootstrap.go")
	if err != nil {
		t.Fatalf("read bootstrap.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, `newTool("bootstrap"`) {
		t.Error("no bootstrap tool is declared")
	}
	if !strings.Contains(body, `mcp.WithString("wing", mcp.Required()`) {
		t.Error("the wing argument is not advertised as required; an agent reading the schema cannot know to send it")
	}

	srv, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	if !strings.Contains(string(srv), "registerBootstrap(reg,") {
		t.Error("registerBootstrap is never called; the tool exists and nothing registers it — the exact defect this ADR exists to remove")
	}

	// The emitted keys live in palace.BootstrapResult.WireShape, which is
	// deliberately the ONE place the shape is written: the handler returned a
	// hand-built map and the cost gate marshalled the struct, so the two could
	// drift while the gate kept passing on a response that no longer matched.
	shape, shapeErr := os.ReadFile("../palace/bootstrap.go")
	if shapeErr != nil {
		t.Fatalf("read palace/bootstrap.go: %v", shapeErr)
	}
	shapeBody := string(shape)
	i := strings.Index(shapeBody, "func (r BootstrapResult) WireShape()")
	if i < 0 {
		t.Fatal("WireShape not found — the emitted shape has moved and this check has stopped checking anything")
	}
	region := shapeBody[i:]
	if j := strings.Index(region, "\n}\n"); j > 0 {
		region = region[:j]
	}
	for _, want := range []string{"entry_point", "eager", "on_demand", "corrections", "truncation", "wing"} {
		if !strings.Contains(region, strconv.Quote(want)) {
			t.Errorf("the emitted bootstrap shape has no %q key", want)
		}
	}
	// And the handler must actually return it, or the shape is a decoration.
	if !strings.Contains(string(src), "res.WireShape()") {
		t.Error("the bootstrap handler does not return WireShape; the emitted response and the measured one can drift apart")
	}
}
