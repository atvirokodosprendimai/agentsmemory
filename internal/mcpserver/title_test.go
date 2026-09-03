package mcpserver

import "testing"

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
