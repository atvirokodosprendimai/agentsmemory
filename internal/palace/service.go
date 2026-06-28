package palace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"

	"gorm.io/gorm"
)

// Sentinel errors the MCP layer maps to tool-level results. Keeping them here
// lets the transport stay ignorant of gorm: the service is the only place that
// knows the persistence library.
var (
	// ErrNotFound is returned when a drawer id does not exist for the team.
	ErrNotFound = errors.New("drawer not found")
	// ErrInvalidInput is returned when a required argument is missing or empty.
	ErrInvalidInput = errors.New("invalid input")
)

// Defaults and bounds for search/recall, mirroring the frozen Python contract so
// the tool surface behaves identically (search: limit 1-100 def5, max_distance
// 0-2 def1.5; check_duplicate: threshold def0.9).
const (
	DefaultSearchLimit  = 5
	MaxSearchLimit      = 100
	DefaultMaxDistance  = 1.5
	DefaultDupThreshold = 0.9

	// searchCandidatePool is how many nearest neighbours to pull before applying
	// a wing/room filter. The vector seam's Search has no server-side filter, so
	// a filtered search must over-fetch and discard non-matching candidates in Go.
	// This is set high enough that a wing/room with results far down the global
	// ranking is not cut before the filter sees it; on the brute-force SQLite
	// backend an over-fetch is free (it scans everything anyway), and Qdrant caps
	// the scan at this bound. The principled fix — pushing the filter into the
	// store as a payload predicate — lands with the hybrid-ranking phase.
	searchCandidatePool = 10000
)

