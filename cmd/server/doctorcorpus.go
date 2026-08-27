// doctorcorpus.go answers the question every finding behind ADR-038 was produced
// by a throwaway script to answer, and that nothing in the tree could answer.
//
// The numbers that motivated that record — 27 drifted rows of 1,705, 39 of 41
// anchored drawers one re-file from losing their pin, 16 knowledge-graph facts
// pointing at a drawer that no longer existed — came from ad-hoc SQL run once, on
// one day, by one person. `doctor` already carried three integrity checks that
// exit non-zero on a finding, and not one of them read the corpus. A drift query
// recorded in a sign-off line is this repository's own rung 3: the capability
// exists and its intended caller cannot discover it.
package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// corpusFindings is what a corpus walk found, in ids and counts only.
//
// ⚠ THREE STATES, NOT TWO, and conflating any pair of them reports a working
// feature as a fault. Since ADR-038 a row can be ENDED — retracted or superseded,
// its text kept on purpose — and a pointer at an ended row is the system working:
//
//   - a reference resolving to a CURRENT row is ordinary;
//   - a reference resolving to an ENDED row is ordinary too. A knowledge-graph
//     fact keeps its source_drawer_id when that drawer is superseded, deliberately:
//     provenance is historical, the fact WAS extracted from that text, and
//     re-pointing it at the successor would assert that the corrected text still
//     supports it — which a correction may have removed;
//   - a reference resolving to NOTHING is the finding.
//
// Reported separately so an operator reading a non-zero count knows which of the
// three they are looking at. A single "dangling" number would answer none of them.
type corpusFindings struct {
	Drawers int // rows walked
	Facts   int // kg_triples walked

	// DriftedKeys are rows whose stored content_key disagrees with the hash of
	// their own fields — the row moved and the key did not.
	DriftedKeys []string

	// Lost* are references that resolve to no row at all.
	LostParents []string
	LostAnchors []string
	LostFacts   []string

	// EndedFactSources are facts whose source drawer was retracted or superseded.
	// NOT a finding: counted so a reader can tell this population from LostFacts,
	// which is the distinction the ad-hoc queries could not make because endings
	// did not exist when they were written.
	EndedFactSources int
}

// clean reports whether the walk found anything an operator must act on.
func (f corpusFindings) clean() bool {
	return len(f.DriftedKeys) == 0 && len(f.LostParents) == 0 &&
		len(f.LostAnchors) == 0 && len(f.LostFacts) == 0
}

// corpusRow is the subset of a drawer this check reads.
//
// Content IS read, because the key hashes it and drift cannot be detected without
// recomputing the same hash — but it is never PRINTED. The invariant is about the
// report, which gets pasted into an issue describing a private palace, not about
// what the process may hold for the length of a comparison.
type corpusRow struct {
	ID         string
	TeamID     string
	Wing       string
	Room       string
	SourceFile string
	ChunkIndex int
	Content    string
	ContentKey string
	ValidTo    string
	ParentID   string
}

// doctorCorpus walks one workspace's drawers, anchors and facts and reports what
// no longer hangs together.
func doctorCorpus(ctx context.Context, cfg config.Config, slug string, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	svc, err := inspectDatabaseServices(cfg)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}
	f, err := walkCorpus(ctx, svc, team.ID)
	if err != nil {
		return err
	}
	return reportCorpus(out, team.Slug, f)
}

