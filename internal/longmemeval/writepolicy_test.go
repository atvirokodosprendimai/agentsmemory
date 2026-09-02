package longmemeval

import (
	"slices"
	"strings"
	"testing"
)

// fixtureQuestion returns one loaded question by id, so a policy test states the
// shape it depends on rather than the whole corpus.
func fixtureQuestion(t *testing.T, id string) Question {
	t.Helper()
	ds, err := Load(samplePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return questionByID(t, ds, id)
}

// policyNamed resolves a policy the way the command will, and fails the test if
// the registry has no such name — a per-policy test asserting behaviour is
// worthless if it silently ran against a zero value.
func policyNamed(t *testing.T, name string) WritePolicy {
	t.Helper()
	p, ok := WritePolicyByName(name)
	if !ok {
		t.Fatalf("no write policy named %q is registered", name)
	}
	return p
}

func TestVerbatimPolicyIsOneRecordPerSession(t *testing.T) {
	q := fixtureQuestion(t, "q_update_1")
	got := policyNamed(t, "verbatim").Write(q)

	if len(got) != len(q.Haystack) {
		t.Fatalf("verbatim produced %d records for %d sessions — the baseline is one record per "+
			"session, and a baseline that reshapes the corpus is not a baseline", len(got), len(q.Haystack))
	}
	// Every turn's text must survive: the baseline is what every other policy is a
	// delta against, so content lost here is content no policy can be blamed for.
	for i, rec := range got {
		for _, turn := range q.Haystack[i].Turns {
			if !strings.Contains(rec.Content, turn.Content) {
				t.Errorf("record %d dropped turn text %q — verbatim edits nothing", i, turn.Content)
			}
		}
	}
}

func TestQuestionFirstPolicyOpensWithTheQuestion(t *testing.T) {
	q := fixtureQuestion(t, "q_update_1")
	for i, rec := range policyNamed(t, "question-first").Write(q) {
		first, _, _ := strings.Cut(rec.Content, "\n")
		if !strings.Contains(first, q.Question) {
			t.Errorf("record %d opens with %q, which does not carry the question — this policy IS "+
				"start-here's titling rule, so approximating it measures something else", i, first)
		}
	}
}

func TestOneFactPolicyKeepsEveryAnsweringTurn(t *testing.T) {
	q := fixtureQuestion(t, "q_update_1")
	got := policyNamed(t, "one-fact").Write(q)

	// A policy that quietly drops content looks like a strong compressor under a
	// judged metric. The answering turns are exactly the content whose loss would
	// otherwise be attributed to the policy being "concise".
	for _, s := range q.Haystack {
		for _, turn := range s.Turns {
			if !turn.HasAnswer {
				continue
			}
			var seen bool
			for _, rec := range got {
				if strings.Contains(rec.Content, turn.Content) {
					seen = true
					break
				}
			}
			if !seen {
				t.Errorf("one-fact dropped an answering turn from session %s: %q", s.ID, turn.Content)
			}
		}
	}
}

func TestBoundedPolicySplitsAtTheThreshold(t *testing.T) {
	q := fixtureQuestion(t, "q_update_1")
	for _, rec := range policyNamed(t, "bounded").Write(q) {
		// Runes, not bytes: the skills state the threshold in runes, and a byte
		// count would split a multi-byte corpus at a different place than the rule
		// the policy exists to test.
		if n := len([]rune(rec.Content)); n > BoundedRunes {
			t.Errorf("bounded produced a %d-rune record, over the %d threshold it exists to apply",
				n, BoundedRunes)
		}
	}
}

func TestEveryPolicyIsDeterministic(t *testing.T) {
	q := fixtureQuestion(t, "q_update_1")
	for _, p := range WritePolicies() {
		first, second := p.Write(q), p.Write(q)
		if len(first) != len(second) {
			t.Fatalf("policy %q returned %d then %d records for one question", p.Name, len(first), len(second))
		}
		for i := range first {
			// Compared field by field rather than with ==: Record carries a slice,
			// and the provenance is part of what has to be reproducible — a run whose
			// AnsweringTurns moved would score a different retrieval column from the
			// same text.
			if first[i].Room != second[i].Room ||
				first[i].Content != second[i].Content ||
				first[i].SessionID != second[i].SessionID ||
				!slices.Equal(first[i].AnsweringTurns, second[i].AnsweringTurns) {
				t.Errorf("policy %q is not deterministic at record %d — a cell that cannot be "+
					"reproduced cannot be compared with a later run", p.Name, i)
			}
		}
	}
}
