package longmemeval

import "testing"

// TestQueryPolicyVerbatimIsTheQuestion pins the baseline column.
//
// It is also the NAMED ANCHOR that catches a deleted registration:
// TestEveryDeclaredQueryPolicyIsSelectable derives its universe from the
// registry and therefore cannot notice one leaving it, for the reason T2's
// Reachability rung 2 records. The anchor names the baseline deliberately —
// that is the member whose removal would be worst, since every other column's
// delta is measured against it.
func TestQueryPolicyVerbatimIsTheQuestion(t *testing.T) {
	q := fixtureQuestion(t, "q_update_1")
	p, ok := QueryPolicyByName("verbatim")
	if !ok {
		t.Fatal("no query policy named \"verbatim\" is registered — it is the baseline column")
	}
	got := p.Queries(q)
	if len(got) != 1 || got[0] != q.Question {
		t.Errorf("verbatim produced %q, want exactly the question as typed — the baseline column "+
			"adds nothing, or every other column's delta is measured against a head start", got)
	}
}

// TestDecomposedQueryPolicyIsCapped keeps a decomposing policy from buying its
// win with extra retrieval rather than with a better question.
func TestDecomposedQueryPolicyIsCapped(t *testing.T) {
	p, ok := QueryPolicyByName("decomposed")
	if !ok {
		t.Fatal("no query policy named \"decomposed\" is registered")
	}
	q := Question{Question: "a? b? c? d? e?"}
	if got := p.Queries(q); len(got) > decomposedCap {
		t.Errorf("decomposed ran %d searches, over the cap of %d — more retrieval is not a "+
			"prompting rule, and an uncapped column would report one as if it were", len(got), decomposedCap)
	}
}
