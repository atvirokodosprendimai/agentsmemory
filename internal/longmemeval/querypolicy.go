package longmemeval

import (
	"fmt"
	"sort"
	"strings"
)

// QueryPolicy is one named way of asking the palace for what a question needs.
//
// It is the grid's column axis, where WritePolicy is its row axis: the whole
// instrument exists to separate "the memory was written well" from "the question
// was asked well", and those two have never been measured apart here.
//
// Queries returns the searches to run. More than one is allowed so a multi-hop
// question can be decomposed, but a policy that returns many is spending
// retrieval the baseline does not get — see decomposedCap.
type QueryPolicy struct {
	Name     string
	Describe string
	Queries  func(Question) []string
}

// decomposedCap bounds how many searches a decomposing policy may run.
//
// Without it that policy could win by retrieving more rather than by asking
// better, and the table would report a prompting rule where the honest finding
// is "more retrieval helps" — which the fixed context budget already prevents on
// the write axis and nothing else would prevent on this one.
const decomposedCap = 3

// frameWords are the tokens this corpus's questions are FRAMED with rather than
// the ones they are about.
//
// Three groups, and each earned its place from the corpus rather than from a
// generic stopword list: first-person pronouns, which 480 of the 500 questions
// carry and which the haystack carries too, so they appear on both sides of
// every comparison and separate nothing; interrogative and auxiliary openers,
// which every question has by construction; and the conversational verbs the
// preference items are wrapped in, where the request IS the frame.
//
// Deliberately NOT a general stopword list. Words like "first", "before",
// "after", "last" and "most" stay, because temporal-reasoning questions are
// about exactly those relations — 133 of the 500 — and a list that stripped them
// would make this policy worse than verbatim on a quarter of the corpus while
// looking like a cleanup.
var frameWords = map[string]bool{
	"i": true, "my": true, "me": true, "mine": true, "myself": true,
	"what": true, "which": true, "who": true, "whom": true, "whose": true,
	"when": true, "where": true, "why": true, "how": true,
	// "how many" and "how much" open 164 of the 500 questions and carry no
	// retrievable signal; "more" is deliberately NOT here, because a comparative
	// question is about the comparison.
	"many": true, "much": true,
	"did": true, "do": true, "does": true, "is": true, "are": true, "was": true,
	"were": true, "have": true, "has": true, "had": true, "the": true, "a": true,
	"an": true, "of": true, "to": true, "at": true, "on": true, "in": true,
	"for": true, "and": true, "or": true, "that": true, "this": true,
	"thinking": true, "trying": true, "recommendations": true, "recommend": true,
	"suggestions": true, "ideas": true, "tips": true, "any": true, "some": true,
	"please": true, "could": true, "would": true, "should": true, "can": true,
}

// stripFrame keeps a question's content words, in order, dropping the frame.
//
// Order is preserved and nothing is stemmed: the goal is to name the thing, not
// to build a bag of words. Duplicates are kept for the same reason — a question
// that says "plants" twice is about plants twice.
func stripFrame(question string) string {
	fields := strings.FieldsFunc(strings.ToLower(question), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '\'')
	})
	var kept []string
	for _, w := range fields {
		if !frameWords[w] {
			kept = append(kept, w)
		}
	}
	return strings.Join(kept, " ")
}

var queryPolicies = map[string]QueryPolicy{}

func registerQuery(p QueryPolicy) {
	if _, dup := queryPolicies[p.Name]; dup {
		panic(fmt.Sprintf("longmemeval: query policy %q registered twice", p.Name))
	}
	queryPolicies[p.Name] = p
}

// QueryPolicies returns every registered query policy, ordered by name, so a
// results table and a --help listing iterate the same way on every run.
func QueryPolicies() []QueryPolicy {
	names := make([]string, 0, len(queryPolicies))
	for name := range queryPolicies {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]QueryPolicy, 0, len(names))
	for _, name := range names {
		out = append(out, queryPolicies[name])
	}
	return out
}

// QueryPolicyByName resolves a policy the way a --query flag value does. The
// bool is the point: an unknown value must be an operator-visible error, never a
// silent fall back to the baseline column.
func QueryPolicyByName(name string) (QueryPolicy, bool) {
	p, ok := queryPolicies[name]
	return p, ok
}

// QueryPolicyUsage renders the allowed --query values, for the same reason
// WritePolicyUsage does: T4's flag must derive its help text from the registry
// rather than typing a list that goes stale on the next registration.
func QueryPolicyUsage() string {
	var b strings.Builder
	b.WriteString("query policy, one of:")
	for _, p := range QueryPolicies() {
		fmt.Fprintf(&b, "\n  %-15s %s", p.Name, p.Describe)
	}
	return b.String()
}

func init() {
	// verbatim is the baseline column: the question exactly as the corpus states
	// it. It adds nothing on purpose — a baseline that pre-processed the question
	// would give every other column a head start to be measured against.
	registerQuery(QueryPolicy{
		Name:     "verbatim",
		Describe: "the question as typed — the baseline",
		Queries:  func(q Question) []string { return []string{q.Question} },
	})

	// named-thing is start-here's rule that an unfamiliar wing must be asked with
	// the entity NAMED, measured here rather than assumed. The skill states it
	// with a 583x spread behind it, but that spread was measured on RECALL RANK,
	// which is precisely the metric this ADR exists to stop treating as the
	// answer.
	//
	// It strips the conversational frame and keeps the content words, because
	// that is what "name the thing" means for this corpus. Measured over all 500
	// questions on 2026-09-01: 480 are written in the FIRST PERSON ("What degree
	// did I graduate with?"), and the haystack is first-person too, so the
	// pronouns appear on both sides and discriminate nothing. 17 of the 30
	// preference items are not questions at all but requests — "I was thinking of
	// trying a new coffee creamer recipe. Any recommendations?" — where asking
	// verbatim searches on "thinking", "trying" and "recommendations" while the
	// retrievable content is "coffee creamer".
	//
	// ⚠This REPLACED a version that prefixed the raw question_date. That was
	// noise by construction: the dates are formatted "2023/05/30 (Tue) 23:40" and
	// appear nowhere in the session text, so it added tokens that could match
	// nothing. No run had used it for a decision.
	registerQuery(QueryPolicy{
		Name:     "named-thing",
		Describe: "the question's content words, conversational frame stripped (start-here's rule)",
		Queries: func(q Question) []string {
			if stripped := stripFrame(q.Question); stripped != "" {
				return []string{stripped}
			}
			// A question that is ALL frame leaves nothing to search for. Falling
			// back to the verbatim question is the honest move: this policy's job is
			// to ask better, never to ask for nothing.
			return []string{q.Question}
		},
	})

	// decomposed asks a multi-hop question as several searches whose results the
	// runner merges. Capped at decomposedCap so it cannot buy its win with extra
	// retrieval; the split is on sentence boundaries because the corpus's
	// multi-session questions are written as several clauses.
	registerQuery(QueryPolicy{
		Name:     "decomposed",
		Describe: fmt.Sprintf("multi-clause questions asked as up to %d searches, merged", decomposedCap),
		Queries: func(q Question) []string {
			parts := strings.FieldsFunc(q.Question, func(r rune) bool {
				return r == '?' || r == ';'
			})
			var out []string
			for _, p := range parts {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
				if len(out) == decomposedCap {
					break
				}
			}
			if len(out) == 0 {
				return []string{q.Question}
			}
			return out
		},
	})
}
