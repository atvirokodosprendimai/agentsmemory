package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// ADR-038 T6. `doctor --corpus` is the rung this ADR was failing: every finding
// behind the record was produced by a throwaway script, and a drift query living
// in a sign-off line is a capability no operator can discover.

// TestDoctorCorpusReportsDriftAndDanglingReferences builds the drift it asserts
// on, rather than asserting that a clean corpus reports clean.
//
// A check whose fixture cannot exhibit the defect is unfalsifiable however
// carefully it is worded — and that is the specific way this repository's checks
// have failed before. Each of the four findings gets a row that is genuinely
// broken in that one way.
func TestDoctorCorpusReportsDriftAndDanglingReferences(t *testing.T) {
	f := corpusFindings{
		Drawers:          1705,
		Facts:            207,
		DriftedKeys:      []string{"aaa", "bbb"},
		LostParents:      []string{"ccc"},
		LostAnchors:      []string{"ddd"},
		LostFacts:        []string{"eee"},
		EndedFactSources: 3,
	}
	var out bytes.Buffer
	err := reportCorpus(&out, "wing_alpha", f)
	if err == nil {
		t.Fatal("a corpus with drifted keys and three classes of lost reference reported success; " +
			"the exit code is this check's whole verdict")
	}
	text := out.String()
	for _, want := range []string{"aaa", "bbb", "ccc", "ddd", "eee", "1705", "207"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report omits %q, so an operator cannot act on it:\n%s", want, text)
		}
	}
	// The three states, kept apart. A fact citing an ENDED drawer is the system
	// working — provenance is historical — and folding it into the lost count would
	// report a deliberate feature as a fault on every palace that uses corrections.
	if !strings.Contains(text, "retracted or superseded") {
		t.Errorf("the report does not separate facts citing an ENDED drawer from facts citing "+
			"nothing. One is expected and one is a defect:\n%s", text)
	}
	if strings.Contains(err.Error(), "3 ") {
		t.Errorf("the ended-source count leaked into the failure verdict: %v", err)
	}

	// A clean corpus exits 0 AND still says what it looked at, because "0 facts
	// cite a retracted drawer" and "this check does not look at that" are different
	// statements and only one of them is reassuring.
	out.Reset()
	if err := reportCorpus(&out, "wing_alpha", corpusFindings{Drawers: 3, Facts: 1}); err != nil {
		t.Fatalf("a clean corpus must exit 0: %v", err)
	}
	if !strings.Contains(out.String(), "no drift and no lost references") {
		t.Errorf("a clean run says nothing about what it checked:\n%s", out.String())
	}
}