// walkCorpus does the reading. Split from the report for the reason every other
// doctor check is split: the rendering is what an operator reads, and a report
// that needs a live database to test is one whose wording nobody checks.
func walkCorpus(ctx context.Context, svc *services, teamID string) (corpusFindings, error) {
	var f corpusFindings
	var rows []corpusRow
	if err := svc.gdb.WithContext(ctx).
		Table("drawers").
		Select("id", "team_id", "wing", "room", "source_file", "chunk_index", "content", "content_key", "valid_to", "parent_id").
		Where("team_id = ?", teamID).Scan(&rows).Error; err != nil {
		return f, fmt.Errorf("walk drawers: %w", err)
	}
	f.Drawers = len(rows)

	live := make(map[string]string, len(rows)) // id -> valid_to
	for _, r := range rows {
		live[r.ID] = r.ValidTo
	}

	for _, r := range rows {
		// A key that disagrees with the row's own fields. Diary rows are exempt by
		// design (an empty key keeps a journal out of dedup), and a row filed before
		// the backfill has no key yet — neither is drift.
		if r.ContentKey != "" && r.Room != "diary" {
			if want := corpusContentKey(r); want != r.ContentKey {
				f.DriftedKeys = append(f.DriftedKeys, r.ID)
			}
		}
		if r.ParentID != "" {
			if _, ok := live[r.ParentID]; !ok {
				f.LostParents = append(f.LostParents, r.ID)
			}
		}
	}

	var anchorIDs []string
	if err := svc.gdb.WithContext(ctx).Table("drawer_anchors").
		Where("team_id = ?", teamID).Pluck("drawer_id", &anchorIDs).Error; err != nil {
		return f, fmt.Errorf("walk anchors: %w", err)
	}
	for _, id := range anchorIDs {
		if _, ok := live[id]; !ok {
			f.LostAnchors = append(f.LostAnchors, id)
		}
	}

	var factSources []string
	if err := svc.gdb.WithContext(ctx).Table("kg_triples").
		Where("team_id = ? AND source_drawer_id != ''", teamID).
		Pluck("source_drawer_id", &factSources).Error; err != nil {
		return f, fmt.Errorf("walk facts: %w", err)
	}
	var factCount int64
	_ = svc.gdb.WithContext(ctx).Table("kg_triples").Where("team_id = ?", teamID).Count(&factCount).Error
	f.Facts = int(factCount)
	for _, id := range factSources {
		validTo, ok := live[id]
		switch {
		case !ok:
			f.LostFacts = append(f.LostFacts, id)
		case validTo != "":
			f.EndedFactSources++
		}
	}

	sort.Strings(f.DriftedKeys)
	sort.Strings(f.LostParents)
	sort.Strings(f.LostAnchors)
	sort.Strings(f.LostFacts)
	return f, nil
}

// corpusContentKey recomputes what a row's key should be, through the palace's own
// recipe rather than a copy of it.
//
// Calling palace.DrawerID here is deliberate and is the ONE place outside
// contentKeyOf that may: a checker with its own copy of the hash would agree with
// itself forever and report a clean corpus whatever the palace actually wrote.
// TestNoPathRederivesADrawerID guards the palace package, which is where the
// exemption lives; this is a different package and a different job.
func corpusContentKey(r corpusRow) string {
	return palace.DrawerID(r.TeamID, r.Wing, r.Room, r.SourceFile, r.ChunkIndex, r.Content)
}

// reportCorpus renders the verdict and returns it as an error so the exit code
// carries it, exactly as --index and --schema do.
func reportCorpus(out io.Writer, slug string, f corpusFindings) error {
	fmt.Fprintf(out, "corpus %q: %d drawers, %d facts\n", slug, f.Drawers, f.Facts)
	// Printed even at zero, because "0 facts point at a retracted drawer" and "this
	// check does not look at that" are different statements and only one of them is
	// reassuring.
	fmt.Fprintf(out, "  %d fact(s) cite a retracted or superseded drawer — expected: provenance is historical\n",
		f.EndedFactSources)

	if f.clean() {
		fmt.Fprintln(out, "  no drift and no lost references")
		return nil
	}
	var parts []string
	for _, group := range []struct {
		label string
		ids   []string
		why   string
	}{
		{"content keys disagree with their own row", f.DriftedKeys, "the row moved and its key did not, so dedup will not match it"},
		{"parent_id names no row", f.LostParents, "a chunk whose memory is gone"},
		{"anchors name no row", f.LostAnchors, "a pin on text nobody can read"},
		{"facts name no row", f.LostFacts, "provenance that resolves to nothing — NOT the same as citing an ended drawer, above"},
	} {
		if len(group.ids) == 0 {
			continue
		}
		fmt.Fprintf(out, "  %d %s (%s):\n", len(group.ids), group.label, group.why)
		for _, id := range shortSample(group.ids) {
			fmt.Fprintf(out, "    %s\n", id)
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(group.ids), group.label))
	}
	return fmt.Errorf("corpus check failed: %s", strings.Join(parts, "; "))
}

// corpusSample bounds how many ids a report prints per finding. A doctor report is
// pasted, and a wing with thousands of drifted rows must not produce thousands of
// lines — the count above is the number that matters.
const corpusSample = 10

// shortSample returns at most corpusSample ids, saying how many were elided.
func shortSample(ids []string) []string {
	if len(ids) <= corpusSample {
		return ids
	}
	out := append([]string(nil), ids[:corpusSample]...)
	return append(out, fmt.Sprintf("… and %d more", len(ids)-corpusSample))
}
