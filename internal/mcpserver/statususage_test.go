package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	mcpprotocol "github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
	"github.com/mark3labs/mcp-go/mcp"
)

// cappedAt reports a fixed monthly ceiling, so the served response can be read
// under a real cap as well as under an unlimited one.
type cappedAt int

// MonthlyCap reports the ceiling this stub was built with.
func (c cappedAt) MonthlyCap(_ context.Context, _ string) (int, error) { return int(c), nil }

// servedRemaining drives the REGISTERED am_status handler and returns the raw
// JSON of usage.remaining, exactly as an agent receives it.
func servedRemaining(t *testing.T, caps usage.CapLookup, team string) string {
	t.Helper()
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	srv := New(Deps{
		Drawers: drawers,
		Skills:  skill.NewService(skill.NewRepo(gdb)),
		Usage:   usage.NewService(usage.NewRepo(gdb), caps),
	})
	st := srv.GetTool(mcpprotocol.ToolPrefix + "status")
	if st == nil {
		t.Fatal("am_status is not registered — this check has stopped checking anything")
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})
	res, err := st.Handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: mcpprotocol.ToolPrefix + "status"},
	})
	if err != nil {
		t.Fatalf("am_status: %v", err)
	}
	var body struct {
		Usage struct {
			Cap       json.RawMessage `json:"monthly_cap"`
			Remaining json.RawMessage `json:"remaining"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(errText(res)), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if string(body.Usage.Cap) == "" {
		t.Fatal("the served response carries no monthly_cap, so there is nothing to disagree with")
	}
	return string(body.Usage.Remaining)
}

// TestAmStatusServesNoRemainderOnAnUnlimitedCap is the SELECTION rung for #153,
// and it exists because the unit test next door is not one.
//
// TestAnUnlimitedCapReportsNoRemainderRatherThanZero marshals RemainingReported
// directly, so reverting server.go to st.Remaining() leaves it green while the
// wire goes back to `monthly_cap: -1, remaining: 0` — the component exercised
// instead of the selection, which is this repository's signature defect and the
// one TestAmStatusServesTheEntryProtocolPointer already records one file over.
// Caught in review before this shipped.
func TestAmStatusServesNoRemainderOnAnUnlimitedCap(t *testing.T) {
	if got := servedRemaining(t, graphTestCaps{}, "team-unlimited"); got != "null" {
		t.Errorf("am_status served remaining=%s beside an unlimited monthly_cap. An agent reading "+
			"a number there cannot tell an unlimited plan from an exhausted one, and the "+
			"conservative reading — stop writing — is the wrong one", got)
	}
}

// TestAmStatusStillServesTheRemainderOfARealCap keeps the fix from becoming a
// field that says nothing. Always-null satisfies the test above and takes the
// number away from every plan that has one.
func TestAmStatusStillServesTheRemainderOfARealCap(t *testing.T) {
	got := servedRemaining(t, cappedAt(500), "team-capped")
	if got == "null" || got == "" {
		t.Fatalf("am_status served remaining=%q under a cap of 500; the field is now silent on the "+
			"only plans where it has something to say", got)
	}
}
