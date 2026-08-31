package palace

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// keyedMutex is a set of mutexes keyed by string, used to serialize work that must
// not run concurrently for the same key (here, mining the same team+source) while
// leaving different keys fully parallel. The zero value is ready to use.
type keyedMutex struct {
	m sync.Map // key -> *sync.Mutex
}

// lock acquires the mutex for key and returns its unlock func. Callers defer the
// returned func. Mutexes are created on first use and retained (the key space is
// bounded by the set of sources a team mines, which is small relative to memory).
func (k *keyedMutex) lock(key string) func() {
	mu, _ := k.m.LoadOrStore(key, &sync.Mutex{})
	l := mu.(*sync.Mutex)
	l.Lock()
	return l.Unlock
}

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
func (s *Service) Mine(ctx context.Context, teamID string, in MineInput) (result MineResult, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageMine)
	defer func() {
		endStage(sp, err, attribute.Int("am.drawers", result.Drawers), attribute.Int("am.closets", result.Closets))
	}()
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

	// Serialize re-mines of this exact source within the process so two concurrent
	// mines cannot interleave the purge-then-write below. \x00 cannot appear in a
	// sanitized teamID or a source (sanitizeSource rejects NUL), so the joined key
	// is unambiguous.
	unlock := s.mineLocks.lock(teamID + "\x00" + source)
	defer unlock()

	contentDate := extractContentDate(source, content)
	now := time.Now().UTC()
	filedAt := now.Format(time.RFC3339)
	filedAtDate := now.Format("2006-01-02")

	chunks := mineChunkText(content, MineChunkSize, MineChunkOverlap, MineChunkMin)

	// Purge the prior version of this source FIRST (drawers and closets, rows and
	// vectors), so a re-mine that now yields fewer — or zero — chunks cannot leave
	// orphans behind. Done before the new write, mirroring add_drawer's purge.
	// The surviving keys are computed from the CHUNKS rather than from the drawers,
	// because the purge runs before the drawers are built — and the key recipe is
	// exactly the chunk's own locating tuple, so the two cannot drift.
	keep := make([]string, 0, len(chunks))
	for _, c := range chunks {
		keep = append(keep, contentKeyOf(teamID, wing, room, source, c.Index, c.Content))
	}
	if err := s.purgeSourceOn(ctx, s.repo, teamID, wing, room, source, keep); err != nil {
		return MineResult{}, err
	}
	// ⚠ THE CLOSET PURGE IS DEFERRED, THE DRAWER PURGE IS NOT. A closet's id is
	// its position in the source rather than a hash of its text, so "the same
	// closet, unchanged" is only knowable by comparing documents — and the old
	// document is gone the moment the purge runs. Snapshotting first is what lets
	// an unchanged closet keep its vector instead of paying to rebuild it: after
	// the drawer reuse above, closets are ALL a re-mine of an unchanged corpus
	// would otherwise still embed (measured: 1 closet against 71 drawer chunks on
	// a 90k source, so 100% of what is left once the drawers are skipped).
	//
	// The orphan guarantee the eager purge gave is preserved by
	// purgeClosetSourceExcept, which drops every prior closet the new set does not
	// replace — including the zero-chunk case below, where the new set is empty
	// and everything prior is stale.
	priorClosets, err := s.repo.EmbeddedClosetDocumentsBySource(ctx, teamID, source)
	if err != nil {
		return MineResult{}, fmt.Errorf("read the source's current closets: %w", err)
	}
	if len(chunks) == 0 {
		if err := s.purgeClosetSourceExcept(ctx, teamID, source, nil); err != nil {
			return MineResult{}, err
		}
		return MineResult{Drawers: 0, Closets: 0, Wing: wing, Room: room, Source: source, ContentDate: contentDate}, nil
	}

	// Build one drawer per chunk. Entities are extracted per chunk (so co-occurrence
	// is local to a chunk); the content date is shared across the source's chunks.
	// Same reuse as Add: a re-mine of unchanged text must not rename the memory,
	// and attachDerivedEdgeTo edges the id in this slice — a fresh mint here would
	// edge an id the upsert never created.
	existing, err := s.repo.IDsByContentKeys(ctx, teamID, keep)
	if err != nil {
		return MineResult{}, fmt.Errorf("look up rows already holding these content keys: %w", err)
	}

	drawers := make([]Drawer, len(chunks))
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		parentID := ""
		if i > 0 {
			parentID = drawers[0].ID
		}
		drawers[i] = Drawer{
			ID:          mintOrReuse(existing, keep[i]),
			ContentKey:  keep[i],
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
	// ⚠ EMBED ONLY WHAT CHANGED. content is an input to the content key, so a key
	// that is already filed AND already embedded means byte-identical text at the
	// same address with a vector that still describes it — re-embedding it buys
	// nothing and costs the whole run. Measured by an operator 2026-08-31 on a
	// CPU-only host: a re-mine to add ONE new session re-embedded the entire
	// corpus, ~2.5 hours, which in practice means nobody tops a corpus up and it
	// goes stale.
	//
	// ⚠ ONE BEHAVIOUR CHANGE, STATED RATHER THAN HIDDEN: a re-mine no longer
	// refreshes vectors for unchanged text, so changing the embedding model and
	// re-mining will NOT re-embed what did not change. Mixed-model vectors in one
	// index are not comparable (see buildEmbedder), so a model switch needs the
	// namespace rebuilt rather than a re-mine — which was already true of every
	// row nothing re-mined.
	reusable, err := s.repo.EmbeddedIDsByContentKeys(ctx, teamID, keep)
	if err != nil {
		return MineResult{}, fmt.Errorf("look up rows already embedded under these content keys: %w", err)
	}
	fresh := make([]Drawer, 0, len(drawers))
	freshTexts := make([]string, 0, len(drawers))
	reused := make([]Drawer, 0, len(drawers))
	for i, d := range drawers {
		if id, ok := reusable[keep[i]]; ok && id == d.ID {
			reused = append(reused, d)
			continue
		}
		fresh = append(fresh, d)
		freshTexts = append(freshTexts, texts[i])
	}
	if len(fresh) > 0 {
		vectors, err := s.embed.Embed(ctx, freshTexts)
		if err != nil {
			return MineResult{}, fmt.Errorf("embed mined chunks: %w", err)
		}
		if err := s.storeDrawers(ctx, teamID, fresh, vectors); err != nil {
			return MineResult{}, err
		}
	}
	// Unchanged rows are still SAVED — the row write is cheap and keeps re-filing
	// semantics (filed_at, parent, entities) identical to before. Only the embed
	// and the vector upsert are skipped, and the vector under this id is the one
	// the first mine wrote for this exact content.
	if len(reused) > 0 {
		if err := s.repo.Save(ctx, reused); err != nil {
			return MineResult{}, fmt.Errorf("save drawers: %w", err)
		}
	}

	closets, err := s.buildAndStoreClosets(ctx, teamID, wing, room, source, content, contentDate, filedAt, filedAtDate, chunks, drawers, priorClosets)
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
func (s *Service) buildAndStoreClosets(ctx context.Context, teamID, wing, room, source, content, contentDate, filedAt, filedAtDate string, chunks []mineChunk, drawers []Drawer, prior map[string]string) (int, error) {
	drawerIDs := make([]string, len(drawers))
	for i, d := range drawers {
		drawerIDs[i] = d.ID
	}
	dateLineSeg := closetDateLineSegment(chunks[0], contentDate, filedAtDate)
	lines := buildClosetLines(source, drawerIDs, content, wing, room, dateLineSeg)
	docs := packClosets(lines, closetCharLimit)
	if len(docs) == 0 {
		// Nothing replaces the prior set, so all of it is stale.
		return 0, s.purgeClosetSourceExcept(ctx, teamID, source, nil)
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
	// Keep an unchanged closet: same id, byte-identical document, and it already
	// had a vector (prior holds only embedded rows). Everything else is rebuilt.
	fresh := make([]Closet, 0, len(closets))
	freshTexts := make([]string, 0, len(closets))
	keep := make(map[string]bool, len(closets))
	for i, c := range closets {
		if doc, ok := prior[c.ID]; ok && doc == c.Document {
			keep[c.ID] = true
			continue
		}
		fresh = append(fresh, c)
		freshTexts = append(freshTexts, texts[i])
	}
	// Drop the prior closets nothing in the new set keeps — the orphan guarantee
	// the eager purge used to give, narrowed to what actually became stale.
	if err := s.purgeClosetSourceExcept(ctx, teamID, source, keep); err != nil {
		return 0, err
	}
	if len(fresh) > 0 {
		vectors, err := s.embed.Embed(ctx, freshTexts)
		if err != nil {
			return 0, fmt.Errorf("embed closets: %w", err)
		}
		if err := s.storeClosets(ctx, teamID, fresh, vectors); err != nil {
			return 0, err
		}
	}
	return len(closets), nil
}

// storeClosets writes closet vectors (into the per-team closet namespace) and then
// the closet rows — the closet analogue of storeDrawers. Vectors first so a row is
// never indexed without its embedding; the closet payload carries the source so a
// search hit maps straight back to the drawers it should boost.
func (s *Service) storeClosets(ctx context.Context, teamID string, closets []Closet, vectors [][]float32) error {
	if err := s.upsertClosetVectors(ctx, teamID, closets, vectors); err != nil {
		return err
	}
	if err := s.repo.SaveClosets(ctx, closets); err != nil {
		return fmt.Errorf("save closets: %w", err)
	}
	return nil
}

// upsertClosetVectors ensures the per-tenant closet namespace and writes the
// closet embeddings only — no rows. Shared by storeClosets (sync) and the
// background embed worker (which backfills vectors for absorbed closet rows).
func (s *Service) upsertClosetVectors(ctx context.Context, teamID string, closets []Closet, vectors [][]float32) error {
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
	return nil
}

// purgeClosetSource drops every closet (row + vector) previously built from a
// source, so a re-mine replaces rather than accumulates closets. Vectors are
// removed from the closet namespace by the ids the rows carry, then the rows.
func (s *Service) purgeClosetSourceExcept(ctx context.Context, teamID, source string, keep map[string]bool) error {
	ids, err := s.repo.ClosetIDsBySource(ctx, teamID, source)
	if err != nil {
		return fmt.Errorf("list source closets: %w", err)
	}
	stale := make([]string, 0, len(ids))
	for _, id := range ids {
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if err := s.vectors.Delete(ctx, closetNamespace(teamID), stale); err != nil {
		return fmt.Errorf("purge source closet vectors: %w", err)
	}
	if err := s.repo.DeleteClosetsByIDsForSource(ctx, teamID, stale); err != nil {
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
	return s.closetBoostsAt(ctx, teamID, vec, s.closetBoostScale)
}

// closetBoostsAt is closetBoosts with the scale supplied by the caller rather
// than read from the service.
//
// It exists for the eval: an arm named after the closet prior has to measure the
// prior at full strength whatever the server happens to serve, or the arm that
// decides whether serving it was right ends up comparing a disabled prior with
// itself. Search has the opposite requirement and keeps reading the served
// scale, which is why the split is here rather than a parameter added to every
// caller.
func (s *Service) closetBoostsAt(ctx context.Context, teamID string, vec []float32, scale float64) map[string]float64 {
	closetCtx, sp := telemetry.Start(ctx, telemetry.StageCloset, attribute.Float64("am.scale", scale))
	boosts := map[string]float64{}
	if scale == 0 {
		// The prior is off: skip the closet vector search too, not just the
		// arithmetic — this is one network call per search.
		sp.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonScaleZero))
		return boosts
	}
	// No filter: a closet summarises a whole source, so its boost is not scoped to
	// the wing/room a search happens to be narrowed to — the drawers it lifts are
	// filtered on their own way in.
	searchRes, err := s.vectors.Search(closetCtx, closetNamespace(teamID), vec, len(closetRankBoosts), nil)
	if err != nil {
		sp.End(telemetry.FailedOpen, telemetry.AttrReason(telemetry.ReasonError))
		return boosts
	}
	hits := searchRes.H
	if len(hits) == 0 {
		sp.End(telemetry.Ran, attribute.Int("am.count", 0))
		return boosts
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.ClosetsByIDs(closetCtx, teamID, ids)
	if err != nil {
		sp.End(telemetry.FailedOpen, telemetry.AttrReason(telemetry.ReasonError))
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
		// Rank decides the ceiling, proximity decides how much of it applies. A
		// closet that is barely related contributes almost nothing instead of the
		// same +0.40 a perfect match would.
		if strength := closetBoostStrength(distanceFromScore(h.Score)); strength > 0 {
			boosts[c.SourceFile] = closetRankBoosts[i] * strength * scale
		}
	}
	sp.End(telemetry.Ran, attribute.Int("am.count", len(boosts)))
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