// Diary defaults, mirroring the frozen Python diary tools so the journal behaves
// identically: every entry is filed into the "diary" room, an untagged entry gets
// the "general" topic, and diary_read returns the last 10 entries by default and
// at most 100.
const (
	// DiaryRoom is the room every diary entry lives in; diary_read scopes by it
	// together with the agent, cleanly separating journal entries from memories.
	DiaryRoom = "diary"
	// DefaultDiaryTopic tags a diary entry written without an explicit topic.
	DefaultDiaryTopic = "general"
	// DefaultDiaryReadN is diary_read's window when last_n is unset.
	DefaultDiaryReadN = 10
	// MaxDiaryReadN caps diary_read's window so one call cannot scan unbounded.
	MaxDiaryReadN = 100

	// diaryTimeLayout stamps a diary entry's FiledAt with a FIXED-WIDTH, nine-digit
	// nanosecond fraction. diary_read orders by filed_at as a string (SQLite TEXT),
	// so the format must be lexicographically sortable: time.RFC3339Nano trims
	// trailing zeros, making its width vary and a string sort disagree with chrono
	// order. A zero-padded fraction keeps string order == time order, and the
	// nanosecond resolution also makes each entry's id-seed unique.
	diaryTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

// AAAKSpec is the compressed memory dialect agents use for diary and closet
// lines. It is static reference text (the get_aaak_spec tool returns it verbatim)
// so it lives as a constant rather than in storage.
const AAAKSpec = `AAAK is a compressed memory dialect MemPalace uses for efficient, human- and LLM-readable storage.

FORMAT:
  ENTITIES: 3-letter uppercase codes (ALC=Alice, JOR=Jordan).
  EMOTIONS: *markers* before text (*warm*=joy, *fierce*=determined, *raw*=vulnerable, *bloom*=tenderness).
  STRUCTURE: pipe-separated fields. FAM: family | PROJ: projects | ⚠: warnings.
  DATES: ISO (2026-03-31). COUNTS: Nx = N mentions. IMPORTANCE: ★ to ★★★★★.

Read AAAK naturally — expand codes mentally, treat *markers* as emotional context.
When writing AAAK: use entity codes, mark emotions, keep structure tight.`

// Embedder turns text into vectors. It is declared at the consumer (per Go's
// "accept interfaces" guidance) so the service depends on the capability, not on
// the concrete Ollama client — which also makes it trivial to fake in tests.
type Embedder interface {
	// Embed returns one vector per input string, in order.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	// EmbedOne is the single-string convenience used by search and check_duplicate.
	EmbedOne(ctx context.Context, input string) ([]float32, error)
}

// Service is the core memory loop: it files drawers (chunk -> embed -> store) and
// recalls them (embed query -> nearest-neighbour -> join metadata). It composes
// the metadata Repo, an Embedder, and the vector store seam; everything is
// tenant-scoped by the teamID argument, which is also the vector namespace.
type Service struct {
	repo    *Repo
	embed   Embedder
	vectors store.VectorStore
	dim     int // embedding dimension new namespaces are created with (bge-m3 = 1024)
	// mineLocks serializes concurrent mines of the same (team, source) within this
	// process, so two re-mines cannot interleave their purge-then-write and leave
	// both content versions behind. It is the in-process analogue of the frozen
	// miner's per-source mine_lock. Note: it does NOT coordinate across horizontally
	// scaled instances — a cross-instance guard would need a DB advisory lock.
	mineLocks keyedMutex
	// graphLocks serializes a team's recompute_graph the same way: a recompute
	// replaces hallways and delete-and-rebuilds entity tunnels, so two concurrent
	// recomputes of one team could interleave and leave a stale rebuild. Same
	// in-process caveat as mineLocks.
	graphLocks keyedMutex
}

// NewService wires the collaborators. dim is the embedding width used to create a
// tenant's vector namespace on first write (the actual width of returned vectors
// is authoritative and used in Add; dim is only the seed/fallback).
func NewService(repo *Repo, embed Embedder, vectors store.VectorStore, dim int) *Service {
	return &Service{repo: repo, embed: embed, vectors: vectors, dim: dim}
}

// AddInput is the add_drawer payload: where the memory goes (wing, room — both
// required), the verbatim text, and optional provenance/date metadata.
type AddInput struct {
	Wing        string
	Room        string
	Content     string
	SourceFile  string
	ContentDate string
}

// Add files a memory: it chunks oversized content, embeds every chunk in one
// batch, writes the vectors, then writes the metadata rows. Vectors are written
// before rows so a row never exists without its embedding — search joins row to
// vector, and the inverse orphan (a vector with no row) is harmless because
// search skips ids it cannot resolve. It returns the drawers created (one per
// chunk), so the tool can report their ids.
func (s *Service) Add(ctx context.Context, teamID string, in AddInput) ([]Drawer, error) {
	wing := strings.TrimSpace(in.Wing)
	room := strings.TrimSpace(in.Room)
	content := strings.TrimSpace(in.Content)
	if wing == "" || room == "" || content == "" {
		return nil, fmt.Errorf("%w: wing, room and content are required", ErrInvalidInput)
	}

	chunks := ChunkText(content, ChunkSize, ChunkOverlap, ChunkMin)
	vectors, err := s.embedChunks(ctx, chunks)
	if err != nil {
		return nil, err
	}

	filedAt := time.Now().UTC().Format(time.RFC3339)
	drawers := make([]Drawer, len(chunks))
	for i, c := range chunks {
		// The first chunk is the parent the rest of a multi-chunk write point
		// back to; the first chunk itself has no parent.
		parentID := ""
		if i > 0 {
			parentID = drawers[0].ID
		}
		drawers[i] = Drawer{
			ID:          DrawerID(teamID, wing, room, in.SourceFile, c.Index, c.Content),
			TeamID:      teamID,
			Wing:        wing,
			Room:        room,
			SourceFile:  in.SourceFile,
			ChunkIndex:  c.Index,
			Content:     c.Content,
			FiledAt:     filedAt,
			ContentDate: strings.TrimSpace(in.ContentDate),
			ParentID:    parentID,
		}
	}

	// Re-filing a *named* source replaces it wholesale: purge the source's prior
	// drawers (rows + vectors) before writing the new set, so shrinking the
	// content cannot leave orphaned higher-index chunks behind. A source-less add
	// is a standalone memory (deduped by its content-hash id), so it is not purged.
	if in.SourceFile != "" {
		if err := s.purgeSource(ctx, teamID, wing, room, in.SourceFile); err != nil {
			return nil, err
		}
	}

	if err := s.storeDrawers(ctx, teamID, drawers, vectors); err != nil {
		return nil, err
	}
	return drawers, nil
}

// embedChunks embeds a batch of chunks, returning one vector per chunk in order.
// It is the shared embed step of every filing path (add_drawer, diary_write), so
// the chunk -> vector contract is single-sourced rather than copied per tool.
func (s *Service) embedChunks(ctx context.Context, chunks []Chunk) ([][]float32, error) {
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed drawer: %w", err)
	}
	return vectors, nil
}

