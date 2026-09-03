package mcpserver

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
)

// declaredTitleOverrides are the tools whose title is set explicitly because the
// derivation reads badly. Listing them here is the deliberate step: a new
// override has to be admitted rather than silently diverging from titleFor.
var declaredTitleOverrides = map[string]bool{
	"am_kg_add": true,
}

// TestEveryToolCarriesADisplayTitle pins the derivation at the catalogue, not at
// titleFor.
//
// MCP separates the programmatic `name` from a human-readable `title` and tells
// clients to prefer the title for display. All 41 tools shipped without one, so a
// picker or a permission prompt showed `am_kg_supersede`. Testing titleFor alone
// would pass while newTool stopped applying it — the component works, nothing
// selects it — so this drives the real registrar and reads what a client gets.
func TestEveryToolCarriesADisplayTitle(t *testing.T) {
	_, tools := liveSurface(t, false)
	if len(tools) == 0 {
		t.Fatal("no tools registered — this gate cannot see the catalogue it is meant to check")
	}

	for _, tool := range tools {
		if tool.Title == "" {
			t.Errorf("%s has no title; a client with nothing to display falls back to the wire name", tool.Name)
			continue
		}
		// A title identical to the wire name is the shape that passes a
		// presence check and buys nothing.
		if tool.Title == tool.Name {
			t.Errorf("%s: title equals the wire name, which is what having no title already does", tool.Name)
		}
		// ⚠ AND IT MUST BE THE DERIVATION OR A DELIBERATE OVERRIDE. Review found
		// that assigning every tool the same constant string passed every
		// assertion above: presence and difference-from-name are both satisfied by
		// "Agent memory tool" on all 41. Comparing against titleFor binds the
		// mechanism rather than the fact that some string is present.
		bare := strings.TrimPrefix(tool.Name, mcpprotocol.ToolPrefix)
		if tool.Title != titleFor(bare) && !declaredTitleOverrides[tool.Name] {
			t.Errorf("%s: title %q is neither titleFor(%q)=%q nor a declared override; a constant applied to every tool would pass a weaker check",
				tool.Name, tool.Title, bare, titleFor(bare))
		}
	}
}

// TestTitleForReadsTheNameRatherThanATable covers the derivation's own edges,
// including the fragments plain capitalisation gets wrong.
func TestTitleForReadsTheNameRatherThanATable(t *testing.T) {
	tests := []struct{ name, want string }{
		{"add_drawer", "Add drawer"},
		{"search", "Search"},
		{"kg_supersede", "Knowledge-graph supersede"},
		{"kg_add", "Knowledge-graph add"},
		{"get_aaak_spec", "Get AAAK spec"},
		{"list_wings", "List wings"},
		{"memories_filed_away", "Memories filed away"},
	}
	for _, tc := range tests {
		if got := titleFor(tc.name); got != tc.want {
			t.Errorf("titleFor(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
