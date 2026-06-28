package palace

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// Mining turns a blob of text into searchable memory: it chunks the content on
// structural boundaries, files each chunk as a verbatim drawer (with extracted
// entities and a content date), and builds the closet pointer index over them.
// It is the SaaS reinterpretation of the frozen CLI miner — the agent supplies the
// content (the server has no access to the agent's filesystem), so mine takes a
// text payload plus a stable `source` that keys idempotency: re-mining the same
// source replaces its drawers and closets wholesale rather than accumulating.

const (
	// DefaultMineRoom is the room a mined source lands in when none is given. The
	// frozen miner derives a room from the file path; a text payload has none, so
	// callers either pass a room or accept this default.
	DefaultMineRoom = "general"
	// DefaultMineAgent is stamped as a drawer's author when none is supplied,
	// matching the frozen miner's default added_by.
	DefaultMineAgent = "mempalace"
	// maxSourceLen bounds the source identifier so an unbounded label can't bloat
	// rows or ids. Sources are opaque keys (a path, URL, or name), so unlike a
	// wing/room they are not run through SanitizeName.
	maxSourceLen = 512
)

// MineInput is the mine payload: the verbatim Content, the Wing it belongs to,
// an optional Room (defaults to DefaultMineRoom), a stable Source that keys
// idempotency, and an optional Agent recorded as the author.
type MineInput struct {
	Content string
	Wing    string
	Room    string
	Source  string
	Agent   string
}

// MineResult reports what a mine produced: how many drawers and closets were
// written, the resolved location, the source, and the content date that was
// detected (empty when none was found).
type MineResult struct {
	Drawers     int    `json:"drawers"`
	Closets     int    `json:"closets"`
	Wing        string `json:"wing"`
	Room        string `json:"room"`
	Source      string `json:"source"`
	ContentDate string `json:"content_date,omitempty"`
}

// Mine files a text payload into the palace. It validates and normalizes the
// inputs, extracts the content date once, chunks the content on structural
// boundaries, files each chunk as a drawer (entities + date + author), and builds
// the closet index. The whole source is purged first so a re-mine replaces rather
// than accumulates. Content that yields no chunk (shorter than the minimum after
// trimming) is a valid no-op: the prior source is still purged and zero is
// reported.
func (s *Service) Mine(ctx context.Context, teamID string, in MineInput) (MineResult, error) {
	wing, err := SanitizeName(in.Wing, "wing")
	if err != nil {
		return MineResult{}, err
	}
	room := strings.TrimSpace(in.Room)
	if room == "" {
		room = DefaultMineRoom
	}
	if room, err = SanitizeName(room, "room"); err != nil {
		return MineResult{}, err
	}
	content, err := SanitizeContent(in.Content)
	if err != nil {
		return MineResult{}, err
	}
	source, err := sanitizeSource(in.Source)
	if err != nil {
		return MineResult{}, err
	}
	agent := strings.TrimSpace(in.Agent)
	if agent == "" {
		agent = DefaultMineAgent
	}
	if agent, err = SanitizeName(agent, "agent"); err != nil {
		return MineResult{}, err
	}
	agent = strings.ToLower(agent)

	contentDate := extractContentDate(content)
	now := time.Now().UTC()
	filedAt := now.Format(time.RFC3339)
	filedAtDate := now.Format("2006-01-02")

	chunks := mineChunkText(content, MineChunkSize, MineChunkOverlap, MineChunkMin)

	// Purge the prior version of this source FIRST (drawers and closets, rows and
	// vectors), so a re-mine that now yields fewer — or zero — chunks cannot leave
	// orphans behind. Done before the new write, mirroring add_drawer's purge.
	if err := s.purgeSource(ctx, teamID, wing, room, source); err != nil {
		return MineResult{}, err
	}
	if err := s.purgeClosetSource(ctx, teamID, source); err != nil {
		return MineResult{}, err
	}
	if len(chunks) == 0 {
		return MineResult{Drawers: 0, Closets: 0, Wing: wing, Room: room, Source: source, ContentDate: contentDate}, nil
	}

	// Build one drawer per chunk. Entities are extracted per chunk (so co-occurrence
	// is local to a chunk); the content date is shared across the source's chunks.
	drawers := make([]Drawer, len(chunks))
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		parentID := ""
		if i > 0 {
			parentID = drawers[0].ID
		}
		drawers[i] = Drawer{
			ID:          DrawerID(teamID, wing, room, source, c.Index, c.Content),
			TeamID:      teamID,
			Wing:        wing,
			Room:        room,
			SourceFile:  source,
			ChunkIndex:  c.Index,
			Content:     c.Content,
			Entities:    extractEntities(c.Content),
			FiledAt:     filedAt,
			ContentDate: contentDate,
			ParentID:    parentID,
			Agent:       agent,
		}
		texts[i] = c.Content
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return MineResult{}, fmt.Errorf("embed mined chunks: %w", err)
	}
	if err := s.storeDrawers(ctx, teamID, drawers, vectors); err != nil {
		return MineResult{}, err
	}

	closets, err := s.buildAndStoreClosets(ctx, teamID, wing, room, source, content, contentDate, filedAt, filedAtDate, chunks, drawers)
	if err != nil {
		return MineResult{}, err
	}

	return MineResult{
		Drawers:     len(drawers),
		Closets:     closets,
		Wing:        wing,
		Room:        room,
		Source:      source,
		ContentDate: contentDate,
	}, nil
}

