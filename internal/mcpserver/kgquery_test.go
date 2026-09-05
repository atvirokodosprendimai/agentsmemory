package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// kgQueryTestTeam is the tenant every case in this file runs as.
const kgQueryTestTeam = "team-kgquery"

// kgToolServer stands the real KG tools up over a migrated palace holding one
// entity with one retracted fact and one live one — the smallest graph in which
// "which half did you return" is a question with different answers.
//
// It reuses the throwaway-palace helpers the graph tests already established
// rather than standing up a second harness.
func kgToolServer(t *testing.T) *server.MCPServer {
	t.Helper()
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb, gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	ctx := context.Background()

	if _, err := drawers.KGAdd(ctx, kgQueryTestTeam, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("seed ended-to-be: %v", err)
	}
	if _, err := drawers.KGAdd(ctx, kgQueryTestTeam, "Alice", "works at", "Globex", "2025-06-01", "", "", "", ""); err != nil {
		t.Fatalf("seed survivor: %v", err)
	}
	if _, _, _, err := drawers.KGInvalidate(ctx, kgQueryTestTeam, "Alice", "works at", "Acme", "2025-06-01", "she left"); err != nil {
		t.Fatalf("seed invalidate: %v", err)
	}

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	registerKG(&registrar{srv: srv}, drawers, usage.NewService(usage.NewRepo(gdb), graphTestCaps{}))
	return srv
}

// callKGQuery drives the REGISTERED kg_query handler and decodes its wire body.
// Going through the registration rather than palace.KGQuery is the point: this
// file's job is to check what an agent actually receives.
func callKGQuery(t *testing.T, srv *server.MCPServer, args map[string]any) map[string]json.RawMessage {
	t.Helper()
	const name = mcpprotocol.ToolPrefix + "kg_query"

	st := srv.GetTool(name)
	if st == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", name)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{
		TeamID: kgQueryTestTeam, UserID: "u1", Role: tenant.RoleAdmin,
	})
	res, err := st.Handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("kg_query: %v", err)
	}
	body := errText(res)
	if res.IsError {
		t.Fatalf("kg_query returned an error result: %s", body)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("kg_query did not return a JSON object: %v\n  %s", err, body)
	}
	return fields
}

// TestDefaultQueryIsCurrentOnly is ADR-026 T4's gate: the BREAKING half.
//
// It names no status, which is what every existing caller does and what this
// repo's own llm_init bootstrap instructs every session to write. Before T4 that
// call returned every fact ever recorded about the entity, retracted ones tagged
// current:false and left for the reader to honour — an agent that acted on one was
// wrong, and nothing on the server stopped it.
//
// The assertion is on the RETRACTED fact being absent, not on the count, because a
// count is satisfied by any two facts and this needs to be the right one missing.
// The withheld keys are asserted too: a default that hides history silently is the
// version ADR-010 rejects, so "filtered" and "said so" are one behaviour and one
// gate, never two that can drift apart.
func TestDefaultQueryIsCurrentOnly(t *testing.T) {
	srv := kgToolServer(t)
	fields := callKGQuery(t, srv, map[string]any{"entity": "Alice"})

	var status string
	if err := json.Unmarshal(fields["status"], &status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != palace.KGStatusCurrent {
		t.Fatalf("the shipped default is %q, want %q", status, palace.KGStatusCurrent)
	}

	var facts []palace.KGFact
	if err := json.Unmarshal(fields["facts"], &facts); err != nil {
		t.Fatalf("facts: %v", err)
	}
	for _, f := range facts {
		if f.Object == "Acme" {
			t.Errorf("the retracted fact is still in the default response: %+v", f)
		}
		if !f.Current {
			t.Errorf("a non-current fact reached the default response: %+v", f)
		}
	}
	if len(facts) != 1 {
		t.Fatalf("expected only the open-ended fact, got %d: %+v", len(facts), facts)
	}

	// Hiding it silently is the version ADR-010 rejects.
	raw, ok := fields["withheld"]
	if !ok {
		t.Fatal("the new default filtered a fact and did not say so")
	}
	var withheld map[string]int64
	if err := json.Unmarshal(raw, &withheld); err != nil {
		t.Fatalf("withheld: %v", err)
	}
	if withheld[palace.KGStatusEnded] != 1 {
		t.Errorf("withheld = %v, want one ended fact", withheld)
	}
	if _, ok := fields["hint"]; !ok {
		t.Error("no hint names the parameter that restores the hidden history")
	}
}

// TestPredicateEntryPointIsReachableFromTheTool is T5's reachability gate, and it
// guards this repository's signature defect rather than the feature.
//
// palace.KGQuery accepting a predicate with no entity is worth nothing if the tool
// still declares entity as Required — the capability would be finished, tested,
// and impossible to invoke. Four capabilities shipped exactly that way in one week
// here, every one of them with passing tests, because the tests exercised the
// component and never the selection. So this calls the REGISTERED handler with no
// entity at all, and separately reads the declared schema: a caller must be able
// to learn from the tool description that entity is optional, not discover it by
// guessing.
func TestPredicateEntryPointIsReachableFromTheTool(t *testing.T) {
	srv := kgToolServer(t)

	// status is named explicitly: this test is about the entry point being
	// reachable, and leaving the default in would silently couple it to T4.
	fields := callKGQuery(t, srv, map[string]any{"predicate": "works at", "status": palace.KGStatusAll})

	var count int
	if err := json.Unmarshal(fields["count"], &count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("predicate alone must return both works_at facts, got %d: %v", count, fields)
	}
	if _, ok := fields["entity"]; ok {
		t.Error("no entity was asked for, so none should be echoed")
	}
	var predicate string
	if err := json.Unmarshal(fields["predicate"], &predicate); err != nil {
		t.Fatalf("predicate echo: %v", err)
	}
	if predicate != "works_at" {
		t.Errorf("predicate echoed as %q, want the normalized %q", predicate, "works_at")
	}

	// The schema half: entity must not be advertised as required.
	tool := srv.GetTool(mcpprotocol.ToolPrefix + "kg_query")
	if tool == nil {
		t.Fatal("kg_query is not registered")
	}
	for _, req := range tool.Tool.InputSchema.Required {
		if req == "entity" {
			t.Error("kg_query still declares entity as required, so the predicate entry point is unreachable " +
				"for any caller that trusts the schema — finished and unselectable is this repo's recurring defect")
		}
	}

	// And a call naming neither entry point must be refused rather than dumping
	// every fact the tenant owns.
	const name = mcpprotocol.ToolPrefix + "kg_query"
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{
		TeamID: kgQueryTestTeam, UserID: "u1", Role: tenant.RoleAdmin,
	})
	res, err := tool.Handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("empty call: %v", err)
	}
	if !res.IsError {
		t.Errorf("a query naming neither entity nor predicate must be refused, got: %s", errText(res))
	}
}

