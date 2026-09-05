package palace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// KG extraction turns mined prose back into relational facts: the palace holds
// thousands of drawers but only the handful of triples agents filed by hand,
// because nothing ever read the drawers and wrote the graph. This file is the
// palace half of the `kg-extract` batch command. It is deliberately LLM-free —
// it lists which sources are eligible, hands back one source's text, and
// replaces one source's triples idempotently. The model call itself lives in
// the CLI (cmd/server/kgextract.go), the same split eval keeps: generation is
// operator scaffolding, not domain logic.

// KGExtractSource is one distinct drawer source eligible for extraction: its
// identifier, how many drawers it spans (so the CLI can size expectations), and
// whether a prior run already filed triples from it. The gorm tags let the
// aggregation query scan straight into it; the json tags keep any future wire
// use snake_case like the other palace views.
type KGExtractSource struct {
	Source    string `gorm:"column:source" json:"source"`
	Drawers   int    `gorm:"column:drawers" json:"drawers"`
	Extracted bool   `gorm:"-" json:"extracted"`
}

// ExtractedTriple is one parsed `subject | predicate | object` line from the
// generator, still unvalidated — KGAdd applies the real sanitizers when the
// triple is filed.
type ExtractedTriple struct {
	Subject   string
	Predicate string
	Object    string
}

// KGReplaceResult reports what one source's replacement did: how many prior
// triples were purged, how many rows now carry this source, and how many
// generated triples KGAdd's validation refused.
type KGReplaceResult struct {
	Purged   int64 `json:"purged"`
	Filed    int   `json:"filed"`
	Rejected int   `json:"rejected"`
}

// --- repo -------------------------------------------------------------------

// DrawerSourcesByWing returns a wing's distinct drawer sources (source_file is
// the mine idempotency key; drawers filed without one — add_drawer, diary — have
// no coherent "document" to extract from and are excluded). Ordered by source so
// batch selection is deterministic across runs.
func (r *Repo) DrawerSourcesByWing(ctx context.Context, teamID, wing string) ([]KGExtractSource, error) {
	var rows []KGExtractSource
	err := r.reader.WithContext(ctx).Model(&drawerRow{}).
		Select("source_file AS source, COUNT(*) AS drawers").
		Where("team_id = ? AND wing = ? AND source_file <> ''", teamID, wing).
		Group("source_file").Order("source_file ASC").
		Scan(&rows).Error
	return rows, err
}

// DrawerContentBySource returns one source's drawer contents in chunk order —
// the pieces mine cut the original document into, ready to be concatenated back.
func (r *Repo) DrawerContentBySource(ctx context.Context, teamID, wing, source string) ([]string, error) {
	var out []string
	err := r.reader.WithContext(ctx).Model(&drawerRow{}).
		Where("team_id = ? AND wing = ? AND source_file = ?", teamID, wing, source).
		Order("chunk_index ASC, id ASC").
		Pluck("content", &out).Error
	return out, err
}

// kgExtractOrigin is the source_closet sentinel extractor-written triples carry:
// it marks a row as machine-derived AND names the wing it was derived in. Both
// halves matter. Hand-filed facts (am_kg_add) can legitimately use the same
// source label as a mined document, and purging them with the extractor's rows
// would replace human-attested facts with whatever a small model emits; and the
// same source_file can exist in two wings (drawer storage keys sources per
// wing), so an unscoped purge in one wing would eat the other wing's extraction.
func kgExtractOrigin(wing string) string { return "kg-extract:" + wing }

// KGSourceFiles returns the distinct source_file values of triples carrying one
// origin sentinel — the set of sources THIS wing's extractor already covered.
// Hand-filed triples never match: their source_closet is whatever the agent set.
func (r *Repo) KGSourceFiles(ctx context.Context, teamID, origin string) ([]string, error) {
	var out []string
	err := r.reader.WithContext(ctx).Model(&kgTripleRow{}).
		Where("team_id = ? AND source_closet = ? AND source_file <> ''", teamID, origin).
		Distinct().Pluck("source_file", &out).Error
	return out, err
}

// DeleteKGTriplesBySource hard-deletes the triples one wing's extractor filed
// from one source, reporting how many went — the KG analogue of
// DeleteClosetsBySource. This is a real DELETE, not KGInvalidate: the temporal
// "never lose history" rule protects facts about a world that changed, while a
// re-extraction merely regenerates machine-derived rows from the same drawers —
// keeping the superseded generator output as "history" would poison as-of
// queries with lines a model happened to emit once. The origin scope is what
// keeps the delete honest: only rows THIS wing's extractor wrote match, so a
// hand-filed fact that happens to share the source label survives every re-run.
// Entities the deleted triples referenced are left in place: they are cheap
// upsert rows, and another fact may still point at them.
func (r *Repo) DeleteKGTriplesBySource(ctx context.Context, teamID, origin, source string) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("team_id = ? AND source_closet = ? AND source_file = ?", teamID, origin, source).
		Delete(&kgTripleRow{})
	return res.RowsAffected, res.Error
}