// buildAndStoreClosets constructs the source's closet pointer lines, packs them
// into closet documents, embeds those documents, and stores them (rows + vectors
// in the closet namespace). It returns the number of closets written. The source's
// old closets were already purged by Mine, so this only writes the new set.
func (s *Service) buildAndStoreClosets(ctx context.Context, teamID, wing, room, source, content, contentDate, filedAt, filedAtDate string, chunks []mineChunk, drawers []Drawer) (int, error) {
	drawerIDs := make([]string, len(drawers))
	for i, d := range drawers {
		drawerIDs[i] = d.ID
	}
	dateLineSeg := closetDateLineSegment(chunks[0], contentDate, filedAtDate)
	lines := buildClosetLines(source, drawerIDs, content, wing, room, dateLineSeg)
	docs := packClosets(lines, closetCharLimit)
	if len(docs) == 0 {
		return 0, nil
	}

	entities := closetEntities(content)
	closets := make([]Closet, len(docs))
	texts := make([]string, len(docs))
	for i, doc := range docs {
		closets[i] = Closet{
			ID:         closetID(teamID, wing, room, source, i+1),
			TeamID:     teamID,
			Wing:       wing,
			Room:       room,
			SourceFile: source,
			Document:   doc,
			Entities:   entities,
			FiledAt:    filedAt,
		}
		texts[i] = doc
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed closets: %w", err)
	}
	if err := s.storeClosets(ctx, teamID, closets, vectors); err != nil {
		return 0, err
	}
	return len(closets), nil
}

// storeClosets writes closet vectors (into the per-team closet namespace) and then
// the closet rows — the closet analogue of storeDrawers. Vectors first so a row is
// never indexed without its embedding; the closet payload carries the source so a
// search hit maps straight back to the drawers it should boost.
func (s *Service) storeClosets(ctx context.Context, teamID string, closets []Closet, vectors [][]float32) error {
	if len(closets) == 0 {
		return nil
	}
	ns := closetNamespace(teamID)
	dim := s.dim
	if len(vectors) > 0 {
		dim = len(vectors[0])
	}
	if err := s.vectors.EnsureNamespace(ctx, ns, dim); err != nil {
		return fmt.Errorf("ensure closet namespace: %w", err)
	}
	points := make([]store.Point, len(closets))
	for i, c := range closets {
		points[i] = store.Point{
			ID:      c.ID,
			Vector:  vectors[i],
			Payload: map[string]any{"wing": c.Wing, "room": c.Room, "source_file": c.SourceFile},
		}
	}
	if err := s.vectors.Upsert(ctx, ns, points); err != nil {
		return fmt.Errorf("upsert closet vectors: %w", err)
	}
	if err := s.repo.SaveClosets(ctx, closets); err != nil {
		return fmt.Errorf("save closets: %w", err)
	}
	return nil
}

// purgeClosetSource drops every closet (row + vector) previously built from a
// source, so a re-mine replaces rather than accumulates closets. Vectors are
// removed from the closet namespace by the ids the rows carry, then the rows.
func (s *Service) purgeClosetSource(ctx context.Context, teamID, source string) error {
	ids, err := s.repo.ClosetIDsBySource(ctx, teamID, source)
	if err != nil {
		return fmt.Errorf("list source closets: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.vectors.Delete(ctx, closetNamespace(teamID), ids); err != nil {
		return fmt.Errorf("purge source closet vectors: %w", err)
	}
	if err := s.repo.DeleteClosetsBySource(ctx, teamID, source); err != nil {
		return fmt.Errorf("purge source closet rows: %w", err)
	}
	return nil
}

// closetBoosts searches the team's closet index with the query vector and returns
// a source_file -> boost map for the hybrid re-rank. Each of the top closet hits
// (only the first len(closetRankBoosts) positions can boost) lends its source the
// boost for that position, provided the closet is within closetDistanceCap; the
// first position a source appears at decides its boost, mirroring the frozen
// searcher. Closets are a ranking SIGNAL, never a gate: a team that has never
// mined has no closet namespace, so any error or empty result simply yields no
// boosts and search proceeds on vector+BM25 alone.
func (s *Service) closetBoosts(ctx context.Context, teamID string, vec []float32) map[string]float64 {
	boosts := map[string]float64{}
	hits, err := s.vectors.Search(ctx, closetNamespace(teamID), vec, len(closetRankBoosts))
	if err != nil || len(hits) == 0 {
		return boosts
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.ClosetsByIDs(ctx, teamID, ids)
	if err != nil {
		return boosts
	}
	seen := map[string]struct{}{}
	for i, h := range hits {
		c, ok := rows[h.ID]
		if !ok {
			continue // closet vector with no row (purged) — skip
		}
		if _, dup := seen[c.SourceFile]; dup {
			continue // a source's boost is fixed by the first position it appears at
		}
		seen[c.SourceFile] = struct{}{}
		if distanceFromScore(h.Score) <= closetDistanceCap {
			boosts[c.SourceFile] = closetRankBoosts[i]
		}
	}
	return boosts
}

// sanitizeSource validates a mine source identifier: non-empty after trimming,
// within maxSourceLen, and free of NUL bytes. Unlike a wing/room it is an opaque
// idempotency key (a path, URL, or label), so it is not held to the safe-name
// pattern — only to these minimal safety bounds.
func sanitizeSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", fmt.Errorf("%w: source is required", ErrInvalidInput)
	}
	if len([]rune(source)) > maxSourceLen {
		return "", fmt.Errorf("%w: source exceeds maximum length of %d characters", ErrInvalidInput, maxSourceLen)
	}
	if strings.ContainsRune(source, 0) {
		return "", fmt.Errorf("%w: source contains null bytes", ErrInvalidInput)
	}
	return source, nil
}
