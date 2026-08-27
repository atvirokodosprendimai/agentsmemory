package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"
)

// ADR-041 T1. Count, from a session transcript, how many no-change assertions it
// held and how many were preceded by a recall.
//
// The unit is deliberately narrow. An agent that says "X still does Y" or "X does
// not do Z" is asserting that NOTHING CHANGED, and code cannot show that — a fix
// looks identical to code that was always right. That is the one class of claim
// this palace is uniquely able to answer, and the class that produced both
// published errors on 2026-08-27.

// classifierVersion is stamped on every observation.
//
// ⚠ BUMP IT WHENEVER assertionShape OR assertionSubject CHANGES. Rates taken under
// different versions are not comparable, and without this stamp tightening the
// regex reads as a behaviour change in the thing being measured (spec F-16).
const classifierVersion = "v2"

// assertionShape matches the sentence form: a claim that nothing changed.
var assertionShape = regexp.MustCompile(
	`\b(still (?:does|is|are|has|have|works?|returns?|fails?|lives?|reads?)|` +
		`does not|do not|is not|are not|has not|have not|` +
		`never (?:checks?|fires?|runs?|reaches?))\b`)

// assertionSubject requires the sentence to name something the palace could hold a
// decision about — a backticked token, an identifier, a path, or a record id.
//
// ⚠ SHAPE ALONE IS NOT ENOUGH, and this was measured rather than assumed. Over one
// real 54,565-line transcript on 2026-08-27, the shape rule alone matched 154
// sentences and most were not claims about system behaviour at all ("Absolute
// values are noise"). Requiring a subject leaves 57 — 37% — and those read as the
// intended class. A filter's shape rule cannot do a lexicon's job; the pairing is
// the classifier.
var assertionSubject = regexp.MustCompile(
	"`[^`]+`|\\b(?:am_[a-z_]+|Test[A-Z]\\w+|ADR-\\d{3}|[a-z_]+\\.(?:go|sh|md|sql)|[a-z]+[A-Z]\\w+)\\b")

// recallTools are the calls that count as having asked the palace.
var recallTools = map[string]bool{
	"mcp__agentsmemory__am_search":     true,
	"mcp__agentsmemory__am_get_drawer": true,
}

// Observation is one session's counts. Counts and identifiers only — no transcript
// text is carried, because the instrument runs on a developer's machine over their
// own working sessions and the measurement never needs the words (spec F-15).
type Observation struct {
	SessionID  string `json:"session_id"`
	Assertions int    `json:"assertions"`
	Preceded   int    `json:"preceded_by_recall"`
	Classifier string `json:"classifier_version"`
}

// sentenceSplit is deliberately crude: transcript prose is markdown, not paragraphs
// of formal English, and a sentence tokenizer would be a dependency for no gain.
var sentenceSplit = regexp.MustCompile(`(?:[.!?]\s+|\n)`)

// IsNoChangeAssertion reports whether one sentence is the countable unit.
//
// Exported for the fixture check below: a corpus that CONTAINS assertions must
// produce matches, and that check needs to see the same decision the scan makes.
//
// ⚠ v2 ADDED TWO REJECTIONS, and they came from reading real matches rather than
// from imagining failure modes. Measured 2026-08-27 over 46 local transcripts the
// classifier was not tuned on: v1 matched 240 sentences, and hand-judging a random
// sample of 25 found 12 genuine and 13 noise — 48% precision, which is a coin
// flip. Two noise sources were systematic and cheap to remove; the third
// (assertions about OTHER systems' APIs) is real, needs a lexicon rather than a
// rule, and is left in with its cost recorded here rather than papered over.
func IsNoChangeAssertion(sentence string) bool {
	s := strings.TrimSpace(sentence)
	if len(s) < 30 || len(s) > 400 {
		return false
	}
	// A markdown table row is cells, not a sentence. The shape words land in them
	// constantly — error columns, comparison tables — and none of it is a claim
	// the writer is making in their own voice.
	if strings.HasPrefix(s, "|") {
		return false
	}
	if !assertionSubject.MatchString(s) {
		return false
	}
	// QUOTING A CLAIM IS NOT MAKING ONE. `is not recognized as an internal or
	// external command` is an error message being pasted, and "File count alone
	// does not create an ADR" quoted from a document is that document's assertion,
	// not this session's. Both matched v1. So the shape must appear OUTSIDE the
	// backticked spans.
	return assertionShape.MatchString(stripQuoted(s))
}

