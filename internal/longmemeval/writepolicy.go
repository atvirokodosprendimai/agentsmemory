package longmemeval

import (
	"fmt"
	"sort"
	"strings"
)

// BoundedRunes is the record size the `bounded` policy splits at.
//
// It is the threshold the centralised skills teach — "one drawer is ONE vector",
// so a memory averaging several topics matches none of them sharply — and this
// package exists to find out whether following it makes an agent answer better.
// The number is therefore a claim under test, not a tuning constant: it is here
// so the size rule is separable from the titling rule, and so a run that finds
// it neutral has measured the rule as stated rather than an approximation of it.
//
// Counted in runes rather than bytes because that is how the rule is written. A
// byte bound would split a multi-byte corpus somewhere else and measure a
// different rule than the one anybody was told to follow.
const BoundedRunes = 1600

// Record is one memory a write policy produces, before anything reaches a store.
//
// Room and Content are what gets written. SessionID and AnsweringTurns are
// PROVENANCE and are deliberately never written into the drawer and never shown
// to the reader: a policy cannot spend context budget on them, and they cannot
// leak the answer's location into the prompt.
//
// The provenance exists because T4 scores a retrieval-only column against
// LongMemEval's answer_session_ids, and once a transformed record is in the
// store nothing recovers which session produced it. Ordinal position cannot,
// because one-fact and bounded change the record count and duplicate content is
// legal. Found in review of PR #148, where this type carried Room and Content
// alone and the column that consumes it was specified two task files away.
type Record struct {
	Room           string
	Content        string
	SessionID      string
	AnsweringTurns []int
}

// WritePolicy is one named way of turning a question's haystack into memories.
//
// It is a pure function by contract: no I/O, no model call, no access to the
// palace. A policy that called a model would make its row partly a measurement
// of that model rather than of the writing rule, which is the one comparison
// this whole instrument exists to make.
type WritePolicy struct {
	Name     string
	Describe string
	Write    func(Question) []Record
}

// writePolicies is the registry, and it is the selection mechanism rather than a
// catalogue kept beside one.
//
// Keeping it as the single source means the flag's allowed values, its --help
// text and the reachability gate all read the same map: deleting a registration
// removes the row from every one of them at once, which is what makes
// TestEveryDeclaredPolicyIsSelectable a gate rather than a checklist.
var writePolicies = map[string]WritePolicy{}

func register(p WritePolicy) {
	if _, dup := writePolicies[p.Name]; dup {
		panic(fmt.Sprintf("longmemeval: write policy %q registered twice", p.Name))
	}
	writePolicies[p.Name] = p
}

// WritePolicies returns every registered policy, ordered by name.
//
// Ordered so that a results table, a --help listing and a test iterate the same
// way on every run; Go's map order would otherwise make a grid's row order move
// between runs of the same command, and a table nobody can diff is a table
// nobody trusts.
func WritePolicies() []WritePolicy {
	names := make([]string, 0, len(writePolicies))
	for name := range writePolicies {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]WritePolicy, 0, len(names))
	for _, name := range names {
		out = append(out, writePolicies[name])
	}
	return out
}

// WritePolicyByName resolves a policy the way a flag value does.
//
// The bool is the whole point: an unknown --write value has to be an error the
// operator sees, never a silent fallback to the baseline. A run that quietly
// measured verbatim under another policy's name would put a wrong row in a table
// that no later reader could detect.
func WritePolicyByName(name string) (WritePolicy, bool) {
	p, ok := writePolicies[name]
	return p, ok
}

// WritePolicyUsage renders the allowed --write values and their descriptions.
//
// It lives here rather than in the command because the command is built by T4
// and this task has to gate rung 3 — that a caller can DISCOVER a policy, not
// merely select it once told the name. T4's flag is required to call this rather
// than format its own list, and its own test fails if it does otherwise; a list
// typed beside the registry is the shape that goes stale the first time somebody
// adds a policy.
func WritePolicyUsage() string {
	var b strings.Builder
	b.WriteString("write policy, one of:")
	for _, p := range WritePolicies() {
		fmt.Fprintf(&b, "\n  %-15s %s", p.Name, p.Describe)
	}
	return b.String()
}

// answeringTurns returns the indices of a session's turns carrying evidence.
//
// Every policy reports these for the records it emits, which is what lets
// TestEveryPolicyPreservesSessionProvenance check that no policy silently drops
// the evidence — the content whose loss a judged metric would otherwise credit
// to the policy being admirably concise.
func answeringTurns(s Session) []int {
	var out []int
	for i, turn := range s.Turns {
		if turn.HasAnswer {
			out = append(out, i)
		}
	}
	return out
}

