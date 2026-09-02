package longmemeval

import (
	"fmt"
	"strings"
)

// Verdict is everything the judge is given about one answer.
//
// It carries no policy name, no cell label and no ordering hint, and
// TestJudgeNeverSeesThePolicyName asserts the rendered prompt contains none —
// ADR-047 property 4. The judge prompt is the one place a preferred outcome
// could be smuggled in for free, so blindness is a property to gate rather than
// a convention to keep.
type Verdict struct {
	ID        string
	Type      string
	Question  string
	Gold      string
	Candidate string
}

// IsAbstention reports whether this is one of LongMemEval's unanswerable items.
//
// The `_abs` id suffix is the only marker the corpus carries for it — there is
// no separate field — which is why this is a method rather than a caller's
// string test repeated at each use site.
func (v Verdict) IsAbstention() bool { return strings.HasSuffix(v.ID, "_abs") }

// Grading clauses, one per question type, taken from the benchmark's own
// evaluator (src/evaluation/evaluate_qa.py, read 2026-09-01).
//
// They are here because a single generic consistency prompt is NOT LongMemEval
// answer accuracy however fixed the model is held: upstream branches, and a
// score taken from an unbranched prompt would be a house metric reported under
// the benchmark's name. Raised in review of PR #148 and verified against the
// upstream file rather than taken on the reviewer's word.
const (
	clauseDefault = "Answer yes if the response contains the correct answer. " +
		"A response that contains only part of the required information is not correct."

	// Upstream tolerates an off-by-one in day counts: a model that says 19 days
	// where the answer is 18 is scored correct. Without this clause every temporal
	// row here would be stricter than the benchmark and the type would look like
	// the one our policies fail at.
	clauseTemporal = "Answer yes if the response contains the correct answer. " +
		"Do not penalize off-by-one errors in a number of days: if the correct answer is 18 days " +
		"and the response says 19 days, that is still correct."

	// Upstream accepts the superseded value when the update is present too, which
	// is the whole point of the knowledge-update type: the model is being asked
	// whether it knows the CURRENT value, not whether it has forgotten the old one.
	clauseKnowledgeUpdate = "Answer yes if the response contains the updated answer. " +
		"If the response contains some previous information along with the updated answer, " +
		"it is still correct, as long as the updated answer is the one required."

	// Preference questions have no single right string: upstream scores them
	// against a rubric and does not require every point of it.
	clausePreference = "The gold answer is a rubric rather than a single correct string. " +
		"The response does not need to reflect every point in the rubric. " +
		"Answer yes as long as it recalls and uses the user's personal information correctly."

	// The abstention template asks a different question entirely, so it is a
	// separate prompt rather than a clause appended to the others.
	clauseAbstention = "This question cannot be answered from the conversation history. " +
		"Answer yes if the response correctly identifies the question as unanswerable, " +
		"including by saying the information is incomplete or unavailable."
)

// clauseFor returns the grading instruction for one question type.
func clauseFor(typ string) string {
	switch typ {
	case TypeTemporalReasoning:
		return clauseTemporal
	case TypeKnowledgeUpdate:
		return clauseKnowledgeUpdate
	case TypeSingleSessionPreference:
		return clausePreference
	default:
		return clauseDefault
	}
}

// JudgePrompt renders the prompt scoring one candidate answer.
//
// The abstention branch omits the gold answer because there is not one: scoring
// an `_abs` item against an empty gold string would mark every response wrong
// and put a constant into every cell alike, damping every contrast the grid is
// there to measure.
func JudgePrompt(v Verdict) string {
	var b strings.Builder
	b.WriteString("You are grading one answer. Reply with exactly one word: yes or no.\n\n")
	if v.IsAbstention() {
		b.WriteString(clauseAbstention)
		fmt.Fprintf(&b, "\n\nQUESTION:\n%s\n\nRESPONSE:\n%s\n\nyes or no:", v.Question, v.Candidate)
		return b.String()
	}
	b.WriteString(clauseFor(v.Type))
	fmt.Fprintf(&b, "\n\nQUESTION:\n%s\n\nCORRECT ANSWER:\n%s\n\nRESPONSE:\n%s\n\nyes or no:",
		v.Question, v.Gold, v.Candidate)
	return b.String()
}

// ReaderPrompt renders the prompt the reader answers from.
//
// One string, held constant across the whole grid: it is not a variable under
// test, and a reader prompt that moved between cells would put a second
// difference into a contrast that is supposed to isolate one.
func ReaderPrompt(question string, memories []string) string {
	var b strings.Builder
	b.WriteString("Answer the question using only the memories below. " +
		"If they do not contain the answer, say you do not have that information.\n\nMEMORIES:\n")
	for _, m := range memories {
		fmt.Fprintf(&b, "---\n%s\n", m)
	}
	fmt.Fprintf(&b, "\nQUESTION:\n%s\n\nANSWER:", question)
	return b.String()
}

// ParseVerdict reads the judge's one-word answer.
//
// ⚠It REFUSES anything it cannot read rather than defaulting to incorrect. A
// judge failure scored as a wrong answer is a model outage recorded as a policy
// losing, and that error is invisible afterwards: the cell is simply lower. The
// caller aborts the cell instead.
func ParseVerdict(raw string) (bool, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimLeft(s, "*_ \t\n")
	switch {
	case strings.HasPrefix(s, "yes"):
		return true, nil
	case strings.HasPrefix(s, "no"):
		return false, nil
	default:
		return false, fmt.Errorf("judge returned no readable verdict: %q", gen1(raw))
	}
}

// gen1 keeps an unreadable verdict short enough to belong in an error.
func gen1(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 80 {
		return string(r[:80])
	}
	return s
}
