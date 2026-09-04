package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/anchorcontract"
)

// Code anchors: the mechanism that lets a memory notice the code it describes has
// changed underneath it.
//
// A drawer that says "installer.go pins CLAUDE_CONFIG_DIR on every call" is worth
// far more than a code search — it carries WHY, which the code never states. It is
// also the one kind of memory that can go quietly wrong: the code gets fixed, the
// sentence does not, and the next session acts on a fact that stopped being true.
// Anchoring the memory to the exact snippet it is about turns that from an
// invisible failure into a flag on the search result.

// Anchor statuses. unchecked is the initial state; the other three are verdicts
// from a verification pass that could actually see the working tree.
const (
	AnchorUnchecked = "unchecked"
	AnchorVerified  = "verified" // the snippet is still in the file
	AnchorDrifted   = "drifted"  // the file is there, the snippet is not
	AnchorMissing   = "missing"  // the file itself is gone
)

// maxAnchorSnippet bounds a pinned snippet. An anchor is a fingerprint, not a
// copy of the file: a few lines identify the code uniquely, while a whole
// function would drift on any edit to any part of it and flag itself constantly.
const maxAnchorSnippet = 2000

// Anchor pins one drawer to one piece of code.
type Anchor struct {
	ID        string
	DrawerID  string
	Repo      string
	Path      string
	Snippet   string
	Status    string
	Line      int    // where the snippet was last found (0 = not located)
	CheckedAt string // RFC3339, empty while unchecked
}

// Stale reports whether this anchor's code has moved on without the memory.
func (a Anchor) Stale() bool { return a.Status == AnchorDrifted || a.Status == AnchorMissing }

// anchorRow is the gorm view of the drawer_anchors table.
type anchorRow struct {
	ID         string `gorm:"column:id;primaryKey"`
	TeamID     string `gorm:"column:team_id"`
	DrawerID   string `gorm:"column:drawer_id"`
	Repo       string `gorm:"column:repo"`
	Path       string `gorm:"column:path"`
	Snippet    string `gorm:"column:snippet"`
	SnippetSHA string `gorm:"column:snippet_sha"`
	Status     string `gorm:"column:status"`
	Line       int    `gorm:"column:line"`
	CheckedAt  string `gorm:"column:checked_at"`
	CreatedAt  string `gorm:"column:created_at"`
}

// TableName pins the table name so gorm does not pluralise the struct name.
func (anchorRow) TableName() string { return "drawer_anchors" }

// AnchorInput is one anchor as a caller supplies it: which file, and the verbatim
// code the memory is about. No line number — see the migration for why.
type AnchorInput struct {
	Repo    string
	Path    string
	Snippet string
}

// anchorID is deterministic in (team, drawer, path, snippet), so re-filing the
// same memory with the same anchor updates it rather than accumulating copies.
func anchorID(teamID, drawerID, path, snippet string) string {
	sum := sha256.Sum256([]byte(teamID + "\x00" + drawerID + "\x00" + path + "\x00" + anchorcontract.NormalizeSnippet(snippet)))
	return hex.EncodeToString(sum[:])[:32]
}

// snippetSHA fingerprints the normalized snippet.
func snippetSHA(snippet string) string {
	sum := sha256.Sum256([]byte(anchorcontract.NormalizeSnippet(snippet)))
	return hex.EncodeToString(sum[:])
}