// storeDrawers is the shared persistence tail every filing path ends in: ensure
// the tenant's vector namespace exists, write the embeddings, then write the
// metadata rows. Vectors are written before rows so a row never exists without
// its embedding — search joins row to vector, and the inverse orphan (a vector
// with no row) is harmless because search skips ids it cannot resolve. The vector
// width the model returned is authoritative for namespace creation, so a mis-set
// s.dim can never make EnsureNamespace and Upsert disagree. drawers and vectors
// must be index-aligned and the same length.
func (s *Service) storeDrawers(ctx context.Context, teamID string, drawers []Drawer, vectors [][]float32) error {
	dim := s.dim
	if len(vectors) > 0 {
		dim = len(vectors[0])
	}
	if err := s.vectors.EnsureNamespace(ctx, teamID, dim); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	points := make([]store.Point, len(drawers))
	for i, d := range drawers {
		// Payload carries only the cheap filter keys; the verbatim content stays
		// single-sourced in the drawers table, joined back by id at search time.
		points[i] = store.Point{
			ID:      d.ID,
			Vector:  vectors[i],
			Payload: map[string]any{"wing": d.Wing, "room": d.Room},
		}
	}
	if err := s.vectors.Upsert(ctx, teamID, points); err != nil {
		return fmt.Errorf("upsert vectors: %w", err)
	}
	if err := s.repo.Save(ctx, drawers); err != nil {
		return fmt.Errorf("save drawers: %w", err)
	}
	return nil
}

