package longmemeval

import (
	"strings"
	"testing"
)

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

// TestNamedThingStripsTheConversationalFrame pins what "name the thing" means
// for this corpus.
//
// Measured over all 500 questions on 2026-09-01: 480 are first-person, and the
// haystack is first-person too, so those pronouns appear on both sides of every
// comparison and separate nothing. 17 of the 30 preference items are requests
// rather than questions, where the frame is the whole sentence.
func TestNamedThingStripsTheConversationalFrame(t *testing.T) {
	p, ok := QueryPolicyByName("named-thing")
	if !ok {
		t.Fatal("no query policy named \"named-thing\" is registered")
	}
	for _, tc := range []struct {
		question string
		want     []string // content words that must survive
		gone     []string // frame that must not
	}{
		{
			question: "What degree did I graduate with?",
			want:     []string{"degree", "graduate"},
			gone:     []string{"what", "did", "i"},
		},
		{
			// The case that motivates the policy: asked verbatim this searches on
			// "thinking", "trying" and "recommendations".
			question: "I was thinking of trying a new coffee creamer recipe. Any recommendations?",
			want:     []string{"coffee", "creamer", "recipe"},
			gone:     []string{"thinking", "trying", "recommendations"},
		},
		{
			question: "How many months ago did I book the Airbnb in San Francisco?",
			want:     []string{"months", "book", "airbnb", "san", "francisco"},
			gone:     []string{"how", "many", "did", "i", "the", "in"},
		},
	} {
		got := p.Queries(Question{Question: tc.question})
		if len(got) != 1 {
			t.Fatalf("named-thing returned %d queries for %q, want 1", len(got), tc.question)
		}
		words := strings.Fields(got[0])
		has := map[string]bool{}
		for _, w := range words {
			has[w] = true
		}
		for _, w := range tc.want {
			if !has[w] {
				t.Errorf("%q lost content word %q → %q", tc.question, w, got[0])
			}
		}
		for _, w := range tc.gone {
			if has[w] {
				t.Errorf("%q kept frame word %q → %q", tc.question, w, got[0])
			}
		}
	}
}

// TestNamedThingKeepsTemporalRelations is the other half, and it is why this is
// not a generic stopword list.
//
// 133 of the 500 questions are temporal-reasoning, and they are ABOUT ordering:
// a list that stripped "first", "before" or "last" would make this policy worse
// than verbatim on a quarter of the corpus while looking like a tidy-up.
func TestNamedThingKeepsTemporalRelations(t *testing.T) {
	p, _ := QueryPolicyByName("named-thing")
	got := p.Queries(Question{
		Question: "Which event did I participate in first, the charity gala or the charity bake sale?",
	})[0]
	for _, w := range []string{"first", "charity", "gala", "bake", "sale"} {
		if !strings.Contains(got, w) {
			t.Errorf("stripped %q, which a temporal question is about: %q", w, got)
		}
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
