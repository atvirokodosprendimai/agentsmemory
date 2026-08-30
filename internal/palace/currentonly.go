package palace

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

// maxCarriedReasonRunes caps the ending reason carried onto a LIVE record.
//
// A reason is free text an agent writes and nothing bounds it at the write end,
// while the carried copy rides every hit of every page. Without a cap the recall
// payload grows with the corpus rather than with the page, which is the property
// this repository already spent a ceiling on once (wholeMemoryBudget).
//
// It applies ONLY to the carried copy. The predecessor keeps its reason whole,
// reachable through the history route — truncating the stored text would make the
// cap a data-loss decision instead of a payload one.
const maxCarriedReasonRunes = 200

// truncateReason cuts a carried reason to maxCarriedReasonRunes on a WORD
// boundary and marks the cut with an ellipsis.
//
// On a boundary because a fragment severed mid-word is a reason nobody can read,
// and a reason nobody can read is a reason nobody acts on. Marked because an
// unmarked cut is indistinguishable from a short reason, so a reader cannot tell
// whether there is more to fetch.
func truncateReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) <= maxCarriedReasonRunes {
		return reason
	}
	runes := []rune(reason)
	// One rune short of the cap, so the ellipsis fits inside it rather than
	// pushing the result one over — a cap the marker can exceed is not a cap.
	cut := string(runes[:maxCarriedReasonRunes-1])
	if i := strings.LastIndexAny(cut, " \t\n"); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \t\n.,;:") + "…"
}

// PredecessorsOf returns, for each of the given live drawer ids, the ended record
// that names it as its successor — keyed by the LIVE id, not by its own.
//
// Keyed that way because the caller holds live records and wants "what did this
// replace"; the column that answers is superseded_by on the OTHER row, so the
// query runs in that direction and the map is built to be indexed by the id the
// caller already has.
func (r *Repo) PredecessorsOf(ctx context.Context, teamID string, ids []string) (map[string]Drawer, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// Batched through chunkIDs, like every other IN-list read here. A search or
	// list page is bounded by limit, but limit is caller-supplied and the same
	// helper already exists — an unbatched bind list is a query that works until
	// the day somebody asks for a big page.
	//
	// Served by idx_drawers_superseded_by (00033), which is partial on the same
	// `!= ''` predicate: idx_drawers_current cannot answer this one.
	out := make(map[string]Drawer, len(ids))
	for _, batch := range chunkIDs(ids) {
		var rows []drawerRow
		if err := r.db.WithContext(ctx).Model(&drawerRow{}).
			Where("team_id = ? AND superseded_by IN ?", teamID, batch).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		collectPredecessors(out, rows)
	}
	return out, nil
}

// collectPredecessors folds a batch of ended rows into the by-successor map.
//
// A memory ends whole, so several ended chunks name the same successor. Chunk 0
// wins: it is the text a reader would have read, and the others carry the
// identical reason anyway — one supersede writes one reason — so this only
// decides which id is reported.
func collectPredecessors(out map[string]Drawer, rows []drawerRow) {
	for _, row := range rows {
		// Two DISTINCT predecessors can converge on one successor — a re-file that
		// dedupes two memories into the same text — and then both have ChunkIndex
		// 0. The id breaks that tie so the answer is deterministic rather than
		// whatever the scan happened to reach first; reporting the full chain is
		// history-flag territory (BACKLOG).
		prev, seen := out[row.SupersededBy]
		if seen && (prev.ChunkIndex < row.ChunkIndex ||
			(prev.ChunkIndex == row.ChunkIndex && prev.ID <= row.ID)) {
			continue
		}
		out[row.SupersededBy] = fromRow(row)
	}
}