// purgeSource deletes every drawer (row + vector) previously filed from a source
// within a (team, wing, room), so a re-add of that source replaces rather than
// accumulates. Vectors are dropped by the ids the rows carry, then the rows.
func (s *Service) purgeSource(ctx context.Context, teamID, wing, room, source string) error {
	ids, err := s.repo.IDsBySource(ctx, teamID, wing, room, source)
	if err != nil {
		return fmt.Errorf("list source drawers: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.vectors.Delete(ctx, teamID, ids); err != nil {
		return fmt.Errorf("purge source vectors: %w", err)
	}
	if err := s.repo.DeleteBySource(ctx, teamID, wing, room, source); err != nil {
		return fmt.Errorf("purge source rows: %w", err)
	}
	return nil
}

// Get returns one drawer, mapping an unknown id to ErrNotFound.
func (s *Service) Get(ctx context.Context, teamID, id string) (Drawer, error) {
	d, err := s.repo.Get(ctx, teamID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Drawer{}, ErrNotFound
	}
	return d, err
}

// Update edits an existing drawer's content/wing/room in place (its id is
// stable). A supplied field must be non-empty — update_drawer must not be a back
// door around the non-empty invariant add_drawer enforces (a blank wing/room
// would file the drawer into an unaddressable taxonomy bucket). Any change
// re-embeds the drawer's final content and re-upserts the vector *before* the row
// is written, so a failed embed leaves the drawer fully consistent in its old
// state rather than with a row ahead of its stale vector. A no-op patch just
// returns the current drawer.
func (s *Service) Update(ctx context.Context, teamID, id string, patch DrawerPatch) (Drawer, error) {
	for _, f := range []struct {
		name string
		val  *string
	}{{"content", patch.Content}, {"wing", patch.Wing}, {"room", patch.Room}} {
		if f.val != nil && strings.TrimSpace(*f.val) == "" {
			return Drawer{}, fmt.Errorf("%w: %s cannot be set empty", ErrInvalidInput, f.name)
		}
	}

	current, err := s.Get(ctx, teamID, id) // also maps unknown id -> ErrNotFound
	if err != nil {
		return Drawer{}, err
	}

	// Nothing to change.
	if patch.Content == nil && patch.Wing == nil && patch.Room == nil {
		return current, nil
	}

	// Compute the post-patch state and refresh the derived index first.
	finalContent, finalWing, finalRoom := current.Content, current.Wing, current.Room
	if patch.Content != nil {
		finalContent = *patch.Content
	}
	if patch.Wing != nil {
		finalWing = *patch.Wing
	}
	if patch.Room != nil {
		finalRoom = *patch.Room
	}
	vec, err := s.embed.EmbedOne(ctx, finalContent)
	if err != nil {
		return Drawer{}, fmt.Errorf("re-embed updated drawer: %w", err)
	}
	point := store.Point{
		ID:      id,
		Vector:  vec,
		Payload: map[string]any{"wing": finalWing, "room": finalRoom},
	}
	if err := s.vectors.Upsert(ctx, teamID, []store.Point{point}); err != nil {
		return Drawer{}, fmt.Errorf("re-upsert updated vector: %w", err)
	}

	// Index is current; now commit the authoritative row.
	updated, err := s.repo.Update(ctx, teamID, id, patch)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Drawer{}, ErrNotFound
	}
	if err != nil {
		return Drawer{}, err
	}
	return updated, nil
}

// Delete removes a drawer's metadata row and its vector. The row goes first so
// the authoritative record is gone before the derived index; a failed vector
// delete leaves an orphan the next search harmlessly skips.
func (s *Service) Delete(ctx context.Context, teamID, id string) error {
	if err := s.repo.Delete(ctx, teamID, id); err != nil {
		return fmt.Errorf("delete drawer row: %w", err)
	}
	if err := s.vectors.Delete(ctx, teamID, []string{id}); err != nil {
		return fmt.Errorf("delete drawer vector: %w", err)
	}
	return nil
}

// List paginates a team's drawers, optionally narrowed to a wing and/or room.
func (s *Service) List(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	return s.repo.List(ctx, teamID, wing, room, limit, offset)
}

// SearchQuery is the mempalace_search input.
type SearchQuery struct {
	Query       string
	Wing        string  // optional filter
	Room        string  // optional filter
	Limit       int     // 1..100, defaults to DefaultSearchLimit
	MaxDistance float64 // drop hits farther than this; <=0 disables the filter
}

