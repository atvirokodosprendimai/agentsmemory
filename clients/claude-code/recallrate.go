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
const classifierVersion = "v1"

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
func IsNoChangeAssertion(sentence string) bool {
	s := strings.TrimSpace(sentence)
	if len(s) < 30 || len(s) > 400 {
		return false
	}
	return assertionShape.MatchString(s) && assertionSubject.MatchString(s)
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