// stripQuoted removes backticked spans so a shape word inside one does not count.
//
// It also drops the sentence's own quoted-string content, which is where pasted
// error text lives. Unbalanced backticks leave the tail intact rather than eating
// it — a truncated snippet should not silently swallow a real assertion after it.
func stripQuoted(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == '`' {
			in = !in
			continue
		}
		if !in {
			b.WriteRune(r)
		}
	}
	if in {
		// Unbalanced: the "quoted" tail was never closed, so treat the whole
		// sentence as unquoted rather than discarding half of it.
		return s
	}
	return b.String()
}

// recallTranscriptLine is the subset of a transcript record this reads.
//
// Separate from mineclaude transcriptLine on purpose: that one is shaped for
// mining memories and drops sidechain traffic downstream. Sharing it would drag
// that filter in, and excluding subagents from a measurement about subagents is
// the defect this task was told to avoid.
type recallTranscriptLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

// Observe scans a transcript in order and returns its counts.
//
// ok is false when the transcript could not be read. An unread transcript is NOT a
// compliant session and must not be recorded as one (spec F-5) — the difference
// between "no assertions" and "no observation" is the whole point of the store.
//
// ⚠ IT DOES NOT FILTER isSidechain, and that omission is deliberate. mineclaude.go
// drops sidechain traffic by design, so a subagent's work is invisible to it.
// Inheriting that here would measure only main sessions and report every subagent
// as absent — the population most likely to skip recall, silently excluded from the
// measurement of skipping recall.
func Observe(transcriptPath string) (Observation, bool) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return Observation{}, false
	}
	defer func() { _ = f.Close() }()

	obs := Observation{Classifier: classifierVersion}
	recalled := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // transcript lines are large
	for sc.Scan() {
		var line recallTranscriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue // a malformed line is skipped, never fatal (F-5)
		}
		if obs.SessionID == "" && line.SessionID != "" {
			obs.SessionID = line.SessionID
		}
		for _, block := range line.Message.Content {
			switch block.Type {
			case "tool_use":
				if recallTools[block.Name] {
					recalled = true
				}
			case "text":
				if line.Type != "assistant" {
					continue // the agent's own claims, not the user's
				}
				for _, s := range sentenceSplit.Split(block.Text, -1) {
					if !IsNoChangeAssertion(s) {
						continue
					}
					obs.Assertions++
					if recalled {
						obs.Preceded++
					}
				}
			}
		}
	}
	// A read error mid-file leaves what was counted so far; the session is not
	// failed over bookkeeping.
	return obs, true
}

// AppendObservation adds one line to the local store.
//
// Append-only and local. A session in which NO recall preceded an assertion is a
// ROW with preceded_by_recall = 0, never an absence — which is why search_events
// could not hold this: its rows are searches, and a miss has no search to hang on
// (spec F-17).
func AppendObservation(storePath string, obs Observation) error {
	f, err := os.OpenFile(storePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open observation store: %w", err)
	}
	defer func() { _ = f.Close() }()
	enc, err := json.Marshal(obs)
	if err != nil {
		return fmt.Errorf("encode observation: %w", err)
	}
	if _, err := f.Write(append(enc, '\n')); err != nil {
		return fmt.Errorf("append observation: %w", err)
	}
	return nil
}

// ErrClassifierMatchedNothing reports a classifier that found no assertion in a
// corpus built to contain them.
//
// This is the gate on the gate (spec F-4). A rate of 100% and a broken regex are
// the same number, and the broken one is silent: every session reports clean and
// the instrument looks like good news. Reported as an error so the instrument
// fails rather than flatters.
var ErrClassifierMatchedNothing = fmt.Errorf("classifier matched no assertion in a corpus that contains them")

// CheckClassifierMatches runs the classifier over sentences known to be assertions
// and refuses silence.
func CheckClassifierMatches(knownAssertions []string) error {
	if len(knownAssertions) == 0 {
		return fmt.Errorf("%w: the corpus itself is empty, so the check proves nothing",
			ErrClassifierMatchedNothing)
	}
	for _, s := range knownAssertions {
		if IsNoChangeAssertion(s) {
			return nil
		}
	}
	return ErrClassifierMatchedNothing
}