// Search recalls drawers by hybrid relevance to a query. It embeds the query and
// over-fetches a pool of nearest vector neighbours, applies the wing/room and
// max-distance filters, then RE-RANKS the survivors by a convex blend of vector
// similarity and lexical Okapi-BM25 (rankHybrid) before returning the top page.
// The blend matches the frozen searcher — vector finds the semantically near,
// BM25 rewards literal term overlap — and beats either alone. Closet boost (the
// third frozen signal) arrives with the mining phase that builds closets; until
// then Score is the vector+BM25 fusion and Distance the raw cosine distance.
func (s *Service) Search(ctx context.Context, teamID string, q SearchQuery) ([]SearchHit, error) {
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return nil, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	// Cap by runes, not bytes: the contract caps queries at 250 characters, and a
	// byte slice could split a multibyte rune into invalid UTF-8 before it reaches
	// the embedder and tokenizer.
	if r := []rune(query); len(r) > 250 {
		query = string(r[:250])
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	vec, err := s.embed.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Over-fetch a re-rank pool: BM25 can only reorder what vector retrieval
	// surfaced, so the pool must be wider than the page (limit*multiplier) for a
	// lexical match outside the top-N to be promoted into it. When filtering by
	// wing/room the survivors are a subset of the pool, so over-fetch far more
	// (searchCandidatePool) to be sure the page can still be filled.
	candidateK := limit * hybridCandidateMultiplier
	filtering := q.Wing != "" || q.Room != ""
	if filtering && candidateK < searchCandidatePool {
		candidateK = searchCandidatePool
	}
	hits, err := s.vectors.Search(ctx, teamID, vec, candidateK)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// Resolve candidate ids to rows in one query.
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.GetMany(ctx, teamID, ids)
	if err != nil {
		return nil, fmt.Errorf("load drawer rows: %w", err)
	}

	// Keep the survivors that pass the wing/room/max-distance filters, in vector
	// order, carrying content (for BM25) and distance (for vector similarity).
	survivors := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		d, ok := rows[h.ID]
		if !ok {
			continue // orphan vector (row deleted) — skip
		}
		if q.Wing != "" && d.Wing != q.Wing {
			continue
		}
		if q.Room != "" && d.Room != q.Room {
			continue
		}
		distance := distanceFromScore(h.Score)
		if q.MaxDistance > 0 && distance > q.MaxDistance {
			continue
		}
		survivors = append(survivors, SearchHit{Drawer: d, Distance: distance})
	}

	// Closet boost: search the team's closets with the same query and let the
	// best-matching closets lift the rank of the drawers from their source. Closets
	// are a SIGNAL, never a gate — a team that has never mined has no closets, so a
	// failed or empty closet search simply yields no boosts and search proceeds.
	closetBoostBySource := s.closetBoosts(ctx, teamID, vec)

	// Hybrid re-rank the survivors by content + distance + closet boost, then page.
	docs := make([]string, len(survivors))
	dists := make([]float64, len(survivors))
	boosts := make([]float64, len(survivors))
	for i, h := range survivors {
		docs[i] = h.Drawer.Content
		dists[i] = h.Distance
		boosts[i] = closetBoostBySource[h.Drawer.SourceFile]
	}
	ranked := rankHybrid(query, docs, dists, boosts)

	results := make([]SearchHit, 0, limit)
	for _, r := range ranked {
		if len(results) >= limit {
			break
		}
		hit := survivors[r.Index]
		hit.Score = r.Fused
		hit.BM25 = r.BM25
		hit.ClosetBoost = r.Boost
		results = append(results, hit)
	}
	return results, nil
}

// DuplicateResult is the check_duplicate verdict: whether the most similar
// existing drawer crosses the threshold, that similarity, and the match (nil
// when nothing is similar enough).
type DuplicateResult struct {
	IsDuplicate bool
	Similarity  float64
	Drawer      *Drawer
}

// CheckDuplicate reports whether content is near-identical to an existing drawer.
// It embeds the content, takes the single nearest neighbour, and compares its
// cosine similarity to threshold (callers pass DefaultDupThreshold when unset).
func (s *Service) CheckDuplicate(ctx context.Context, teamID, content string, threshold float64) (DuplicateResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return DuplicateResult{}, fmt.Errorf("%w: content is required", ErrInvalidInput)
	}
	// Cosine similarity lives in [-1, 1]; a duplicate threshold outside [0, 1] is
	// nonsense (>1 can never match an exact duplicate, <0 marks everything a
	// duplicate), so clamp it rather than trust a stray argument.
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 1 {
		threshold = 1
	}
	vec, err := s.embed.EmbedOne(ctx, content)
	if err != nil {
		return DuplicateResult{}, fmt.Errorf("embed content: %w", err)
	}
	hits, err := s.vectors.Search(ctx, teamID, vec, 1)
	if err != nil {
		return DuplicateResult{}, fmt.Errorf("vector search: %w", err)
	}
	if len(hits) == 0 {
		return DuplicateResult{IsDuplicate: false}, nil
	}
	top := hits[0]
	sim := float64(top.Score)
	res := DuplicateResult{IsDuplicate: sim >= threshold, Similarity: sim}
	if res.IsDuplicate {
		if d, err := s.repo.Get(ctx, teamID, top.ID); err == nil {
			res.Drawer = &d
		}
	}
	return res, nil
}

