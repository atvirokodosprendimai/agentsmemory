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
	// the entity named, measured here rather than assumed. The skill states it
	// with a 583x spread behind it, but that spread was measured on RECALL RANK,
	// which is precisely the metric this ADR exists to stop treating as the answer.
	registerQuery(QueryPolicy{
		Name:     "named-thing",
		Describe: "the question with its date and subject stated first (start-here's rule)",
		Queries: func(q Question) []string {
			if q.Date == "" {
				return []string{q.Question}
			}
			return []string{fmt.Sprintf("%s %s", q.Date, q.Question)}
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
