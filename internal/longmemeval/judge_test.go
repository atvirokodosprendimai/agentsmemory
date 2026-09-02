package longmemeval

import (
	"strings"
	"testing"
)

// TestJudgeNeverSeesThePolicyName is the blindness gate ADR-047 property 4
// names.
//
// The judge prompt is the one place a preferred outcome could be smuggled in for
// free, so the rendered prompt is asserted to contain no registered policy name
// — derived from the registries rather than from a list, so a policy added later
// is checked on the same commit.
func TestJudgeNeverSeesThePolicyName(t *testing.T) {
	prompt := JudgePrompt(Verdict{
		Question:  "what did I say about the roof?",
		Type:      TypeSingleSessionUser,
		Gold:      "it leaks",
		Candidate: "the roof leaks",
	})
	for _, p := range WritePolicies() {
		if strings.Contains(prompt, p.Name) {
			t.Errorf("the judge prompt names write policy %q — the judge must not be able to "+
				"tell which cell produced the answer it is scoring", p.Name)
		}
	}
	for _, p := range QueryPolicies() {
		if strings.Contains(prompt, p.Name) {
			t.Errorf("the judge prompt names query policy %q", p.Name)
		}
	}
}

// TestJudgePromptBranchesOnQuestionType pins the benchmark's own semantics.
//
// Verified against upstream evaluate_qa.py on 2026-09-01: temporal-reasoning
// tolerates an off-by-one in a day count, knowledge-update accepts the old value
// when the update is present too, and single-session-preference is scored
// against a rubric rather than for equality. A single generic consistency prompt
// reproduces none of that, so a score taken from one is not LongMemEval answer
// accuracy however fixed the model is held.
func TestJudgePromptBranchesOnQuestionType(t *testing.T) {
	base := Verdict{Question: "q", Gold: "g", Candidate: "c"}
	seen := map[string]string{}
	for _, typ := range []string{
		TypeSingleSessionUser,
		TypeSingleSessionAssistant,
		TypeSingleSessionPreference,
		TypeTemporalReasoning,
		TypeKnowledgeUpdate,
		TypeMultiSession,
	} {
		v := base
		v.Type = typ
		seen[typ] = JudgePrompt(v)
	}

	if !strings.Contains(strings.ToLower(seen[TypeTemporalReasoning]), "off-by-one") {
		t.Errorf("temporal-reasoning prompt does not carry the off-by-one tolerance upstream "+
			"applies, so this metric is stricter than the benchmark it is named after:\n%s",
			seen[TypeTemporalReasoning])
	}
	if !strings.Contains(strings.ToLower(seen[TypeKnowledgeUpdate]), "previous") {
		t.Errorf("knowledge-update prompt does not allow the superseded value alongside the "+
			"update, which upstream accepts:\n%s", seen[TypeKnowledgeUpdate])
	}
	if !strings.Contains(strings.ToLower(seen[TypeSingleSessionPreference]), "rubric") {
		t.Errorf("single-session-preference prompt is not rubric-scored:\n%s",
			seen[TypeSingleSessionPreference])
	}
	// The three specialised prompts must actually differ from the plain one, or
	// the branch is decoration and every assertion above could be satisfied by one
	// prompt carrying every clause at once.
	plain := seen[TypeSingleSessionUser]
	for _, typ := range []string{TypeTemporalReasoning, TypeKnowledgeUpdate, TypeSingleSessionPreference} {
		if seen[typ] == plain {
			t.Errorf("prompt for %q is identical to the plain one — the branch does nothing", typ)
		}
	}
}

// TestJudgeScoresAnAbstentionQuestionForUnanswerability covers the _abs case,
// which upstream drives with an entirely different template: the question is
// whether the model correctly identified the question as unanswerable, not
// whether it matched a gold string it has none of.
func TestJudgeScoresAnAbstentionQuestionForUnanswerability(t *testing.T) {
	v := Verdict{
		Question:  "when did I mention the boat?",
		Type:      TypeSingleSessionUser,
		ID:        "q_something_abs",
		Gold:      "",
		Candidate: "I don't have that information.",
	}
	if !v.IsAbstention() {
		t.Fatal("a question id ending in _abs is an abstention item — that suffix is the only " +
			"marker the corpus carries")
	}
	prompt := JudgePrompt(v)
	if !strings.Contains(strings.ToLower(prompt), "unanswerable") {
		t.Errorf("the abstention prompt does not ask about unanswerability:\n%s", prompt)
	}
	plain := v
	plain.ID = "q_something"
	if JudgePrompt(plain) == prompt {
		t.Error("the abstention prompt is identical to the ordinary one, so the _abs case is " +
			"being scored for content it has no gold answer for")
	}
}

func TestJudgeIsBinary(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"yes", true},
		{"YES\n", true},
		{"  yes, the response contains it", true},
		{"no", false},
		{"No.", false},
	} {
		got, err := ParseVerdict(tc.raw)
		if err != nil {
			t.Errorf("ParseVerdict(%q): %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseVerdict(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestJudgeRefusesAnUnparseableVerdict keeps a model outage from being recorded
// as a policy losing a point.
func TestJudgeRefusesAnUnparseableVerdict(t *testing.T) {
	for _, raw := range []string{"", "   ", "maybe", "I cannot comply"} {
		if _, err := ParseVerdict(raw); err == nil {
			t.Errorf("ParseVerdict(%q) returned no error — an unreadable verdict must abort the "+
				"cell, never score as incorrect, or an endpoint failure reads as a finding", raw)
		}
	}
}
