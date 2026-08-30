package mcpserver

import (
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
)

// TestEveryToolPublishesHintsAClientCanBranchOn reads the LIVE tools/list and
// asserts the VALUES, not their presence.
//
// ⚠ PRESENCE CANNOT BE ASSERTED HERE, and finding that out is what made this test
// honest. The first draft checked each hint for nil, which reads as thorough and is
// dead code: measured on the wire, an UNDECLARED write tool comes back
// `idempotent=false destructive=true` — mcp-go materialises MCP's own spec defaults,
// so nothing a handler leaves unset ever arrives as absent. A nil branch here could
// never fire.
//
// That measurement is also the finding this change exists for. Before it, no tool
// set idempotentHint or destructiveHint on a write, so EVERY write tool on this
// surface was advertising itself as destructive by default — am_add_drawer
// included. A client building a confirmation prompt from the hints would have
// prompted on all fourteen, which is the same as prompting on none.
//
// Asserted over the wire because that is where a client reads it: a test over the
// mcp.Tool value would pass just as happily if classifyTool were never called.
func TestEveryToolPublishesHintsAClientCanBranchOn(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "hosted"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) {
			catalog, tools := liveSurface(t, local)
			if len(tools) == 0 {
				t.Fatal("tools/list returned nothing — every assertion below would be vacuous")
			}
			write := make(map[string]bool, len(catalog))
			for _, e := range catalog {
				write[e.Name] = e.Write
			}

			got := func(p *bool) bool { return p != nil && *p }
			checked := 0
			for _, tool := range tools {
				a := tool.Annotations
				if got(a.OpenWorldHint) {
					t.Errorf("%s publishes openWorldHint=true. Nothing here reaches outside the "+
						"caller's own workspace, and MCP's definition uses memory access as its "+
						"example of a closed domain", tool.Name)
				}
				if isWrite := write[tool.Name]; got(a.ReadOnlyHint) == isWrite {
					t.Errorf("%s publishes readOnlyHint=%t while the registrar has write=%t",
						tool.Name, got(a.ReadOnlyHint), isWrite)
				}
				if !write[tool.Name] {
					if got(a.DestructiveHint) {
						t.Errorf("read-only tool %s is advertised as destructive", tool.Name)
					}
					if !got(a.IdempotentHint) {
						t.Errorf("read-only tool %s is advertised as non-idempotent; a read "+
							"changes nothing, so retrying it is always safe", tool.Name)
					}
					continue
				}
				short := strings.TrimPrefix(tool.Name, mcpprotocol.ToolPrefix)
				want, ok := writeToolSemantics[short]
				if !ok {
					// TestWriteToolSemanticsCoversEveryWriteTool owns this case, and it
					// is the ONLY check that can see it — see the note above.
					continue
				}
				checked++
				if got(a.IdempotentHint) != want.idempotent {
					t.Errorf("%s publishes idempotentHint=%t, declared %t: %s",
						tool.Name, got(a.IdempotentHint), want.idempotent, want.why)
				}
				if got(a.DestructiveHint) != want.destructive {
					t.Errorf("%s publishes destructiveHint=%t, declared %t: %s",
						tool.Name, got(a.DestructiveHint), want.destructive, want.why)
				}
			}
			// A run that compared no declared write tool proves nothing about the
			// half of classifyTool this change added.
			if checked == 0 {
				t.Error("no declared write tool was compared against the wire — this surface " +
					"has fourteen, so the loop matched nothing and the check is vacuous")
			}
		})
	}
}

// TestWriteToolSemanticsCoversEveryWriteTool derives its universe from the
// registrar in BOTH directions.
//
// A declaration list is a second source of truth, and the failure mode of one is
// silence: a write tool added tomorrow would ship with nil hints, and a tool
// removed would leave an entry describing something that no longer exists. Neither
// breaks anything a behavioural test can see.
func TestWriteToolSemanticsCoversEveryWriteTool(t *testing.T) {
	// Local registers everything hosted does and more, so it is the wider universe.
	catalog, _ := liveSurface(t, true)

	registered := map[string]bool{}
	var undeclared []string
	for _, e := range catalog {
		if !e.Write {
			continue
		}
		short := strings.TrimPrefix(e.Name, mcpprotocol.ToolPrefix)
		registered[short] = true
		if _, ok := writeToolSemantics[short]; !ok {
			undeclared = append(undeclared, short)
		}
	}
	if len(registered) == 0 {
		t.Fatal("the catalogue reports no write tools — it has fourteen, so this check is " +
			"reading something other than the registrar and cannot fail")
	}
	sort.Strings(undeclared)
	for _, name := range undeclared {
		t.Errorf("write tool %q declares no idempotent/destructive semantics, so it ships with "+
			"nil hints and a client reads destructive=true by MCP's default. Add it to "+
			"writeToolSemantics with the reason.", name)
	}

	for name, s := range writeToolSemantics {
		if !registered[name] {
			t.Errorf("writeToolSemantics declares %q, which is not a registered write tool — "+
				"delete the entry rather than leaving semantics for something that is not there", name)
		}
		if strings.TrimSpace(s.why) == "" {
			t.Errorf("writeToolSemantics[%q] states no reason. idempotent and destructive are "+
				"judgements about what the handler does; the reason is what makes one reviewable", name)
		}
	}
}
