package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
)

// TestEveryWriteToolStatesWhatARetryDoes derives its universe from the catalog's
// own Write flag, so a write tool added tomorrow joins this check on the same
// commit rather than being remembered into it.
//
// A timed-out write is indistinguishable from a refused one, and the caller must
// then choose between losing the write and duplicating it. Three of four
// concurrent sessions hit this against one palace on 2026-08-31 (#152); the
// sharpest case was am_merge_wing reporting `The operation timed out.` on a merge
// that had committed. One session retried an am_add_drawer and was right — but on
// the strength of "re-adding the same source is idempotent" written in a
// different tool's description, which is inference, not evidence.
//
// The contract already existed as MCP hints. This asserts it also reaches the
// DESCRIPTION, because the hints are structured metadata a client may never
// surface, while the description is what an agent reads before it decides.
func TestEveryWriteToolStatesWhatARetryDoes(t *testing.T) {
	catalog, live := liveSurface(t, false)
	descByName := make(map[string]string, len(live))
	for _, tool := range live {
		descByName[tool.Name] = tool.Description
	}
	writes := 0
	for _, entry := range catalog {
		if !entry.Write {
			continue
		}
		writes++
		short := strings.TrimPrefix(entry.Name, mcpprotocol.ToolPrefix)
		// The description as the LIVE surface serves it, not the catalog's copy:
		// the sentence is appended in classifyTool, so reading the catalog entry
		// alone would test the string this file already believes.
		desc := descByName[entry.Name]
		if !strings.Contains(desc, "IF THIS CALL DOES NOT ANSWER") {
			t.Errorf("%s writes and its description never says what a retry does. A caller holding "+
				"a timeout has to choose between losing the write and duplicating it, and nothing "+
				"in front of them decides it", entry.Name)
			continue
		}
		// The sentence must MATCH the tool's declared semantics: a generated
		// sentence that always said "safe" would satisfy the check above while
		// telling a caller to repeat a merge.
		s, declared := writeToolSemantics[short]
		if !declared {
			t.Errorf("%s is registered as a write tool but declares no semantics, so its retry "+
				"sentence is generated from nothing", entry.Name)
			continue
		}
		safe := strings.Contains(desc, "RETRYING IS SAFE")
		if safe != s.idempotent {
			t.Errorf("%s advertises retry-safe=%v while writeToolSemantics declares idempotent=%v; "+
				"the sentence and the hint describe the same call and must not disagree",
				entry.Name, safe, s.idempotent)
		}
	}
	if writes == 0 {
		t.Fatal("no write tools were examined, so this gate passed without looking at anything")
	}
	t.Logf("examined %d write tool(s)", writes)
}

// TestANonIdempotentWriteWarnsRatherThanReassures pins the branch that matters,
// over the tool the incident was reported against. A gate that only counts
// sentences cannot tell the two contracts apart, and the wrong one here is the
// expensive direction: telling a caller a merge is safe to repeat.
func TestANonIdempotentWriteWarnsRatherThanReassures(t *testing.T) {
	_, live := liveSurface(t, false)
	desc := ""
	for _, tool := range live {
		if tool.Name == mcpprotocol.ToolPrefix+"merge_wing" {
			desc = tool.Description
		}
	}
	if desc == "" {
		t.Fatal("am_merge_wing is not registered — this check has stopped checking anything")
	}
	if !strings.Contains(desc, "DO NOT RETRY BLINDLY") {
		t.Errorf("am_merge_wing is declared non-idempotent and destructive, and its description "+
			"does not warn against a blind retry:\n%s", desc)
	}
	if strings.Contains(desc, "RETRYING IS SAFE") {
		t.Errorf("am_merge_wing tells a caller a retry is safe:\n%s", desc)
	}
}

// TestKgAddIsOnlyANoOpForACurrentFact grounds one retry claim in BEHAVIOUR
// rather than in the map that generates the sentence.
//
// ⚠ THE GATE ABOVE CANNOT CATCH A WRONG CLAIM. It compares the description
// against writeToolSemantics — the same map the sentence is generated from — so a
// map entry that lies about the handler produces a confident sentence and a green
// test. Review caught exactly that: kg_add was declared idempotent, and the no-op
// only covers a fact with no valid_to, because CurrentTripleID matches on
// valid_to = ”. A fact filed with a closed window is inserted again on retry
// under a fresh time-derived id, so the advice a caller had just been given —
// retrying is safe — was wrong for the historical form of the same call.
//
// This test is what makes the classification a fact about the code.
func TestKgAddIsOnlyANoOpForACurrentFact(t *testing.T) {
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	const team = "team-kgretry"
	ctx := context.Background()

	first, err := drawers.KGAdd(ctx, team, "svc", "runs_on", "hostA", "", "", "", "", "")
	if err != nil {
		t.Fatalf("kg add: %v", err)
	}
	again, err := drawers.KGAdd(ctx, team, "svc", "runs_on", "hostA", "", "", "", "", "")
	if err != nil {
		t.Fatalf("kg add (retry): %v", err)
	}
	if first.TripleID != again.TripleID {
		t.Errorf("retrying a CURRENT fact minted a second triple (%s then %s); the description "+
			"promises a no-op there", first.TripleID, again.TripleID)
	}

	// A CLOSED window is a different call, and it is not a no-op either way. The
	// id is derived from the time of writing, so a retry lands as a UNIQUE
	// constraint failure inside the same second and as a SECOND ROW across it —
	// measured here, and the constraint error is what this fixture reproduces.
	// Neither outcome is what "retrying is safe" tells a caller to expect.
	one, err := drawers.KGAdd(ctx, team, "svc", "ran_on", "hostB", "2026-01-01", "2026-02-01", "", "", "")
	if err != nil {
		t.Fatalf("kg add (closed): %v", err)
	}
	two, retryErr := drawers.KGAdd(ctx, team, "svc", "ran_on", "hostB", "2026-01-01", "2026-02-01", "", "", "")
	sameFact := retryErr == nil && two.TripleID == one.TripleID
	if sameFact {
		t.Skip("a closed-window fact now dedupes like a current one; if that is deliberate, " +
			"kg_add can go back to idempotent: true and this test should assert the new behaviour")
	}

	// The tool must therefore not promise a safe retry.
	srv := New(Deps{})
	tool := srv.GetTool(mcpprotocol.ToolPrefix + "kg_add")
	if tool == nil {
		t.Fatal("am_kg_add is not registered")
	}
	if !strings.Contains(tool.Tool.Description, "DO NOT RETRY BLINDLY") {
		t.Errorf("a repeated closed-window fact %s, and am_kg_add still tells the caller a retry "+
			"is safe:\n%s", outcome(retryErr), tool.Tool.Description)
	}
}

// outcome names what a repeat actually did, so the failure message reports the
// observed behaviour rather than the one this test expected.
func outcome(err error) string {
	if err != nil {
		return "fails with " + err.Error()
	}
	return "inserts a second row"
}