// Taxonomy is the get_taxonomy view: every wing with its rooms and counts.
type Taxonomy struct {
	Wings []TaxonomyWing `json:"wings"`
}

// TaxonomyWing is one wing in the taxonomy: its totals and the rooms inside it.
type TaxonomyWing struct {
	Wing    string     `json:"wing"`
	Drawers int        `json:"drawers"`
	Rooms   []RoomStat `json:"rooms"`
}

// GetTaxonomy assembles the wing -> rooms tree from the two indexed
// aggregations, so an agent can see the shape of a team's memory before searching.
func (s *Service) GetTaxonomy(ctx context.Context, teamID string) (Taxonomy, error) {
	wings, err := s.repo.Wings(ctx, teamID)
	if err != nil {
		return Taxonomy{}, err
	}
	rooms, err := s.repo.Rooms(ctx, teamID, "")
	if err != nil {
		return Taxonomy{}, err
	}
	byWing := make(map[string][]RoomStat, len(wings))
	for _, r := range rooms {
		byWing[r.Wing] = append(byWing[r.Wing], r)
	}
	tax := Taxonomy{Wings: make([]TaxonomyWing, 0, len(wings))}
	for _, w := range wings {
		tax.Wings = append(tax.Wings, TaxonomyWing{
			Wing:    w.Wing,
			Drawers: w.Drawers,
			Rooms:   byWing[w.Wing],
		})
	}
	return tax, nil
}

// Wings and Rooms expose the list_wings / list_rooms aggregations directly.
func (s *Service) Wings(ctx context.Context, teamID string) ([]WingStat, error) {
	return s.repo.Wings(ctx, teamID)
}

// Rooms lists a team's rooms, optionally within one wing.
func (s *Service) Rooms(ctx context.Context, teamID, wing string) ([]RoomStat, error) {
	return s.repo.Rooms(ctx, teamID, wing)
}

// Reconnect re-readies a tenant's vector namespace and confirms the store is
// reachable. The Python tool invalidated a cached Qdrant client; this server is
// stateless (no per-session cache), so reconnect has no client to drop — it is
// instead a cheap liveness probe agents can call to verify the backend before a
// burst of writes. EnsureNamespace is idempotent, so re-running it is safe.
func (s *Service) Reconnect(ctx context.Context, teamID string) error {
	if err := s.vectors.EnsureNamespace(ctx, teamID, s.dim); err != nil {
		return fmt.Errorf("reconnect: vector store unreachable: %w", err)
	}
	return nil
}

// DiaryWriteInput is the diary_write payload: whose journal (Agent), the AAAK
// entry text, an optional Topic (defaulting to DefaultDiaryTopic), and an
// optional Wing (defaulting to the agent's own wing).
type DiaryWriteInput struct {
	Agent string
	Entry string
	Topic string
	Wing  string
}

// DiaryWriteResult reports what diary_write filed: the logical entry id (the
// first chunk's id), the normalized agent and topic, the entry's timestamp, how
// many chunks it became, and — only when it chunked — every physical chunk id.
type DiaryWriteResult struct {
	EntryID   string
	Agent     string
	Topic     string
	Timestamp string
	Chunks    int
	ChunkIDs  []string
}

