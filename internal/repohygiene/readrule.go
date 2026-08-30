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
	"io/fs"
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

// verdictRE matches the line by which a baseline says whether a rate may be read
// from it at all.
//
// It exists because ADR-044 T1 recorded a baseline that says IN PROSE "this must
// not be read as satisfying F-5", and review found the gate counting it as one
// anyway: `Baseline` carried only a path and a digest, so a file declaring itself
// unusable was indistinguishable from a usable one. A gate that reports a
// requirement satisfied because a file exists, without reading what the file
// says, is this repository's signature defect applied to its own evidence.
//
// Machine-readable for the same reason every other rule here is: the prose was
// already correct and no tool could act on it.
var verdictRE = regexp.MustCompile(`(?m)^baseline:\s*(usable|degenerate)\s*$`)

// Baseline is one recorded measurement and the rule identity it claims.
type Baseline struct {
	// Path is the baseline file, relative to the repository root.
	Path string
	// CitedDigest is the digest the file names, or "" when it names none.
	CitedDigest string
	// Verdict is what the file says about its own usability: "usable",
	// "degenerate", or "" when it says nothing. A baseline that declares nothing
	// is not assumed usable — see Usable.
	Verdict string
}

// Usable reports whether a rate may be read from this baseline at all, before any
// question of which rule it cites.
//
// A file that says NOTHING is not usable. That is the direction to be wrong in:
// an unmarked baseline is one nobody has judged, and treating "no verdict" as
// "fine" is exactly the assumption that let a file saying "this is not a
// baseline" satisfy the requirement it disclaims.
func (b Baseline) Usable() bool { return b.Verdict == "usable" }

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
	// WalkDir rather than Glob. A glob of "*.md" matches siblings only, so a
	// baseline filed at baselines/2026/x.md was invisible and the gate stayed
	// GREEN — silently, which is the class this package exists to catch, and
	// against this function's own promise that a baseline added tomorrow joins
	// the check. Found in review 2026-08-29.
	var paths []string
	dir := filepath.Join(root, BaselinesDir)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		// A directory like this acquires a README the way any directory does, and
		// reddening CI over one would teach people that the gate is noise. It is
		// skipped by name rather than by a convention nobody would discover.
		if strings.EqualFold(d.Name(), "README.md") {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk baselines: %w", err)
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
		if m := verdictRE.FindSubmatch(b); m != nil {
			bl.Verdict = string(m[1])
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

// shippedWithoutUsableBaseline records a decision to ship mechanism work while no
// USABLE baseline exists, keyed by the record that took the decision.
//
// It is a written exemption rather than a silent one, which is the only form this
// repository accepts: a knob it finds inert must be listed WITH A REASON, never
// just listed. An empty reason is refused by TestF5AnOverrideNamesItsReason, so
// the reason is the review.
//
// Removing an entry is how the constraint comes back. That is deliberate: the
// override is expected to be temporary, and a temporary thing with no expiry is
// a permanent thing nobody decided on.
var shippedWithoutUsableBaseline = map[string]string{
	"ADR-044": "Amendment 2026-08-29, owner Zy. The artifact half of F-5 is satisfied — the rule is " +
		"committed, digested and cited. What is overridden is the constraint ADR-044 inherits from " +
		"ADR-041, no mechanism before a baseline, because the only baseline is degenerate: 90 of 91 " +
		"recalls against ONE fetch on a drawer_fetches instrument that landed the same morning. The " +
		"mechanism tasks correct statements the server makes about its own responses, whose wrongness " +
		"is established by reading the arithmetic rather than by measuring how often anyone acts on " +
		"it. Re-taking the baseline is an open Follow-up on that record; when it lands, DELETE THIS " +
		"ENTRY rather than editing it.",
}
