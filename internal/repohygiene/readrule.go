// Package repohygiene holds gates over the repository's own documents — the
// checks that fail when a pointer, a citation or a recorded number stops being
// true. Most of it is tests; this file is the one piece of non-test code, because
// the counting rule it reads has to be resolvable by something other than the
// gate that checks it.
package repohygiene

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RulePath and BaselinesDir are where the measurement artifacts live, relative to
// the repository root. They are constants rather than arguments because the whole
// point of ADR-044 F-5 is that the rule is ONE committed artifact with one
// identity — a gate that took the path as a parameter could be pointed at a
// convenient copy.
const (
	RulePath     = "docs/measurement/read-counting-rule.md"
	BaselinesDir = "docs/measurement/baselines"
)

// citationRE matches the line by which a baseline names the rule it was measured
// under. The digest is anchored to its own line so that a baseline DISCUSSING a
// digest in prose cannot be mistaken for one citing it — the same distinction
// between a setting and the discussion of a setting that a structural assertion
// over a config file has to make.
var citationRE = regexp.MustCompile(`(?m)^rule-sha256:\s*([0-9a-f]{64})\s*$`)

// Baseline is one recorded measurement and the rule identity it claims.
type Baseline struct {
	// Path is the baseline file, relative to the repository root.
	Path string
	// CitedDigest is the digest the file names, or "" when it names none.
	CitedDigest string
}

// CitationState is what resolving a baseline's citation against the current rule
// produced.
type CitationState int

const (
	// CitationResolves means the baseline names the rule as it stands now.
	CitationResolves CitationState = iota
	// CitationMissing means the baseline names no rule at all. It is a different
	// failure from CitationStale and is reported differently: one is a baseline
	// nobody bound, the other is a baseline the rule moved out from under.
	CitationMissing
	// CitationStale means the baseline names a rule that is no longer current.
	// ADR-044 T2 is what gives this state its consequence; T1 only distinguishes
	// it so the report cannot claim an observation it did not make.
	CitationStale
)

// String names the state in the vocabulary a report uses.
func (c CitationState) String() string {
	switch c {
	case CitationResolves:
		return "resolves"
	case CitationMissing:
		return "no citation"
	case CitationStale:
		return "cites a rule that is no longer current"
	}
	return "unknown"
}

// NormalizeRule renders a rule's bytes into the form its digest is taken over:
// CRLF collapsed to LF, trailing whitespace stripped from every line, and exactly
// one trailing newline.
//
// The normalization exists so that a reformat which changes no words does not
// invalidate every baseline in the tree. Without it the gate fires on an editor
// setting, and a gate that fires constantly is one people learn to skip.
func NormalizeRule(b []byte) []byte {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n")
}

// RuleDigest returns the current identity of the counting rule under root.
func RuleDigest(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, RulePath))
	if err != nil {
		return "", fmt.Errorf("read counting rule: %w", err)
	}
	sum := sha256.Sum256(NormalizeRule(b))
	return hex.EncodeToString(sum[:]), nil
}

// Baselines returns every recorded baseline under root, derived from the
// directory rather than from a maintained list — so a baseline added tomorrow
// joins the check without anyone remembering to register it. A list kept beside
// the truth is a thing somebody has to remember.
func Baselines(root string) ([]Baseline, error) {
	paths, err := filepath.Glob(filepath.Join(root, BaselinesDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("glob baselines: %w", err)
	}
	out := make([]Baseline, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read baseline %s: %w", p, err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		bl := Baseline{Path: filepath.ToSlash(rel)}
		if m := citationRE.FindSubmatch(b); m != nil {
			bl.CitedDigest = string(m[1])
		}
		out = append(out, bl)
	}
	return out, nil
}

// QuoteRate reports whether a rate may be quoted from this baseline, and refuses
// with the reason when it may not.
//
// ⚠ IT NAMES THE RULE CHANGE RATHER THAN REPORTING A COMPARISON, which is the
// whole of ADR-044 F-6. A baseline taken under one counting rule and a figure
// taken under another are two different quantities, and comparing them produces a
// number that looks exactly like a real one. The failure has to be loud at the
// point of quoting, because there is nothing about the resulting percentage that
// says it is meaningless.
//
// The two refusals are deliberately different sentences. "No citation" is a
// baseline nobody bound; "cites a rule that is no longer current" is a baseline
// the rule moved out from under. Collapsing them would report an observation the
// gate did not make.
func (b Baseline) QuoteRate(currentDigest string) error {
	switch b.Resolve(currentDigest) {
	case CitationResolves:
		return nil
	case CitationMissing:
		return fmt.Errorf("%s names no counting rule, so there is no quantity to quote: "+
			"add a rule-sha256 line naming %s", b.Path, RulePath)
	default:
		return fmt.Errorf("%s was measured under a counting rule that has since changed "+
			"(baseline cites %s, %s is now %s) — the rule changed, so this baseline is invalid and "+
			"no rate may be quoted from it; re-collect rather than re-cite",
			b.Path, short(b.CitedDigest), RulePath, short(currentDigest))
	}
}

// short renders a digest for a human-readable message without losing the ability
// to tell two of them apart.
func short(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12] + "…"
}

// Resolve reports how a baseline's citation stands against the current rule.
func (b Baseline) Resolve(currentDigest string) CitationState {
	switch {
	case b.CitedDigest == "":
		return CitationMissing
	case b.CitedDigest == currentDigest:
		return CitationResolves
	default:
		return CitationStale
	}
}