// WriteDiary files an agent's journal entry. It mirrors the frozen tool: the
// agent name is lowercased (so reads are case-insensitive, #1243), the topic
// defaults to "general", and the wing defaults to the agent's own wing
// (wing_<agent>) unless one is supplied. The entry rides the same chunk -> embed
// -> store machinery as add_drawer, but — unlike add_drawer's content-hashed,
// idempotent ids — each diary id folds in the write timestamp, so journaling the
// *same* reflection twice keeps both entries instead of overwriting one: a
// journal is append-only. (The frozen tool used a non-idempotent add for exactly
// this reason; the timestamp seed makes a same-id collision effectively
// impossible, so reusing the idempotent upsert store path is safe.)
func (s *Service) WriteDiary(ctx context.Context, teamID string, in DiaryWriteInput) (DiaryWriteResult, error) {
	agent, err := SanitizeName(in.Agent, "agent_name")
	if err != nil {
		return DiaryWriteResult{}, err
	}
	agent = strings.ToLower(agent)

	entry, err := SanitizeContent(in.Entry)
	if err != nil {
		return DiaryWriteResult{}, err
	}

	topic := in.Topic
	if strings.TrimSpace(topic) == "" {
		topic = DefaultDiaryTopic
	}
	if topic, err = SanitizeName(topic, "topic"); err != nil {
		return DiaryWriteResult{}, err
	}

	wing := strings.TrimSpace(in.Wing)
	if wing == "" {
		// Default to the agent's own wing. The agent is already sanitized and
		// lowercased; spaces become underscores so the result still satisfies the
		// safe-name pattern (underscores are legal in a name's interior).
		wing = "wing_" + strings.ReplaceAll(agent, " ", "_")
	} else if wing, err = SanitizeName(wing, "wing"); err != nil {
		return DiaryWriteResult{}, err
	}

	// One timestamp per write: it stamps every chunk's FiledAt (so diary_read can
	// order entries newest-first) and seeds the id (so the entry is unique).
	// RFC3339Nano gives enough resolution that two successive writes never collide.
	now := time.Now().UTC()
	filedAt := now.Format(diaryTimeLayout)
	date := now.Format("2006-01-02")
	// seed makes the id unique per write: the timestamp orders entries, the random
	// nonce guarantees uniqueness even if two writes (e.g. on two scaled instances)
	// land on the same nanosecond — without it a same-ns, same-content write would
	// collide and the idempotent store upsert would silently overwrite a prior
	// journal entry. The clean filedAt (no nonce) is what stamps FiledAt for sorting.
	seed := diarySeed(filedAt)

	// SanitizeContent guarantees a non-empty entry, so diaryChunks yields >= 1
	// chunk and drawers[0] below is always present. diaryChunks (not ChunkText)
	// keeps the journal entry verbatim — no overlap, no trim — matching the frozen
	// tool. EntryID is the first chunk's id (our ParentID model makes chunk 0 the
	// canonical, fetchable handle); the frozen tool's logical handle was opaque and
	// un-fetchable, but for the common single-chunk AAAK entry the two coincide.
	chunks := diaryChunks(entry, ChunkSize)
	vectors, err := s.embedChunks(ctx, chunks)
	if err != nil {
		return DiaryWriteResult{}, err
	}

	drawers := make([]Drawer, len(chunks))
	for i, c := range chunks {
		parentID := ""
		if i > 0 {
			parentID = drawers[0].ID
		}
		drawers[i] = Drawer{
			ID:          diaryEntryID(teamID, wing, agent, topic, c.Index, c.Content, seed),
			TeamID:      teamID,
			Wing:        wing,
			Room:        DiaryRoom,
			ChunkIndex:  c.Index,
			Content:     c.Content,
			FiledAt:     filedAt,
			ContentDate: date,
			ParentID:    parentID,
			Agent:       agent,
			Topic:       topic,
		}
	}
	if err := s.storeDrawers(ctx, teamID, drawers, vectors); err != nil {
		return DiaryWriteResult{}, err
	}

	res := DiaryWriteResult{
		EntryID:   drawers[0].ID,
		Agent:     agent,
		Topic:     topic,
		Timestamp: filedAt,
		Chunks:    len(drawers),
	}
	// A single-chunk entry's id is already EntryID; only a chunked entry needs its
	// physical ids enumerated so a caller can fetch each piece by id.
	if len(drawers) > 1 {
		res.ChunkIDs = make([]string, len(drawers))
		for i, d := range drawers {
			res.ChunkIDs[i] = d.ID
		}
	}
	return res, nil
}