// AddAnchors pins a drawer to code. It is idempotent per (drawer, path, snippet)
// and preserves an existing anchor's verdict, so re-filing a memory does not reset
// what verification already learned about it.
func (s *Service) AddAnchors(ctx context.Context, teamID, drawerID string, in []AnchorInput) (int, error) {
	if drawerID == "" {
		return 0, fmt.Errorf("%w: drawer id is required", ErrInvalidInput)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows := make([]anchorRow, 0, len(in))
	for _, a := range in {
		path := strings.TrimSpace(a.Path)
		snippet := strings.TrimSpace(a.Snippet)
		if path == "" || snippet == "" {
			return 0, fmt.Errorf("%w: each anchor needs a path and a snippet", ErrInvalidInput)
		}
		if len([]rune(snippet)) > maxAnchorSnippet {
			snippet = string([]rune(snippet)[:maxAnchorSnippet])
		}
		rows = append(rows, anchorRow{
			ID:         anchorID(teamID, drawerID, path, snippet),
			TeamID:     teamID,
			DrawerID:   drawerID,
			Repo:       strings.TrimSpace(a.Repo),
			Path:       path,
			Snippet:    snippet,
			SnippetSHA: snippetSHA(snippet),
			Status:     AnchorUnchecked,
			CreatedAt:  now,
		})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	// Insert-if-absent: an existing row keeps its status, line and checked_at,
	// because a verdict is knowledge and re-filing the memory learns nothing new
	// about the code.
	for _, row := range rows {
		var existing int64
		if err := s.repo.db.WithContext(ctx).Model(&anchorRow{}).
			Where("id = ?", row.ID).Count(&existing).Error; err != nil {
			return 0, fmt.Errorf("check anchor: %w", err)
		}
		if existing > 0 {
			continue
		}
		if err := s.repo.db.WithContext(ctx).Create(&row).Error; err != nil {
			return 0, fmt.Errorf("save anchor: %w", err)
		}
	}
	return len(rows), nil
}

// AnchorFilter narrows a listing: by wing (via the drawers it holds), by repo
// label, or by status.
type AnchorFilter struct {
	Wing   string
	Repo   string
	Status string
	Limit  int

	// Path narrows to the anchors pinned to exactly this file (ADR-051 T2).
	//
	// It exists so a memory can be surfaced at the moment its file is opened,
	// without anything being asked. That is deliberately NOT a search: ADR-041's
	// T5 is stopped because the only query available at PreToolUse is a bare grep
	// pattern, and a bare identifier retrieves a session's narrative more often
	// than a team's decision. An anchor is an exact pin, so this is a join on a
	// string the tool call already names — nothing is ranked, and there is no
	// relevance to fall short of.
	//
	// Exact match, not a prefix or a glob. A directory-wide cue fires on files
	// nothing was said about, and a cue that fires when it has nothing to say is
	// how a channel gets ignored.
	Path string

	// IncludeEnded brings back anchors on superseded and retracted drawers, which
	// the default excludes.
	//
	// It is opt-in because the two readers want opposite things. A VERIFIER wants
	// questions it can act on, and an ended record's pin is unfixable by
	// construction — Service.Update refuses an ended record (ADR-038 ends rather
	// than overwrites), so a drifted verdict there can never be cleared and
	// re-reports at every session start forever. An AUDITOR wants the whole
	// corpus, including the pins that ended records still legitimately carry:
	// those rows are kept on purpose, because an ended record keeps its text and
	// its pin is still true OF THAT TEXT.
	IncludeEnded bool
}

// ListAnchors returns anchors to check, newest drawers first. It is what the
// host-side `verify` command reads: the server cannot see the working tree, so it
// hands out the questions and takes back the answers.
func (s *Service) ListAnchors(ctx context.Context, teamID string, f AnchorFilter) ([]Anchor, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	// Join drawers unconditionally: an anchor whose drawer no longer exists must
	// never reach a verifier, or it reports drift on a memory that is gone. The
	// delete path prunes anchors too, so this is the belt to that braces — cheap,
	// and it also covers rows deleted by any path added later.
	//
	// ⚠ AND AN ENDED DRAWER IS GONE IN THE SENSE THAT MATTERS, WHICH THE FIRST
	// VERSION OF THIS JOIN DID NOT COVER. A superseded record's anchors are drifted
	// almost by construction, because a record is usually superseded precisely when
	// the code it pinned changed — and nothing could ever clear the report:
	// Service.Update refuses an ended record, correctly, since ADR-038 ends records
	// instead of overwriting them. So the sweep asked forever and the answer could
	// never be filed. Reported from another project on 2026-09-03 after two such
	// anchors survived every attempt to fix them. The successor carries its own
	// anchors and is the record a session should be reading.
	join := "JOIN drawers ON drawers.id = drawer_anchors.drawer_id AND drawers.team_id = drawer_anchors.team_id"
	if !f.IncludeEnded {
		join += " AND drawers.valid_to = ''"
	}
	q := s.repo.reader.WithContext(ctx).Model(&anchorRow{}).
		Joins(join).
		Where("drawer_anchors.team_id = ?", teamID)
	if f.Repo != "" {
		q = q.Where("drawer_anchors.repo = ?", f.Repo)
	}
	if f.Status != "" {
		q = q.Where("drawer_anchors.status = ?", f.Status)
	}
	if f.Path != "" {
		q = q.Where("drawer_anchors.path = ?", f.Path)
	}
	if f.Wing != "" {
		// Wing lives on the drawer (already joined above), so scope through it —
		// one join beats storing the wing twice and letting the copies disagree
		// after a merge_wing.
		q = q.Where("drawers.wing = ?", f.Wing)
	}
	var rows []anchorRow
	if err := q.Order("drawer_anchors.created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list anchors: %w", err)
	}
	out := make([]Anchor, 0, len(rows))
	for _, r := range rows {
		out = append(out, Anchor{
			ID: r.ID, DrawerID: r.DrawerID, Repo: r.Repo, Path: r.Path,
			Snippet: r.Snippet, Status: r.Status, Line: r.Line, CheckedAt: r.CheckedAt,
		})
	}
	return out, nil
}

// AnchorVerdict is one verification result coming back from a client that could
// read the file.
type AnchorVerdict struct {
	ID     string
	Status string
	Line   int
}

// MarkAnchors records verdicts. It writes only the status columns — never the
// content — so stamping a drawer's anchor cannot trigger a re-embed of the memory
// itself. Unknown ids are ignored rather than failing the batch: a client working
// from a stale listing should still get credit for the anchors it did check.
func (s *Service) MarkAnchors(ctx context.Context, teamID string, verdicts []AnchorVerdict) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	n := 0
	for _, v := range verdicts {
		switch v.Status {
		case AnchorVerified, AnchorDrifted, AnchorMissing, AnchorUnchecked:
		default:
			return n, fmt.Errorf("%w: unknown anchor status %q", ErrInvalidInput, v.Status)
		}
		res := s.repo.db.WithContext(ctx).Model(&anchorRow{}).
			Where("team_id = ? AND id = ?", teamID, v.ID).
			Updates(map[string]any{"status": v.Status, "line": v.Line, "checked_at": now})
		if res.Error != nil {
			return n, fmt.Errorf("mark anchor: %w", res.Error)
		}
		n += int(res.RowsAffected)
	}
	return n, nil
}

