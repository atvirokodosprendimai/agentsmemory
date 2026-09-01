package longmemeval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// The six question types LongMemEval-S labels its questions with.
//
// They are constants rather than free strings because the subset selector
// stratifies by them: a typo would produce a seventh stratum holding one
// question, and the run would report a clean spread it does not have.
const (
	TypeSingleSessionUser       = "single-session-user"
	TypeSingleSessionAssistant  = "single-session-assistant"
	TypeSingleSessionPreference = "single-session-preference"
	TypeTemporalReasoning       = "temporal-reasoning"
	TypeKnowledgeUpdate         = "knowledge-update"
	TypeMultiSession            = "multi-session"
)

// Turn is one message in a session.
//
// HasAnswer is LongMemEval's own turn-level evidence label, and it is the whole
// reason the retrieval-only column ADR-047 reports beside the judged score costs
// nothing extra: it says which turns contain what the question needs, so a
// policy's records can be checked for the evidence without asking a model
// anything.
type Turn struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	HasAnswer bool   `json:"has_answer"`
}

// Session is one dated user/assistant conversation in a question's haystack.
//
// The id and the date do not live in the session object in the published format
// — they arrive in two sibling arrays — so this type is the place they are
// joined, once, under Load's alignment check rather than at every use site.
type Session struct {
	ID    string
	Date  string
	Turns []Turn
}

// Question is one LongMemEval-S item: what to ask, what the right answer is, the
// haystack to ask it against, and which sessions actually carry the evidence.
type Question struct {
	ID             string
	Type           string
	Question       string
	Answer         string
	Date           string
	Haystack       []Session
	GoldSessionIDs []string
}

// Dataset is a loaded LongMemEval-S file together with its identity.
//
// SHA256 is not decoration. Every number this package produces is valid FOR a
// corpus, and a results file naming a path names something that can be edited
// under it; the digest is what lets a later run say whether it is comparable.
// Same rule ADR-007 states for populations, applied to the corpus itself.
type Dataset struct {
	Questions []Question
	Path      string
	SHA256    string
}

// wireQuestion is the published on-disk shape. It is separate from Question
// because the file stores one session across three parallel arrays and this
// package refuses to carry that representation any further than Load.
type wireQuestion struct {
	ID               string   `json:"question_id"`
	Type             string   `json:"question_type"`
	Question         string   `json:"question"`
	Answer           string   `json:"answer"`
	Date             string   `json:"question_date"`
	HaystackIDs      []string `json:"haystack_session_ids"`
	HaystackDates    []string `json:"haystack_dates"`
	HaystackSessions [][]Turn `json:"haystack_sessions"`
	AnswerSessionIDs []string `json:"answer_session_ids"`
}

// Load reads a LongMemEval-S file, joins each question's three parallel haystack
// arrays into dated sessions, and refuses anything it cannot join honestly.
//
// It fails rather than repairs, for two failures that are invisible downstream.
// A question whose ids, dates and sessions disagree in length would zip short
// and hand a session its neighbour's date — every temporal-reasoning question
// then has a wrong premise and no test anywhere would show it. And a gold
// session that is not in the haystack cannot be retrieved by any policy, so it
// scores zero across the whole grid: a broken row that reads as the honest
// finding "no policy helps here". Both errors name the question so the first
// real file says exactly what disagreed.
func Load(path string) (Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, fmt.Errorf("read %s: %w", path, err)
	}
	var wire []wireQuestion
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Dataset{}, fmt.Errorf("parse %s: %w", path, err)
	}

	sum := sha256.Sum256(raw)
	ds := Dataset{
		Path:      path,
		SHA256:    hex.EncodeToString(sum[:]),
		Questions: make([]Question, 0, len(wire)),
	}
	for _, w := range wire {
		q, err := w.question()
		if err != nil {
			return Dataset{}, fmt.Errorf("%s: %w", path, err)
		}
		ds.Questions = append(ds.Questions, q)
	}
	return ds, nil
}

// question joins the wire form's parallel arrays and validates the two
// invariants Load's comment records.
func (w wireQuestion) question() (Question, error) {
	if len(w.HaystackIDs) != len(w.HaystackSessions) || len(w.HaystackDates) != len(w.HaystackSessions) {
		return Question{}, fmt.Errorf(
			"question %q: haystack arrays disagree — %d session ids, %d dates, %d sessions; "+
				"zipping them short would date a session from its neighbour",
			w.ID, len(w.HaystackIDs), len(w.HaystackDates), len(w.HaystackSessions))
	}

	q := Question{
		ID:             w.ID,
		Type:           w.Type,
		Question:       w.Question,
		Answer:         w.Answer,
		Date:           w.Date,
		GoldSessionIDs: w.AnswerSessionIDs,
		Haystack:       make([]Session, 0, len(w.HaystackSessions)),
	}
	present := make(map[string]bool, len(w.HaystackIDs))
	for i, turns := range w.HaystackSessions {
		q.Haystack = append(q.Haystack, Session{
			ID:    w.HaystackIDs[i],
			Date:  w.HaystackDates[i],
			Turns: turns,
		})
		present[w.HaystackIDs[i]] = true
	}
	for _, gold := range w.AnswerSessionIDs {
		if !present[gold] {
			return Question{}, fmt.Errorf(
				"question %q: gold session %q is not in its own haystack, so no policy can retrieve it "+
					"and the row would score zero for all of them alike",
				w.ID, gold)
		}
	}
	return q, nil
}