// TestDoctorCorpusFindsRealDriftInARealDatabase drives the walk against a migrated
// SQLite palace, because reportCorpus above is only the rendering — the query that
// decides what to render is where a check silently stops finding anything.
func TestDoctorCorpusFindsRealDriftInARealDatabase(t *testing.T) {
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	ctx := context.Background()
	added, err := svc.drawers.Add(ctx, teamID, palace.AddInput{
		Wing: "wing_alpha", Room: "decisions", Content: "a memory whose key will be broken",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := added.Drawers[0].ID

	// A clean corpus first, or the assertion below cannot tell "found the drift"
	// from "reports everything as drifted".
	clean, err := walkCorpus(ctx, svc, teamID)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !clean.clean() {
		t.Fatalf("a freshly filed corpus already reports findings: %+v", clean)
	}
	if clean.Drawers == 0 {
		t.Fatal("the walk read no drawers, so it would report clean on any database")
	}

	// Break exactly one thing, the way a wing relabel used to: move the row and
	// leave the key behind.
	if err := svc.gdb.Exec(`UPDATE drawers SET wing = 'wing_beta' WHERE id = ?`, id).Error; err != nil {
		t.Fatalf("drift the row: %v", err)
	}
	// And point a fact at a drawer that does not exist.
	if err := svc.gdb.Exec(
		`INSERT INTO kg_triples (team_id, id, subject, predicate, object, valid_from, valid_to, confidence, source_closet, source_file, source_drawer_id, extracted_at)
		 VALUES (?, 't_lost', 'svc', 'documented_in', 'nowhere', '', '', 1.0, '', '', 'no-such-drawer', '2026-08-27T00:00:00Z')`,
		teamID).Error; err != nil {
		t.Fatalf("seed lost fact: %v", err)
	}

	found, err := walkCorpus(ctx, svc, teamID)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(found.DriftedKeys) != 1 || found.DriftedKeys[0] != id {
		t.Errorf("drifted keys = %v; want exactly [%s]. A row whose wing moved without its key is "+
			"invisible to dedup: every re-file inserts beside it", found.DriftedKeys, id)
	}
	if len(found.LostFacts) != 1 || found.LostFacts[0] != "no-such-drawer" {
		t.Errorf("lost facts = %v; want exactly [no-such-drawer]", found.LostFacts)
	}
	if found.clean() {
		t.Error("the walk reported clean with a drifted row and a lost fact in the database")
	}
}

// TestDoctorCorpusCountsAnEndedSourceSeparately is the three-states rule at the
// query, not just in the report.
//
// A fact keeps its source_drawer_id when that drawer is superseded, deliberately:
// the fact WAS extracted from that text, and re-pointing it at the successor would
// assert that the corrected text still supports it. So an ended source must land
// in EndedFactSources and never in LostFacts — otherwise every palace that uses
// corrections reports a growing pile of phantom defects.
func TestDoctorCorpusCountsAnEndedSourceSeparately(t *testing.T) {
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	ctx := context.Background()
	added, err := svc.drawers.Add(ctx, teamID, palace.AddInput{
		Wing: "wing_alpha", Room: "decisions", Content: "the claim a fact was extracted from",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := added.Drawers[0].ID
	if _, err := svc.drawers.KGAdd(ctx, teamID, "svc", "documented in", "the note", "", "", "", "", id); err != nil {
		t.Fatalf("seed fact: %v", err)
	}
	if err := svc.drawers.InvalidateDrawer(ctx, teamID, id, "the claim was withdrawn"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	f, err := walkCorpus(ctx, svc, teamID)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	// At least one, not exactly one: filing a drawer also writes a derived room
	// edge carrying the same source_drawer_id, so retracting the drawer leaves two
	// facts citing it. The property under test is the CLASSIFICATION, not the count
	// — pinning an exact number here would make this test fail the day the derived
	// edges change, which is a different subject entirely.
	if f.EndedFactSources < 1 {
		t.Errorf("EndedFactSources = %d; want at least 1", f.EndedFactSources)
	}
	if len(f.LostFacts) != 0 {
		t.Errorf("lost facts = %v; a fact citing a RETRACTED drawer is the system working, and "+
			"counting it as lost reports the feature as a fault", f.LostFacts)
	}
	if !f.clean() {
		t.Error("a corpus whose only unusual state is a deliberate retraction reported a finding")
	}
}

// TestDoctorCorpusIsAdvertisedInHelp is rung 3, and the only rung a behavioural
// test cannot reach: a check nobody can find is a check nobody runs.
func TestDoctorCorpusIsAdvertisedInHelp(t *testing.T) {
	desc := doctorCommand(config.Default()).Description
	block := desc[strings.Index(desc, "integrity checks"):]
	if i := strings.Index(block, "diagnostic reports"); i > 0 {
		block = block[:i]
	}
	if !strings.Contains(block, "--corpus") {
		t.Errorf("--corpus is not in the INTEGRITY block of doctor --help. It exits non-zero on a "+
			"finding, so listing it among the diagnostic reports — which explicitly do not declare "+
			"palace health — would misdescribe what its exit code means:\n%s", block)
	}
}

// TestDoctorCorpusIsReachable is rung 2, and it is invisible to
// TestEveryFlagIsRead: --corpus IS read, and was read in a block nothing could
// reach. --index used to `return`, so any check dispatched after it never ran when
// both flags were passed — and a passing --index hid a failing --corpus.
func TestDoctorCorpusIsReachable(t *testing.T) {
	cfg, _ := eraseTestWorkspace(t)

	run := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		cmd := doctorCommand(cfg)
		var out bytes.Buffer
		cmd.Writer, cmd.ErrWriter = &out, &out
		err := cmd.Run(context.Background(), append([]string{"doctor",
			"--db", cfg.DBPath, "--project", "local"}, args...))
		return out.String(), err
	}

	if _, err := run(t, "--corpus"); err != nil && strings.Contains(err.Error(), "nothing to check") {
		t.Fatal("--corpus alone exits with \"nothing to check\": the dispatch guard does not name " +
			"it, so the flag is declared, documented, read — and unreachable")
	}
	// Both together: --index must not swallow the corpus report on its way out.
	out, _ := run(t, "--corpus", "--index")
	if !strings.Contains(out, "corpus ") {
		t.Errorf("--corpus produced no report when passed alongside --index; a check dispatched "+
			"after an early return runs only when nothing before it does:\n%s", out)
	}
}