// TestFilteredResponseReportsWhatItWithheld is ADR-026 T3's gate.
//
// It asserts the withheld NUMBER, not the presence of the field, and it reads it
// off the WIRE rather than off KGQueryResult. Both choices are the same lesson:
// printSupersessionGate computed a near-miss explanation and discarded it for
// weeks — 246 characters produced, 0 printed — and only a test that read the
// value caught it. A gate asserting `withheld != nil` passes on a hard-coded
// zero, and a gate reading the struct passes on a handler that never serialises
// it.
func TestFilteredResponseReportsWhatItWithheld(t *testing.T) {
	srv := kgToolServer(t)

	for _, tc := range []struct {
		name         string
		status       string
		wantCount    int
		wantWithheld map[string]int64
	}{
		{
			name:         "current hides the one retracted fact and says so",
			status:       palace.KGStatusCurrent,
			wantCount:    1,
			wantWithheld: map[string]int64{palace.KGStatusEnded: 1},
		},
		{
			name:         "ended hides the one live fact and says so",
			status:       palace.KGStatusEnded,
			wantCount:    1,
			wantWithheld: map[string]int64{palace.KGStatusCurrent: 1},
		},
		{
			// Nothing was removed, so the keys must be ABSENT rather than zero.
			// Their presence is itself the signal that something is missing; a
			// withheld:{} on every response trains the reader to ignore it.
			name:      "all withholds nothing and stays silent about it",
			status:    palace.KGStatusAll,
			wantCount: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := callKGQuery(t, srv, map[string]any{"entity": "Alice", "status": tc.status})

			var count int
			if err := json.Unmarshal(fields["count"], &count); err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != tc.wantCount {
				t.Errorf("count = %d, want %d", count, tc.wantCount)
			}

			var status string
			if err := json.Unmarshal(fields["status"], &status); err != nil {
				t.Fatalf("status: %v", err)
			}
			if status != tc.status {
				t.Errorf("status echoed as %q, want %q — a caller cannot tell what was applied", status, tc.status)
			}

			raw, present := fields["withheld"]
			if tc.wantWithheld == nil {
				if present {
					t.Errorf("withheld is present with nothing withheld: %s", raw)
				}
				if _, ok := fields["hint"]; ok {
					t.Error("hint is present with nothing withheld")
				}
				return
			}
			if !present {
				t.Fatalf("withheld is absent though %v was filtered out — computed and never printed is this repo's recurring shape", tc.wantWithheld)
			}
			var got map[string]int64
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("withheld: %v", err)
			}
			if len(got) != len(tc.wantWithheld) {
				t.Fatalf("withheld = %v, want %v", got, tc.wantWithheld)
			}
			for k, want := range tc.wantWithheld {
				if got[k] != want {
					t.Errorf("withheld[%q] = %d, want %d", k, got[k], want)
				}
			}
			if _, ok := fields["hint"]; !ok {
				t.Error("something was withheld and no hint names the parameter that restores it")
			}
		})
	}
}
