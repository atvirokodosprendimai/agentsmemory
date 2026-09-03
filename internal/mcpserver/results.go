package mcpserver

import (
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
)

// The enumeration results. Every tool that answers "what is in here" returns one
// of these instead of an ad-hoc map, so mcp.WithOutputSchema can generate its
// schema from the very type the handler returns and the two cannot disagree.
//
// ⚠ A TYPE PER TOOL RATHER THAN ONE GENERIC WRAPPER, AND THE JSON KEY IS WHY. The
// obvious shape is listResult[T]{Items []T; Count int}, but each of these answers
// under its own key — "wings", "rooms", "tunnels" — and a generic cannot vary a
// struct tag. Renaming them all to "items" would be a wire break on every
// enumeration tool at once, to save a dozen three-line structs. The repetition is
// the cheaper half of that trade.
//
// ⚠ AND THE COUNT IS NOT REDUNDANT WITH len(). It is what a caller reads to know
// whether a page was cut, and several of these tools bound their result — so it
// stays a field rather than something a client is expected to recompute.

// listedAnchor is one code anchor as am_list_anchors reports it.
//
// ⚠ NOT anchorView, WHICH ALREADY EXISTS AND IS A DIFFERENT SHAPE. That one is
// how SEARCH reports an anchor — path, status, line, checked_at — because a hit
// only needs to say whether its pin still holds. A verifier listing anchors needs
// the id to mark, the drawer it belongs to, the repo label that says whether the
// anchor is even about the tree in front of it, and the snippet to look for. Two
// audiences, two shapes; collapsing them would hand search four fields it has no
// use for and no reason to render.
//
// It replaces an inline map[string]any built at the call site. That map produced
// a schema of "object, no properties", which validates anything and tells a
// caller nothing — the shape TestEveryDeclaredOutputSchemaIsSatisfiedByTheTool
// refuses precisely because it passes while describing nothing.
type listedAnchor struct {
	ID        string `json:"id"`
	DrawerID  string `json:"drawer_id"`
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Snippet   string `json:"snippet"`
	Status    string `json:"status"`
	Line      int    `json:"line"`
	CheckedAt string `json:"checked_at"`
}

// anchorsResult is am_list_anchors' answer.
type anchorsResult struct {
	Anchors []listedAnchor `json:"anchors"`
	Count   int            `json:"count"`
}

// wingsResult is am_list_wings' answer.
type wingsResult struct {
	Wings []palace.WingStat `json:"wings"`
	Count int               `json:"count"`
}

// roomsResult is am_list_rooms' answer.
type roomsResult struct {
	Rooms []palace.RoomStat `json:"rooms"`
	Count int               `json:"count"`
}

// tunnelsResult is am_list_tunnels' answer.
type tunnelsResult struct {
	Tunnels []tunnelView `json:"tunnels"`
	Count   int          `json:"count"`
}

// tunnelRoomsResult is am_find_tunnels' answer: the rooms a tunnel search found.
type tunnelRoomsResult struct {
	Rooms []palace.GraphNode `json:"rooms"`
	Count int                `json:"count"`
}

// connectionsResult is am_follow_tunnels' answer.
//
// ⚠ IT IS NOT am_find_tunnels', AND THE SCHEMA GATE IS WHAT SAID SO. This type
// was first attached to find_tunnels because both tools live in graph.go and both
// answer with a count; find_tunnels answers under "rooms" and follow_tunnels under
// "connections". The mismatch produced "unexpected additional properties [rooms]"
// on the first run — a schema pointing at the wrong tool is exactly the drift
// generating from the returned type is meant to prevent, and it was caught by
// validating the real response rather than by reading the code.
type connectionsResult struct {
	Connections []palace.TunnelConnection `json:"connections"`
	Count       int                       `json:"count"`
}

// skillsResult is am_list_skills' answer.
type skillsResult struct {
	Skills []skill.Summary `json:"skills"`
	Count  int             `json:"count"`
}