// sessionText joins a session's turns into the plain transcript form every
// policy starts from.
func sessionText(s Session) string {
	var b strings.Builder
	for i, turn := range s.Turns {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s: %s", turn.Role, turn.Content)
	}
	return b.String()
}

func init() {
	// verbatim is registered first because it is the baseline every delta is
	// measured against, and a reader of this file should meet it before any rule
	// under test. It must never be removed: without it there is no contrast.
	register(WritePolicy{
		Name:     "verbatim",
		Describe: "one record per session, turns joined unedited — the baseline",
		Write: func(q Question) []Record {
			out := make([]Record, 0, len(q.Haystack))
			for _, s := range q.Haystack {
				out = append(out, Record{
					Room:           "sessions",
					Content:        fmt.Sprintf("%s\n%s", s.Date, sessionText(s)),
					SessionID:      s.ID,
					AnsweringTurns: answeringTurns(s),
				})
			}
			return out
		},
	})

	// question-first is start-here's titling rule, implemented as stated rather
	// than approximated: the record OPENS with the question's own words, which is
	// the property the skill's measured recall numbers were taken on.
	register(WritePolicy{
		Name:     "question-first",
		Describe: "each session opened with the question it answers (start-here's titling rule)",
		Write: func(q Question) []Record {
			out := make([]Record, 0, len(q.Haystack))
			for _, s := range q.Haystack {
				out = append(out, Record{
					Room:           "sessions",
					Content:        fmt.Sprintf("%s\n%s\n%s", q.Question, s.Date, sessionText(s)),
					SessionID:      s.ID,
					AnsweringTurns: answeringTurns(s),
				})
			}
			return out
		},
	})

	// one-fact is "give experience its own record" taken to its limit: each
	// answering turn becomes its own memory, with its neighbour for the context
	// that makes it readable alone.
	//
	// A session with no answering turn still emits one record. Dropping it would
	// hand this policy a smaller, cleaner haystack than every other policy gets,
	// so its score would partly measure a corpus nobody else was given.
	register(WritePolicy{
		Name:     "one-fact",
		Describe: "each answering turn and its neighbour as their own record",
		Write: func(q Question) []Record {
			var out []Record
			for _, s := range q.Haystack {
				idx := answeringTurns(s)
				if len(idx) == 0 {
					out = append(out, Record{
						Room:      "sessions",
						Content:   fmt.Sprintf("%s\n%s", s.Date, sessionText(s)),
						SessionID: s.ID,
					})
					continue
				}
				for _, i := range idx {
					turns := []Turn{s.Turns[i]}
					if i+1 < len(s.Turns) {
						turns = append(turns, s.Turns[i+1])
					} else if i > 0 {
						turns = append([]Turn{s.Turns[i-1]}, turns...)
					}
					out = append(out, Record{
						Room:           "sessions",
						Content:        fmt.Sprintf("%s\n%s", s.Date, sessionText(Session{Turns: turns})),
						SessionID:      s.ID,
						AnsweringTurns: []int{i},
					})
				}
			}
			return out
		},
	})

	// bounded is verbatim split at BoundedRunes, so the size rule can be measured
	// apart from the titling rule. Every chunk of a split session keeps that
	// session's id and its answering turns, because provenance is a property of
	// where the text came from, not of how it was cut.
	register(WritePolicy{
		Name:     "bounded",
		Describe: fmt.Sprintf("verbatim, split at the %d-rune threshold the skills teach", BoundedRunes),
		Write: func(q Question) []Record {
			var out []Record
			for _, s := range q.Haystack {
				body := fmt.Sprintf("%s\n%s", s.Date, sessionText(s))
				for _, chunk := range splitRunes(body, BoundedRunes) {
					out = append(out, Record{
						Room:           "sessions",
						Content:        chunk,
						SessionID:      s.ID,
						AnsweringTurns: answeringTurns(s),
					})
				}
			}
			return out
		},
	})
}

// splitRunes cuts s into pieces of at most n runes, counting runes rather than
// bytes so a multi-byte transcript splits where the rule says it should.
func splitRunes(s string, n int) []string {
	r := []rune(s)
	if len(r) <= n {
		return []string{s}
	}
	var out []string
	for start := 0; start < len(r); start += n {
		end := min(start+n, len(r))
		out = append(out, string(r[start:end]))
	}
	return out
}