// AnchorsForDrawers returns the anchors of the given drawers, keyed by drawer id,
// so a page of search hits can carry its own staleness without a query per hit.
func (s *Service) AnchorsForDrawers(ctx context.Context, teamID string, ids []string) (map[string][]Anchor, error) {
	out := map[string][]Anchor{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []anchorRow
	if err := s.repo.reader.WithContext(ctx).
		Where("team_id = ? AND drawer_id IN ?", teamID, ids).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load anchors: %w", err)
	}
	for _, r := range rows {
		out[r.DrawerID] = append(out[r.DrawerID], Anchor{
			ID: r.ID, DrawerID: r.DrawerID, Repo: r.Repo, Path: r.Path,
			Snippet: r.Snippet, Status: r.Status, Line: r.Line, CheckedAt: r.CheckedAt,
		})
	}
	return out, nil
}

// AnchorsForMemories returns anchors attached to any chunk of each logical
// memory, keyed by memory root. Add currently pins chunk zero, but retrieval may
// select a child; resolving siblings here keeps staleness attached to the memory
// instead of to whichever chunk happened to win.
func (s *Service) AnchorsForMemories(ctx context.Context, teamID string, memoryIDs []string) (map[string][]Anchor, error) {
	out := make(map[string][]Anchor, len(memoryIDs))
	// Identity only. This runs on every search, right after the caller has
	// already loaded these same memories in full, and all it needs is which
	// chunk ids belong to which root — fetching the content again would make
	// anchor resolution the largest read in the request for nothing.
	chunks, err := s.repo.MemoryChunkIDsByRoots(ctx, teamID, memoryIDs)
	if err != nil {
		return nil, fmt.Errorf("load memory chunks for anchors: %w", err)
	}
	// Walk the CALLER's roots, and each root's chunks in the chunk order the
	// repo returned. Ranging the maps instead put a memory's anchors in a
	// different order on every call, and that order is user-visible — the MCP
	// search response appends them straight through, so an agent diffing two
	// identical recalls saw its anchors move.
	ids := make([]string, 0, len(memoryIDs))
	rootOf := make(map[string]string, len(memoryIDs))
	seen := make(map[string]bool, len(memoryIDs))
	for _, root := range memoryIDs {
		if seen[root] {
			continue
		}
		seen[root] = true
		for _, id := range chunks[root] {
			ids = append(ids, id)
			rootOf[id] = root
		}
	}
	byDrawer, err := s.AnchorsForDrawers(ctx, teamID, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if anchors := byDrawer[id]; len(anchors) > 0 {
			out[rootOf[id]] = append(out[rootOf[id]], anchors...)
		}
	}
	return out, nil
}

// ReplaceAnchors swaps a drawer's anchors for a new set, returning how many were
// written.
//
// Replace rather than append, because it exists for the case where a memory's
// CONTENT changed: the old anchor pins the old text, and appending would leave it
// beside the new one, still checked, still able to mark the corrected memory
// STALE. A memory whose text is right and whose anchor is dead reads as
// out-of-date to every future session, which is the opposite of what correcting
// it was for.
//
// An empty set clears the anchors, which is the honest option when a rewrite no
// longer points at any particular code.
func (s *Service) ReplaceAnchors(ctx context.Context, teamID, drawerID string, in []AnchorInput) (int, error) {
	if drawerID == "" {
		return 0, fmt.Errorf("%w: drawer id is required", ErrInvalidInput)
	}
	if err := s.repo.db.WithContext(ctx).
		Where("team_id = ? AND drawer_id = ?", teamID, drawerID).
		Delete(&anchorRow{}).Error; err != nil {
		return 0, fmt.Errorf("clear the drawer's old anchors: %w", err)
	}
	if len(in) == 0 {
		return 0, nil
	}
	return s.AddAnchors(ctx, teamID, drawerID, in)
}
