package mcpserver

import (
	"context"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerBootstrap exposes the one call that replaces the client-side protocol.
//
// The reg.add is what makes it reachable AND what puts it in the catalogue. A
// bootstrap nobody can discover is the 13-call protocol it was written to
// replace, wearing a different name.
func registerBootstrap(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("bootstrap",
		mcp.WithDescription("Start work in a wing with ONE call. Returns the wing's entry point, "+
			"the eager tier's content inline, the entry room's overflow as on_demand pointers "+
			"with the call that fetches them, the must and ref tier authored on the wing's "+
			"by-name root as `tiers` — pointers only, each with the edge's `hint` and the "+
			"namespace it hangs `under`, never inline — corrections already swept "+
			"server-side, the resolved wing, and a truncation report whose `tiers_omitted` "+
			"counts tier leaves refused, past the bound, or on a node that overflowed a graph "+
			"page. Replaces a client-side protocol of 13 calls that also needed a hardcoded "+
			"root drawer id. A wing with no entry point still bootstraps, and a wing with no "+
			"tier serves none."),
		mcp.WithString("wing", mcp.Required(), mcp.Description("The wing to bootstrap.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		res, err := drawers.Bootstrap(ctx, t.TeamID, req.GetString("wing", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// The SAME shape the cost gate measures. Two hand-written maps drift, and
		// the drift is invisible: the gate keeps passing while the response it
		// claims to measure grows.
		return jsonResult(res.WireShape()), nil
	})
}
