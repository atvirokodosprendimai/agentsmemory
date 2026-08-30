package store

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// qualifiers are the words that turn a serving claim into a conditional one.
//
// The gate does not try to judge whether a sentence is TRUE — it cannot. It
// checks that a sentence claiming the index serves searches admits that
// something else sometimes does, which is the property that went false: the old
// package comment was unconditional, and an unconditional sentence cannot
// survive a fallback being added anywhere in the package.
var qualifiers = []string{
	"if", "while", "when", "unless", "until", "behind", "agree", "stale",
	"source of truth", "sot", "fall", "fell", "degraded",
}

// TestNoCommentClaimsSearchIsServedOnlyByTheIndex fails when a comment in this
// package says the index serves searches without admitting the fallback.
//
// It exists because two package comments — store.go's and hybrid.go's — asserted
// exactly that, and both went false when ADR-033 R2 added the source-of-truth
// fallback. hybrid.go's sat about thirty lines above the very code that
// falsified it, survived the review that landed R2, and was still there months
// later (issue #59). Per AGENTS.md, documentation here is load-bearing: an agent
// on a harness with no palace access, and a contributor reading the file on
// GitHub, have only the comment.
//
// A word-count or "is this comment accurate" rule is not available — accuracy is
// not decidable — so the gate checks the one mechanical property that failed:
// UNCONDITIONALITY. Same shape as TestDiscoveredPairsAdmitTheirCondition, which
// requires --help text to name the knob that gates it.
func TestNoCommentClaimsSearchIsServedOnlyByTheIndex(t *testing.T) {
	for _, file := range packageSources(t) {
		for _, sentence := range servingSentences(t, file) {
			if !admitsACondition(sentence) {
				t.Errorf("%s: this comment says the index serves searches and names no condition:\n\n  %s\n\n"+
					"Since ADR-033 R2 the source of truth serves whenever the index has fallen behind "+
					"(see Hybrid.Search). An unconditional sentence here is the exact drift issue #59 "+
					"found: it was true when written and nothing noticed when it stopped being true.",
					file, sentence)
			}
		}
	}
}

// TestTheServingClaimGateCatchesAnUnconditionalSentence is the falsifiability
// half, and it is a subtest driving the SAME predicate rather than a copy.
//
// A corpus with zero offenders cannot exercise the branch that reports one, so
// without this the gate could be severed and stay green while announcing that
// every comment is fine — which is how a disabled gate shipped in this tree
// before (see citation_test.go's own record of it).
func TestTheServingClaimGateCatchesAnUnconditionalSentence(t *testing.T) {
	// These are the CLAUSES the gate charges. The comment above this list used
	// to call them "verbatim" from hybrid.go and store.go, and for store.go that
	// was false — its shipped sentence is one clause of a longer one, and the
	// trimming is exactly what hid the miss below. The verbatim text is driven
	// through the real extractor in the subtest at the end instead.
	offenders := []string{
		"Searches are served entirely by the index.",
		"searches are served by the index",
		"A search is served from the index, always.",
		// Unconditional, and it contains the letters of a qualifier inside a
		// word that names no condition: "different" holds "if", "specific"
		// holds "if" again. A substring test excuses both, which is why
		// admitsACondition matches on word boundaries.
		"Searches are served by the index, in a different way for each specific store.",
	}
	for _, s := range offenders {
		if admitsACondition(s) {
			t.Errorf("the gate accepts an unconditional claim, so it would not have caught the "+
				"sentence issue #59 was filed about: %q", s)
		}
	}

	// And the shapes that must NOT fire, because a gate that cries wolf gets
	// disabled. Both are live sentences from this package after the fix, not
	// invented ones: the first is Hybrid.Search's doc comment, the second is the
	// corrected package comment in store.go.
	fine := []string{
		"Search is served by the index while the halves agree on population, and by the source of truth when the index has fallen behind.",
		"searches are served by the index while the two halves agree on population, and by the SoT when the index has fallen behind",
		// The plainest way to write the same true condition. It was rejected
		// before "if" joined the list, which is a false alarm on correct prose
		// — the failure mode the paragraph above says turns a gate off.
		"Search is served by the index if it is current.",
	}
	for _, s := range fine {
		if !admitsACondition(s) {
			t.Errorf("the gate rejects a correctly-qualified sentence, which is how a gate gets "+
				"turned off: %q", s)
		}
	}
}

