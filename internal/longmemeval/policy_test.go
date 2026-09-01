package longmemeval

import (
	"strings"
	"testing"
)

// TestEveryDeclaredPolicyIsSelectable is the reachability gate ADR-047 names in
// its Enforced-by header.
//
// It derives its universe from the registry rather than from a list written
// beside it, so a policy registered tomorrow joins the check on the same commit.
// A checklist would not: this repository's signature defect is a capability that
// is finished, tested and selected by nothing, and a table with a missing row
// reads as a policy that lost rather than as a policy that never ran.
func TestEveryDeclaredPolicyIsSelectable(t *testing.T) {
	policies := WritePolicies()
	if len(policies) == 0 {
		t.Fatal("the registry is empty, so every assertion below is vacuous — a gate that passes " +
			"over nothing is the failure this test exists to prevent")
	}
	usage := WritePolicyUsage()
	for _, p := range policies {
		got, ok := WritePolicyByName(p.Name)
		if !ok {
			t.Errorf("policy %q is registered but does not resolve by name — nothing can select it", p.Name)
			continue
		}
		if got.Name != p.Name {
			t.Errorf("WritePolicyByName(%q) returned %q", p.Name, got.Name)
		}
		// Rung 3: the caller has to be able to DISCOVER the policy, not merely
		// select it once told the name. T4's flag is required to render its allowed
		// values from this function rather than formatting its own list, so a
		// policy absent here is one no --help will ever mention.
		if !strings.Contains(usage, p.Name) {
			t.Errorf("policy %q is missing from WritePolicyUsage(), so no --help text derived from "+
				"it can name the policy: %s", p.Name, usage)
		}
	}
}

// TestEveryPolicyPreservesSessionProvenance gates the contract T4's retrieval
// column is scored through.
//
// Found in review of PR #148: T2 defined a policy's output as room-plus-content
// and T4 required a column scored against answer_session_ids, and neither task
// could see that the first made the second impossible. Order cannot recover the
// mapping, because one-fact and bounded change the record count and duplicate
// content is legal — so the source session has to travel with the record.
func TestEveryPolicyPreservesSessionProvenance(t *testing.T) {
	q := fixtureQuestion(t, "q_update_1")
	inHaystack := map[string]bool{}
	answering := map[string]map[int]bool{}
	for _, s := range q.Haystack {
		inHaystack[s.ID] = true
		for i, turn := range s.Turns {
			if turn.HasAnswer {
				if answering[s.ID] == nil {
					answering[s.ID] = map[int]bool{}
				}
				answering[s.ID][i] = true
			}
		}
	}
	if len(answering) == 0 {
		t.Fatal("the fixture question has no answering turn, so the coverage half of this test " +
			"would pass over nothing")
	}

	for _, p := range WritePolicies() {
		records := p.Write(q)
		if len(records) == 0 {
			t.Errorf("policy %q produced no records", p.Name)
			continue
		}
		covered := map[string]map[int]bool{}
		for i, rec := range records {
			if !inHaystack[rec.SessionID] {
				t.Errorf("policy %q record %d names session %q, which is not in the question's "+
					"haystack — the retrieval column cannot score a record whose source is unknown",
					p.Name, i, rec.SessionID)
				continue
			}
			for _, turn := range rec.AnsweringTurns {
				if covered[rec.SessionID] == nil {
					covered[rec.SessionID] = map[int]bool{}
				}
				covered[rec.SessionID][turn] = true
			}
		}
		for sid, turns := range answering {
			for idx := range turns {
				if !covered[sid][idx] {
					t.Errorf("policy %q loses answering turn %d of session %s: no record claims it, "+
						"so that evidence is unscoreable for this policy alone and the column would "+
						"read as the policy failing to retrieve", p.Name, idx, sid)
				}
			}
		}
	}
}