// attachSupersedes resolves what each live record replaced and writes it onto the
// records in place, with the reason truncated for carrying.
//
// One query for the whole page rather than one per record: this runs on every
// default read route, and a per-hit lookup would make recall cost scale with page
// size in round trips rather than in rows.
func (s *Service) attachSupersedes(ctx context.Context, teamID string, ds []Drawer) error {
	if len(ds) == 0 {
		return nil
	}
	// Keyed by the memory ROOT, not by the row's own id, and that asymmetry is the
	// whole correctness of this function. supersedeInto stamps every ended chunk's
	// superseded_by with the SUCCESSOR'S ROOT (`added.Drawers[0].ID`), while a
	// search page's representative is whichever CHUNK matched — often a child. A
	// lookup by the row's own id therefore finds nothing for exactly the
	// multi-chunk memories a correction most often replaces, and the record comes
	// back with no lineage while the invariant says it always carries one.
	ids := make([]string, 0, len(ds))
	seen := make(map[string]bool, len(ds))
	for _, d := range ds {
		root := memoryOf(d)
		if !seen[root] {
			seen[root] = true
			ids = append(ids, root)
		}
	}
	prev, err := s.repo.PredecessorsOf(ctx, teamID, ids)
	if err != nil {
		return err
	}
	if len(prev) == 0 {
		return nil
	}
	for i := range ds {
		p, ok := prev[memoryOf(ds[i])]
		if !ok {
			continue
		}
		ds[i].Supersedes = p.ID
		ds[i].SupersededReason = truncateReason(p.EndedReason)
	}
	return nil
}

// GetAnyVersion returns a drawer by id whether or not it has been ended — the
// single history route for one record.
//
// It is also what the write paths read with. EndDrawer refuses an already-ended
// drawer by NAME ("already ended on X, because …"), supersedeInto checks the head
// chunk's window, and Update's multi-chunk guard counts chunks: all three need the
// ended row, and reading through the current-only Get would turn each of those
// precise refusals into a bare "not found" for a row that plainly exists.
func (s *Service) GetAnyVersion(ctx context.Context, teamID, id string) (Drawer, error) {
	d, err := s.repo.Get(ctx, teamID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Drawer{}, ErrNotFound
	}
	if err != nil {
		return Drawer{}, err
	}
	one := []Drawer{d}
	if err := s.attachSupersedes(ctx, teamID, one); err != nil {
		return Drawer{}, err
	}
	return one[0], nil
}

// ListAnyVersion is List including records that have been ended.
func (s *Service) ListAnyVersion(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	out, err := s.repo.List(ctx, teamID, wing, room, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := s.attachSupersedes(ctx, teamID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachSupersedesToHits is attachSupersedes over a ranked page.
//
// SearchHit embeds Drawer by value, so the page is walked by index and the
// resolved fields written back onto the hit — a copy would be silently discarded.
func (s *Service) attachSupersedesToHits(ctx context.Context, teamID string, hits []SearchHit) error {
	if len(hits) == 0 {
		return nil
	}
	ds := make([]Drawer, len(hits))
	for i, h := range hits {
		ds[i] = h.Drawer
	}
	if err := s.attachSupersedes(ctx, teamID, ds); err != nil {
		return err
	}
	for i := range hits {
		hits[i].Drawer = ds[i]
	}
	return nil
}

// GetMemoryAnyVersion is GetMemory with the history route folded in, so the two
// live behind one call rather than two the caller has to choose between.
//
// includeHistory=false is exactly GetMemory. True returns every chunk whatever its
// window — which, after T4, is all-current or all-ended for any one memory, since
// a supersede ends the whole memory in one pass.
func (s *Service) GetMemoryAnyVersion(ctx context.Context, teamID, id string, includeHistory bool) ([]Drawer, error) {
	if !includeHistory {
		return s.GetMemory(ctx, teamID, id)
	}
	chunks, err := s.repo.MemoryChunks(ctx, teamID, id)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, ErrNotFound
	}
	if err := s.attachSupersedes(ctx, teamID, chunks); err != nil {
		return nil, err
	}
	return chunks, nil
}

// ListCurrent is the page of CURRENT records, without the supersedes lineage
// List resolves onto them.
//
// It exists for the transfer paths — a wing bundle, a cross-workspace copy —
// which move rows rather than present them to a reader, and for which the
// per-page predecessor lookup is work nobody consumes. Same predicate as List, so
// a bundle and a recall agree on what "current" means.
func (s *Service) ListCurrent(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	return s.repo.ListCurrent(ctx, teamID, wing, room, limit, offset)
}
