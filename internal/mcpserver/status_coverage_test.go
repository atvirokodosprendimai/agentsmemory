package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestAmStatusReportsCoverage: am_status is the one call the protocol mandates
// first, so it is where serving coverage must be. A palace whose only memory is
// a closet awaiting its first embedding reads 1.0 — vacuously healthy, never a
// division error — while the raw per-namespace fields still show the queue.
func TestAmStatusReportsCoverage(t *testing.T) {
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	srv := New(Deps{
		Drawers: drawers,
		Usage:   usage.NewService(usage.NewRepo(gdb), graphTestCaps{}),
	})
	const team = "team-coverage"

	ctx := context.Background()
	// A pending closet: a row, no vector yet, a queue rather than a fault.
	if err := drawers.Repo().SaveClosetsUnembedded(ctx, []palace.Closet{{
		TeamID: team, ID: "c-pending", Wing: "alpha", Room: "db",
		SourceFile: "s.md", Document: "packed doc", Entities: []string{"E"}, FiledAt: "2026-01-01T00:00:00Z",
	}}); err != nil {
		t.Fatalf("seed pending closet: %v", err)
	}

	st := srv.GetTool(mcpprotocol.ToolPrefix + "status")
	if st == nil {
		t.Fatal("am_status is not registered — this check has stopped checking anything")
	}
	callCtx := auth.WithTenant(context.Background(), tenant.Tenant{
		TeamID: team, UserID: "u1", Role: tenant.RoleAdmin,
	})
	res, err := st.Handler(callCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: mcpprotocol.ToolPrefix + "status"},
	})
	if err != nil {
		t.Fatalf("am_status: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(errText(res)), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	raw, ok := body["coverage"]
	if !ok {
		t.Fatal("am_status carries no coverage block")
	}
	var cov struct {
		Coverage   float64 `json:"coverage"`
		Namespaces map[string]struct {
			Coverage    float64 `json:"coverage"`
			Expected    int     `json:"expected"`
			Indexed     int     `json:"indexed"`
			Missing     int     `json:"index_missing"`
			Mislabelled int     `json:"index_mislabelled"`
		} `json:"namespaces"`
		Pending struct {
			Drawers int `json:"drawers"`
			Closets int `json:"closets"`
		} `json:"pending_embedding"`
	}
	if err := json.Unmarshal(raw, &cov); err != nil {
		t.Fatalf("decode coverage block: %v", err)
	}
	if cov.Coverage != 1.0 {
		t.Fatalf("pending-only palace coverage = %v, want 1.0 (vacuously healthy, not a division error)", cov.Coverage)
	}
	if cov.Pending.Closets != 1 {
		t.Fatalf("pending closets = %d, want 1 — the queue must be visible in the mandated first call", cov.Pending.Closets)
	}
	if cov.Namespaces["closets"].Expected != 0 {
		t.Fatalf("closets expected = %d, want 0 (pending excluded by construction)", cov.Namespaces["closets"].Expected)
	}
}

// TestFailedDriftAuditRendersNoCoverageNumber: a coverage number that could not
// be taken must not render. A zero DriftReport's Coverage() reads 1.0 —
// indistinguishable from genuine health, in exactly the state (palace in
// trouble) where the wake-up call matters most. Same Known discipline as the
// inbox block; this test goes red if the error path ever paints a number.
func TestFailedDriftAuditRendersNoCoverageNumber(t *testing.T) {
	got := coverageBlockFor(palace.DriftReport{}, errors.New("truth down"))
	if known, _ := got["known"].(bool); known {
		t.Fatal("failed audit reported known: true — a number that could not be taken reads as health")
	}
	for _, key := range []string{"coverage", "namespaces", "pending_embedding"} {
		if _, ok := got[key]; ok {
			t.Fatalf("failed audit rendered %q — must be omitted, not painted as a value", key)
		}
	}
	if got["note"] == nil {
		t.Fatal("failed audit carries no note — the caller cannot tell all-clear from unknown")
	}

	healthy := coverageBlockFor(palace.DriftReport{}, nil)
	if known, _ := healthy["known"].(bool); !known {
		t.Fatal("healthy audit reported known: false")
	}
	if _, ok := healthy["coverage"]; !ok {
		t.Fatal("healthy audit rendered no coverage number")
	}
}

// TestAmStatusServesTheEntryProtocolPointer is the SELECTION rung, and it is a
// different question from TestStatusHintNamesTheEntryProtocolWhenOneExists.
//
// ⚠ THAT TEST CALLS statusHint DIRECTLY, so it stays green while the served
// am_status response says nothing — the component exercised instead of the
// selection, which is this repository's signature defect. This one drives the
// real registered handler, with a real skill in a real workspace, and reads the
// hint out of the JSON an agent would actually receive.
//
// The compiler catches one severing (dropping `entrySkillNames` from the call
// leaves it declared and not used) but not the other: deleting the lookup and
// passing nil compiles perfectly and goes silent. This is the test for that.
func TestAmStatusServesTheEntryProtocolPointer(t *testing.T) {
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	skills := skill.NewService(skill.NewRepo(gdb))
	srv := New(Deps{
		Drawers: drawers,
		Skills:  skills,
		Usage:   usage.NewService(usage.NewRepo(gdb), graphTestCaps{}),
	})
	const team = "team-entryskill"
	ctx := context.Background()
	callCtx := auth.WithTenant(ctx, tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})

	hintOf := func(t *testing.T) string {
		t.Helper()
		st := srv.GetTool(mcpprotocol.ToolPrefix + "status")
		if st == nil {
			t.Fatal("am_status is not registered — this check has stopped checking anything")
		}
		res, err := st.Handler(callCtx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: mcpprotocol.ToolPrefix + "status"},
		})
		if err != nil {
			t.Fatalf("am_status: %v", err)
		}
		var body struct {
			Hint string `json:"hint"`
		}
		if err := json.Unmarshal([]byte(errText(res)), &body); err != nil {
			t.Fatalf("decode status: %v", err)
		}
		return body.Hint
	}

	// Before: no entry protocol in this workspace, so no sentence about one.
	if before := hintOf(t); strings.Contains(before, EntrySkill) {
		t.Errorf("a workspace with no entry protocol is told to load one:\n%s", before)
	}

	// Seeded through the repo, not the service: this test is about what am_status
	// SERVES, and routing the fixture through skill authorization would make an
	// authz change able to break a hint test for reasons that are not about hints.
	if _, err := skill.NewRepo(gdb).Upsert(ctx, team, EntrySkill,
		"the team's entry protocol", "# start-here\n\nbody", "u1"); err != nil {
		t.Fatalf("seed the entry skill: %v", err)
	}

	after := hintOf(t)
	if !strings.Contains(after, EntrySkill) {
		t.Errorf("the SERVED am_status hint never names %q, so the one call every session makes "+
			"still does not point at the entry protocol:\n%s", EntrySkill, after)
	}
	if !strings.Contains(after, "am_load_skill") {
		t.Errorf("the served hint names the skill but not the call that loads it:\n%s", after)
	}
}
