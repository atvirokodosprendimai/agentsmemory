package mcpserver

import (
	"context"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerMine wires the mine tool: an agent submits a blob of text and the server
// chunks it into verbatim drawers (with entities + a content date) and builds the
// closet pointer index over them. Mining is idempotent by `source` — re-mining the
// same source replaces its drawers and closets. Scoped to the resolved tenant.
func registerMine(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("mine",
		mcp.WithDescription("Mine a blob of text into the palace: chunk it into verbatim drawers (with extracted entities and a detected date) and build the closet index. Idempotent by source — re-mining the same source replaces it."),
		mcp.WithString("content", mcp.Required(), mcp.Description("The verbatim text to mine. Stored exactly, never summarised.")),
		mcp.WithString("wing", mcp.Required(), mcp.Description("Project namespace the mined memory belongs to.")),
		mcp.WithString("source", mcp.Required(), mcp.Description("A stable identifier for this content (a path, URL, or label). Re-mining the same source replaces its drawers and closets.")),
		mcp.WithString("room", mcp.Description("Aspect within the wing (default \"general\").")),
		mcp.WithString("agent", mcp.Description("Author recorded on each drawer (default \"mempalace\").")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		wing, err := req.RequireString("wing")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		source, err := req.RequireString("source")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := drawers.Mine(ctx, t.TeamID, palace.MineInput{
			Content: content,
			Wing:    wing,
			Source:  source,
			Room:    req.GetString("room", ""),
			Agent:   req.GetString("agent", ""),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Flatten {ok:true} over the result's fields for a stable wire shape.
		return jsonResult(struct {
			OK bool `json:"ok"`
			palace.MineResult
		}{OK: true, MineResult: res}), nil
	})
}