// recallObserveCommand records one session's recall-before-assertion counts.
//
// Plumbing rather than a user-facing verb: the Stop hook calls it with the
// transcript the harness handed it. It is registered in main.go's command list,
// which is what makes it reachable — a subcommand that exists and is not listed is
// a function nothing can call.
//
// It NEVER fails a session (ADR-041 T1, spec F-5). Every failure path exits 0
// silently, because a hook that breaks a session over bookkeeping has its
// priorities backwards.
func recallObserveCommand() *cli.Command {
	return &cli.Command{
		Name:   "recall-observe",
		Usage:  "record how many no-change assertions this session made without recalling first",
		Hidden: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "transcript", Usage: "path to the session transcript"},
			&cli.StringFlag{Name: "store", Usage: "observation store (default: beside the transcript)"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			transcript := c.String("transcript")
			if transcript == "" {
				return nil
			}
			obs, ok := Observe(transcript)
			if !ok {
				return nil // an unread transcript records nothing (F-5)
			}
			store := c.String("store")
			if store == "" {
				store = filepath.Join(filepath.Dir(transcript), "recall-observations.jsonl")
			}
			_ = AppendObservation(store, obs)
			return nil
		},
	}
}

// ADR-041 T2. Turning observations into a rate that can be quoted.

// minBaselineSessions is the floor below which no rate is reported.
//
// ⚠ FIXED BEFORE COLLECTION, and that is the whole point. T2's Stop Condition
// names the way to make this criterion impossible to fail: choose the minimum
// AFTER seeing how many observations turned up. Twenty is what the task committed
// to before any were collected.
const minBaselineSessions = 20

// ErrUndersized reports a sample too small to quote a rate from.
var ErrUndersized = fmt.Errorf("not enough observations to report a rate")

// ErrPrecisionUnknown reports a rate asked for without its precision.
//
// ⚠ THIS REFUSAL IS THE POINT OF T2's AMENDMENT. Measured 2026-08-27 over 46
// transcripts, the classifier runs at roughly 48% precision — about half the
// denominator is not the class. A bare rate would be quoted as meaning one thing
// while meaning another, and it would be quoted for a year. So the reader refuses
// to produce one rather than trusting the caller to remember the caveat.
var ErrPrecisionUnknown = fmt.Errorf("a rate cannot be reported without the precision it was judged at")

// Rate is a baseline: the number, and everything needed to know what it means.
type Rate struct {
	Sessions   int    `json:"sessions"`
	Assertions int    `json:"assertions"`
	Preceded   int    `json:"preceded_by_recall"`
	Classifier string `json:"classifier_version"`
	// Precision is hand-judged, not derived — no code can tell a genuine
	// no-change assertion from a shape-alike. It is supplied, and required.
	PrecisionPct int `json:"precision_pct"`
}

// Percent is the rate itself, and it is the least interesting field here.
func (r Rate) Percent() float64 {
	if r.Assertions == 0 {
		return 0
	}
	return 100 * float64(r.Preceded) / float64(r.Assertions)
}

// ReadObservations loads the local store.
func ReadObservations(storePath string) ([]Observation, error) {
	f, err := os.Open(storePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Observation
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var o Observation
		if json.Unmarshal(sc.Bytes(), &o) == nil {
			out = append(out, o)
		}
	}
	return out, sc.Err()
}

// ComputeRate aggregates observations into a quotable baseline, or refuses.
//
// It refuses two ways, and both are the task rather than defensive programming: an
// undersized sample (a rate from five sessions gets quoted like a rate from five
// hundred), and a missing precision figure. It also refuses to average across
// classifier versions — F-16 exists because tightening the regex would otherwise
// read as a movement in the thing being measured.
func ComputeRate(obs []Observation, precisionPct int) (Rate, error) {
	if len(obs) < minBaselineSessions {
		return Rate{}, fmt.Errorf("%w: %d session(s), need %d — a rate from a handful of "+
			"sessions is quoted exactly like a rate from hundreds", ErrUndersized, len(obs), minBaselineSessions)
	}
	if precisionPct <= 0 {
		return Rate{}, ErrPrecisionUnknown
	}
	r := Rate{Sessions: len(obs), PrecisionPct: precisionPct}
	for _, o := range obs {
		if r.Classifier == "" {
			r.Classifier = o.Classifier
		}
		if o.Classifier != r.Classifier {
			return Rate{}, fmt.Errorf("observations mix classifier versions %q and %q; rates taken "+
				"under different classifiers are not comparable and must not be averaged",
				r.Classifier, o.Classifier)
		}
		r.Assertions += o.Assertions
		r.Preceded += o.Preceded
	}
	return r, nil
}