// CountKGTriplesBySource counts the triples one wing's extractor currently
// carries for one source_file.
func (r *Repo) CountKGTriplesBySource(ctx context.Context, teamID, origin, source string) (int64, error) {
	var n int64
	err := r.reader.WithContext(ctx).Model(&kgTripleRow{}).
		Where("team_id = ? AND source_closet = ? AND source_file = ?", teamID, origin, source).
		Count(&n).Error
	return n, err
}

// --- service ----------------------------------------------------------------

// KGExtractSources lists a wing's distinct drawer sources, each flagged with
// whether triples already cite it, ordered never-extracted first (alphabetical
// within each half). That ordering is what makes `--limit N` runs incremental:
// each run advances into fresh sources instead of re-doing the same first N, and
// once nothing fresh remains, further runs refresh the extracted ones —
// replace-by-source keeps that safe.
func (s *Service) KGExtractSources(ctx context.Context, teamID, wing string) ([]KGExtractSource, error) {
	w, err := SanitizeName(wing, "wing")
	if err != nil {
		return nil, err
	}
	sources, err := s.repo.DrawerSourcesByWing(ctx, teamID, w)
	if err != nil {
		return nil, fmt.Errorf("list drawer sources: %w", err)
	}
	extracted, err := s.repo.KGSourceFiles(ctx, teamID, kgExtractOrigin(w))
	if err != nil {
		return nil, fmt.Errorf("list extracted sources: %w", err)
	}
	done := make(map[string]struct{}, len(extracted))
	for _, src := range extracted {
		done[src] = struct{}{}
	}
	for i := range sources {
		_, sources[i].Extracted = done[sources[i].Source]
	}
	sort.SliceStable(sources, func(i, j int) bool {
		return !sources[i].Extracted && sources[j].Extracted
	})
	return sources, nil
}

// KGSourceText returns one source's drawer contents concatenated in chunk order
// — the text the extraction prompt reads. Mined chunks overlap by 20%, so the
// concatenation repeats some text at the seams; that is left as-is, because the
// repeated facts it yields collapse in KGAdd's dedup rather than needing to be
// stitched out here.
func (s *Service) KGSourceText(ctx context.Context, teamID, wing, source string) (string, error) {
	w, err := SanitizeName(wing, "wing")
	if err != nil {
		return "", err
	}
	src, err := sanitizeSource(source)
	if err != nil {
		return "", err
	}
	parts, err := s.repo.DrawerContentBySource(ctx, teamID, w, src)
	if err != nil {
		return "", fmt.Errorf("read source drawers: %w", err)
	}
	return strings.Join(parts, "\n\n"), nil
}

// KGReplaceSource files one source's freshly extracted triples, first purging
// whatever a prior extraction filed from the same source, so a re-run replaces
// rather than accumulates — mirroring how Mine purges a source's closets before
// rewriting them. The purge runs only once the caller has the new triples in
// hand: generation failing must never cost the graph its previous extraction.
//
// A triple KGAdd's validation refuses (over-long value, unsafe predicate) is the
// model's fault, not the operator's, so it is counted in Rejected and skipped;
// any other error is a storage fault and aborts. Filed is the row count with
// this source_file after filing, NOT the number of KGAdd calls: KGAdd dedups
// against the whole team, so a fact already known from another source keeps its
// original provenance and would over-count here.
func (s *Service) KGReplaceSource(ctx context.Context, teamID, wing, source string, triples []ExtractedTriple) (KGReplaceResult, error) {
	w, err := SanitizeName(wing, "wing")
	if err != nil {
		return KGReplaceResult{}, err
	}
	src, err := sanitizeSource(source)
	if err != nil {
		return KGReplaceResult{}, err
	}
	origin := kgExtractOrigin(w)
	purged, err := s.repo.DeleteKGTriplesBySource(ctx, teamID, origin, src)
	if err != nil {
		return KGReplaceResult{}, fmt.Errorf("purge prior triples of %q: %w", src, err)
	}
	rejected := 0
	for _, t := range triples {
		if _, err := s.KGAdd(ctx, teamID, t.Subject, t.Predicate, t.Object, "", "", origin, src, ""); err != nil {
			if errors.Is(err, ErrInvalidInput) {
				rejected++
				continue
			}
			return KGReplaceResult{}, fmt.Errorf("file triple from %q: %w", src, err)
		}
	}
	filed, err := s.writer.CountKGTriplesBySource(ctx, teamID, origin, src)
	if err != nil {
		return KGReplaceResult{}, fmt.Errorf("count filed triples of %q: %w", src, err)
	}
	return KGReplaceResult{Purged: purged, Filed: int(filed), Rejected: rejected}, nil
}