// diarySeed combines the write timestamp with a random nonce to seed a diary
// id, so the id is unique even when two writes share a nanosecond. crypto/rand is
// the source; on the near-impossible event it fails, we fall back to the
// timestamp alone rather than block a journal write — at worst reintroducing the
// vanishingly small same-nanosecond collision the nonce exists to remove.
func diarySeed(filedAt string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return filedAt
	}
	return filedAt + "|" + hex.EncodeToString(b[:])
}

// DiaryEntry is one entry diary_read returns: when it was written, its topic, and
// the verbatim text — the read projection of a diary Drawer.
type DiaryEntry struct {
	Date      string `json:"date"`
	Timestamp string `json:"timestamp"`
	Topic     string `json:"topic"`
	Content   string `json:"content"`
}

// DiaryReadResult is the diary_read response: the normalized agent, the page of
// entries (newest first), the total entries in scope, and how many are shown.
type DiaryReadResult struct {
	Agent   string       `json:"agent"`
	Entries []DiaryEntry `json:"entries"`
	Total   int64        `json:"total"`
	Showing int          `json:"showing"`
}

// ReadDiary returns an agent's most recent diary entries, newest first. Like the
// frozen tool it lowercases the agent (case-insensitive reads), clamps lastN to
// [1, MaxDiaryReadN], and treats an empty wing as "every wing this agent has
// journaled in" — hook-written entries land in project wings, so a wingless read
// must still see them. Total is the full count in scope, so a caller can tell its
// journal is larger than the returned window.
func (s *Service) ReadDiary(ctx context.Context, teamID, agent, wing string, lastN int) (DiaryReadResult, error) {
	cleanAgent, err := SanitizeName(agent, "agent_name")
	if err != nil {
		return DiaryReadResult{}, err
	}
	cleanAgent = strings.ToLower(cleanAgent)

	if wing = strings.TrimSpace(wing); wing != "" {
		if wing, err = SanitizeName(wing, "wing"); err != nil {
			return DiaryReadResult{}, err
		}
	}

	if lastN <= 0 {
		lastN = DefaultDiaryReadN
	}
	if lastN > MaxDiaryReadN {
		lastN = MaxDiaryReadN
	}

	rows, err := s.repo.Diary(ctx, teamID, cleanAgent, wing, lastN)
	if err != nil {
		return DiaryReadResult{}, fmt.Errorf("read diary: %w", err)
	}
	total, err := s.repo.DiaryCount(ctx, teamID, cleanAgent, wing)
	if err != nil {
		return DiaryReadResult{}, fmt.Errorf("count diary: %w", err)
	}

	entries := make([]DiaryEntry, len(rows))
	for i, d := range rows {
		entries[i] = DiaryEntry{
			Date:      d.ContentDate,
			Timestamp: d.FiledAt,
			Topic:     d.Topic,
			Content:   d.Content,
		}
	}
	return DiaryReadResult{
		Agent:   cleanAgent,
		Entries: entries,
		Total:   total,
		Showing: len(entries),
	}, nil
}

// distanceFromScore converts a cosine similarity in [-1, 1] into a distance in
// [0, 2] (0 = identical), matching the Python contract's max_distance scale.
func distanceFromScore(score float32) float64 {
	d := 1 - float64(score)
	if d < 0 {
		return 0
	}
	if d > 2 {
		return 2
	}
	return d
}