// TestTheShippedStoreCommentIsCaughtVerbatim drives the historical comment
// through the real extractor, unedited, because the hand-trimmed fixture above
// is what let this gate ship green over its own motivating example.
//
// The clause carrying the claim names no condition; the word SoT that used to
// excuse it belongs to the clause about write ordering, one semicolon earlier.
// Charging a claim for a qualifier in a neighbouring clause is how a gate
// reports that every comment is fine over a corpus containing the offender it
// was built for.
func TestTheShippedStoreCommentIsCaughtVerbatim(t *testing.T) {
	// Verbatim from store.go before this change, line breaks and all.
	const shipped = `Hybrid wires the two together: writes land in the SoT first, then the index;
searches are served by the index. Swapping the search backend (Qdrant for
something else later) therefore means writing one driver and rebuilding the
index — the truth never moves.`

	claims := servingClaimsIn(shipped)
	if len(claims) == 0 {
		t.Fatal("the extractor found no serving claim in the sentence issue #59 was filed about, " +
			"so the gate cannot charge it however good admitsACondition gets")
	}
	var caught bool
	for _, c := range claims {
		if !admitsACondition(c) {
			caught = true
		}
	}
	if !caught {
		t.Errorf("every clause of the shipped store.go comment was excused, so the gate is green "+
			"over the exact comment it exists to catch: %q", claims)
	}
}

// admitsACondition reports whether a serving claim names something that limits
// it. Deliberately generous: the cost of missing a badly-worded true sentence is
// one uncaught comment, and the cost of a false alarm is the gate's credibility.
//
// Matching is on WORD BOUNDARIES, not substrings, and that is what makes the
// list safe to keep generous. A plain substring test cannot hold "if": the
// letters appear inside "different", "specific" and "verify", so every sentence
// using one of those would be excused without naming a condition at all. The
// same test is a prefix on each word, so "fall" still admits "fallen", "agree"
// still admits "agrees", and "when" still admits "whenever" — the inflections
// the list was written to cover — while no qualifier can hide mid-word.
func admitsACondition(sentence string) bool {
	words := strings.FieldsFunc(strings.ToLower(sentence), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	// A leading and trailing space so a phrase qualifier matches at either end
	// of the sentence exactly as it does in the middle.
	joined := " " + strings.Join(words, " ")
	for _, q := range qualifiers {
		if strings.Contains(q, " ") {
			if strings.Contains(joined, " "+q) {
				return true
			}
			continue
		}
		for _, w := range words {
			if strings.HasPrefix(w, q) {
				return true
			}
		}
	}
	return false
}

// servingSentences returns the comment sentences in one file that claim the
// index serves searches. A sentence qualifies only when it names searching, the
// index, and serving — all three, so a sentence about WRITING to the index or
// about rebuilding it is never charged.
func servingSentences(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var out []string
	for _, group := range file.Comments {
		// Sentence-level, not comment-level: a long comment usually qualifies the
		// claim in a LATER sentence, and charging the whole group would let that
		// distant qualifier excuse an unconditional sentence — the precise error
		// the old hybrid.go comment made, since Search's correct wording sat in
		// the same file.
		out = append(out, servingClaimsIn(group.Text())...)
	}
	return out
}

// servingClaimsIn returns the clauses of one comment that claim the index serves
// searches. Exported to the falsifiability half so that half drives the REAL
// extraction rather than a copy of it.
//
// Splitting is on CLAUSE boundaries — "." and also ";" and ":" — not on the full
// stop alone. That is not tidiness: the sentence issue #59 was filed about is
//
//	Hybrid wires the two together: writes land in the SoT first, then the index;
//	searches are served by the index.
//
// One full stop, so a "."-only split hands the whole thing to admitsACondition as
// a single unit, where the word SoT — belonging to the clause about WRITE
// ORDERING — prefix-matches the qualifier "sot" and excuses a serving claim that
// names no condition at all. The gate was green over its own motivating example.
// A qualifier earns its excuse only inside the clause making the claim.
func servingClaimsIn(comment string) []string {
	var out []string
	for _, clause := range strings.FieldsFunc(flattenComment(comment), func(r rune) bool {
		return r == '.' || r == ';' || r == ':'
	}) {
		lower := strings.ToLower(clause)
		// "serv" and nothing looser. An earlier draft also matched "answered
		// by", and it fired on Filter's doc comment — "scoping is answered BY
		// the index rather than after it" — which is about WHERE filtering
		// happens, not about which half serves. That sentence is correct, and a
		// gate that reports it is a gate someone turns off.
		if strings.Contains(lower, "search") &&
			strings.Contains(lower, "index") &&
			strings.Contains(lower, "serv") {
			out = append(out, strings.TrimSpace(clause))
		}
	}
	return out
}

// flattenComment collapses a comment's line breaks so a sentence split across
// lines is still one sentence. Without this a claim wrapped mid-phrase would be
// split into fragments, and a fragment holding the qualifier would pass while
// the fragment holding the claim failed — noise in both directions.
func flattenComment(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// packageSources lists this package's non-test Go files.
func packageSources(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		t.Fatal("no package sources found — this check has stopped checking anything")
	}
	return out
}
